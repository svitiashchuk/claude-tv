package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const hourKey = "2006-01-02T15"

// Bucket aggregates one local clock hour.
type Bucket struct {
	Tokens   int64            `json:"tokens"`
	Cost     float64          `json:"cost"`
	ByFamily map[string]int64 `json:"by_family,omitempty"`
}

func (b *Bucket) add(ev Event) {
	t := ev.Usage.Tokens()
	b.Tokens += t
	b.Cost += ev.Usage.Cost(ev.Model)
	if b.ByFamily == nil {
		b.ByFamily = map[string]int64{}
	}
	b.ByFamily[Family(ev.Model)] += t
}

// Store keeps hourly history beyond Claude Code's own transcript cleanup (30 days by default).
type Store struct {
	path  string
	Hours map[string]Bucket `json:"hours"`
}

func DefaultStorePath() string {
	dir := os.Getenv("XDG_DATA_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "claude-tv", "hours.json")
}

// OpenStore loads the store; a missing file is an empty store. An empty path keeps it in memory.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, Hours: map[string]Bucket{}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Hours == nil {
		s.Hours = map[string]Bucket{}
	}
	return s, nil
}

// Merge folds freshly scanned events into the store and reports whether anything changed.
// Transcripts only lose data (cleanup), never gain it for a past hour, so the larger bucket wins.
func (s *Store) Merge(events []Event) bool {
	fresh := map[string]*Bucket{}
	for _, ev := range events {
		k := ev.Time.Local().Format(hourKey)
		if fresh[k] == nil {
			fresh[k] = &Bucket{}
		}
		fresh[k].add(ev)
	}
	changed := false
	for k, b := range fresh {
		if old, ok := s.Hours[k]; !ok || b.Tokens > old.Tokens {
			s.Hours[k] = *b
			changed = true
		}
	}
	return changed
}

func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Hour returns the bucket for the local hour containing t.
func (s *Store) Hour(t time.Time) Bucket { return s.Hours[t.Local().Format(hourKey)] }
