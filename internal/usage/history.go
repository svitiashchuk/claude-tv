package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Usage is the token accounting of one assistant message.
type Usage struct {
	Input, Output, CacheWrite, CacheWrite5m, CacheWrite1h, CacheRead int64
	Fast                                                             bool
}

// Tokens counts everything the model processed, cache reads included (same as ccusage's total).
func (u Usage) Tokens() int64 { return u.Input + u.Output + u.CacheWrite + u.CacheRead }

// Event is one billed assistant message from Claude Code's transcripts.
type Event struct {
	Time    time.Time
	Model   string
	Project string
	Usage   Usage
}

type rawLine struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	RequestID string    `json:"requestId"`
	Cwd       string    `json:"cwd"`
	Message   *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int64  `json:"input_tokens"`
			Output        int64  `json:"output_tokens"`
			CacheCreate   int64  `json:"cache_creation_input_tokens"`
			CacheRead     int64  `json:"cache_read_input_tokens"`
			Speed         string `json:"speed"`
			CacheCreation *struct {
				W5m int64 `json:"ephemeral_5m_input_tokens"`
				W1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// parseLine returns the event and its dedupe key, or ok=false for lines that carry no billing.
func parseLine(line []byte) (ev Event, key string, ok bool) {
	if !bytes.Contains(line, []byte(`"usage"`)) || !bytes.Contains(line, []byte(`"assistant"`)) {
		return
	}
	var r rawLine
	if json.Unmarshal(line, &r) != nil || r.Type != "assistant" || r.Message == nil || r.Message.Usage == nil {
		return
	}
	m := r.Message
	if m.Model == "" || m.Model == "<synthetic>" {
		return
	}
	u := Usage{Input: m.Usage.Input, Output: m.Usage.Output, CacheWrite: m.Usage.CacheCreate,
		CacheRead: m.Usage.CacheRead, Fast: m.Usage.Speed == "fast"}
	if cc := m.Usage.CacheCreation; cc != nil {
		u.CacheWrite5m, u.CacheWrite1h = cc.W5m, cc.W1h
	}
	if u.Tokens() == 0 {
		return
	}
	// Claude Code writes one line per content block, each repeating the message's usage.
	key = m.ID + ":" + r.RequestID
	return Event{Time: r.Timestamp, Model: m.Model, Project: filepath.Base(r.Cwd), Usage: u}, key, true
}

type fileState struct {
	size   int64
	mod    time.Time
	events []Event
	keys   []string
}

// Scanner reads Claude Code transcripts, re-parsing only files that changed since the last scan.
type Scanner struct {
	Root  string
	mu    sync.Mutex
	files map[string]*fileState
}

func NewScanner(root string) *Scanner {
	return &Scanner{Root: root, files: map[string]*fileState{}}
}

// DefaultRoot is ~/.claude/projects, or $CLAUDE_CONFIG_DIR/projects.
func DefaultRoot() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "projects")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// Scan returns every deduplicated event currently on disk.
func (s *Scanner) Scan() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	err := filepath.WalkDir(s.Root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[path] = true
		if fs, ok := s.files[path]; ok && fs.size == info.Size() && fs.mod.Equal(info.ModTime()) {
			return nil
		}
		fs, err := parseFile(path)
		if err != nil {
			return nil
		}
		fs.size, fs.mod = info.Size(), info.ModTime()
		s.files[path] = fs
		return nil
	})
	for path := range s.files {
		if !seen[path] {
			delete(s.files, path)
		}
	}
	var out []Event
	dedup := map[string]bool{}
	for _, fs := range s.files {
		for i, ev := range fs.events {
			if dedup[fs.keys[i]] {
				continue
			}
			dedup[fs.keys[i]] = true
			out = append(out, ev)
		}
	}
	return out, err
}

func parseFile(path string) (*fileState, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fs := &fileState{}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if ev, key, ok := parseLine(line); ok {
			fs.events = append(fs.events, ev)
			fs.keys = append(fs.keys, key)
		}
		if err == io.EOF {
			return fs, nil
		}
		if err != nil {
			return fs, err
		}
	}
}
