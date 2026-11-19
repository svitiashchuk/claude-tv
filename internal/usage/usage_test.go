package usage

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two content blocks of one message (same id+requestId, repeated usage), one later message,
// a user line, and a synthetic one.
const transcript = `{"type":"assistant","timestamp":"2026-11-18T10:00:00Z","requestId":"r1","cwd":"/home/x/proj","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":1000,"cache_creation_input_tokens":2000,"cache_read_input_tokens":100000,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2000}}}}
{"type":"assistant","timestamp":"2026-11-18T10:00:01Z","requestId":"r1","cwd":"/home/x/proj","message":{"id":"m1","model":"claude-opus-5-5","usage":{"input_tokens":10,"output_tokens":1000,"cache_creation_input_tokens":2000,"cache_read_input_tokens":100000,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2000}}}}
{"type":"user","timestamp":"2026-11-18T10:01:00Z","message":{"role":"user","content":"hi"}}
{"type":"assistant","timestamp":"2026-11-18T11:30:00Z","requestId":"r2","cwd":"/home/x/other","message":{"id":"m2","model":"claude-sonnet-5-5","usage":{"input_tokens":5,"output_tokens":500,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"speed":"fast"}}}
{"type":"assistant","timestamp":"2026-11-18T11:31:00Z","requestId":"r3","message":{"id":"m3","model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0}}}
`

func writeTranscript(t *testing.T) string {
	dir := t.TempDir()
	p := filepath.Join(dir, "proj", "session.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScanDedupesContentBlocks(t *testing.T) {
	sc := NewScanner(writeTranscript(t))
	evs, err := sc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 billed messages, got %d", len(evs))
	}
	var total int64
	for _, e := range evs {
		total += e.Usage.Tokens()
	}
	if want := int64(10 + 1000 + 2000 + 100000 + 5 + 500); total != want {
		t.Fatalf("tokens = %d, want %d", total, want)
	}
	// unchanged files are served from cache and still deduped
	again, _ := sc.Scan()
	if len(again) != 2 {
		t.Fatalf("rescan: want 2, got %d", len(again))
	}
}

func TestCost(t *testing.T) {
	opus := Usage{Input: 10, Output: 1000, CacheWrite: 2000, CacheWrite1h: 2000, CacheRead: 100000}
	// $4 in, $20 out, $8 1h-write, $0.20 read per MTok
	want := (10*4 + 1000*20 + 2000*8 + 100000*0.20) / 1e6
	if got := opus.Cost("claude-opus-5-5"); math.Abs(got-want) > 1e-12 {
		t.Fatalf("opus cost %v want %v", got, want)
	}
	fast := Usage{Input: 5, Output: 500, Fast: true}
	if got, want := fast.Cost("claude-sonnet-5-5"), 2*(5*2+500*10)/1e6; math.Abs(got-want) > 1e-12 {
		t.Fatalf("fast cost %v want %v", got, want)
	}
	if PriceFor("claude-opus-5-5").Input != 4 || PriceFor("claude-opus-5").Input != 5 {
		t.Fatal("opus-5-5 must not fall through to opus-5 pricing")
	}
}

func TestStoreKeepsHistoryAfterTranscriptCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hours.json")
	s, _ := OpenStore(path)
	evs, _ := NewScanner(writeTranscript(t)).Scan()
	if !s.Merge(evs) {
		t.Fatal("first merge should change the store")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, _ := OpenStore(path)
	if s2.Merge(nil) {
		t.Fatal("merging nothing (transcripts deleted) must not change history")
	}
	h := s2.Hour(time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC))
	if h.Tokens != 103010 || h.ByFamily["opus"] != 103010 {
		t.Fatalf("10:00 bucket = %+v", h)
	}
}

func TestParseLimits(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	body := `{"five_hour":{"utilization":62.0,"resets_at":"2026-11-18T16:48:00+00:00"},
	          "seven_day":{"utilization":38,"resets_at":"2026-11-21T09:00:00.123+00:00"},
	          "seven_day_opus":null}`
	l, err := parseLimits([]byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Session.Known || l.Session.Util != 62 || l.Session.ResetsAt.Hour() != 16 {
		t.Fatalf("session = %+v", l.Session)
	}
	if !l.Week.Known || l.Week.Util != 38 || l.Week.ResetsAt.IsZero() {
		t.Fatalf("week = %+v", l.Week)
	}
	l, _ = parseLimits([]byte(`{"five_hour":null}`), now)
	if l.Session.Known || l.Week.Known {
		t.Fatal("missing windows must be unknown")
	}
}

func TestForecast(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	// window started 2h ago (resets in 3h); 60% used → 30%/h → 100% in 1h20m, before the reset
	w := Window{Known: true, Util: 60, ResetsAt: now.Add(3 * time.Hour), Length: 5 * time.Hour}
	eta, hits, ok := w.Forecast(now)
	if !ok || !hits || eta.Sub(now).Round(time.Minute) != 80*time.Minute {
		t.Fatalf("eta %v hits %v ok %v", eta.Sub(now), hits, ok)
	}
	// 20% used in 2h → 10%/h → would need 8h, the window resets first
	w.Util = 20
	if _, hits, ok := w.Forecast(now); !ok || hits {
		t.Fatalf("slow pace should not hit the limit: hits %v ok %v", hits, ok)
	}
	// too early in the window to extrapolate
	w = Window{Known: true, Util: 5, ResetsAt: now.Add(5*time.Hour - 2*time.Minute), Length: 5 * time.Hour}
	if _, _, ok := w.Forecast(now); ok {
		t.Fatal("2 minutes in is too early to forecast")
	}
	if got := (Window{ResetsAt: now.Add(3 * time.Hour), Length: 5 * time.Hour}).EvenPace(now); math.Abs(got-40) > 1e-9 {
		t.Fatalf("even pace %v want 40", got)
	}
}

func TestSessionCurveEndsAtUtilization(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.UTC)
	w := Window{Known: true, Util: 50, ResetsAt: now.Add(3 * time.Hour), Length: 5 * time.Hour}
	evs := []Event{
		{Time: now.Add(-90 * time.Minute), Model: "claude-opus-5-5", Usage: Usage{Output: 1000}},
		{Time: now.Add(-30 * time.Minute), Model: "claude-opus-5-5", Usage: Usage{Output: 3000}},
		{Time: now.Add(-6 * time.Hour), Model: "claude-opus-5-5", Usage: Usage{Output: 9999}}, // before window
	}
	pts := sessionCurve(now, w, evs)
	if last := pts[len(pts)-1]; math.Abs(last.Y-50) > 1e-9 || math.Abs(last.X-0.4) > 1e-9 {
		t.Fatalf("last point %+v", last)
	}
	if math.Abs(pts[1].Y-12.5) > 1e-9 { // first message is a quarter of the window's spend
		t.Fatalf("first step %+v", pts[1])
	}
}

func TestTokenSources(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	dir := t.TempDir()
	future := time.Now().Add(time.Hour).UnixMilli()
	later := time.Now().Add(5 * time.Hour).UnixMilli()
	cred := func(tok string, exp int64) []byte {
		return []byte(fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"expiresAt":%d}}`, tok, exp))
	}
	c := &LimitsClient{CredentialsPath: filepath.Join(dir, ".credentials.json")}

	// macOS: no file, token only in the Keychain (security prints it with a trailing newline)
	c.Keychain = func() ([]byte, error) { return append(cred("from-keychain", future), '\n'), nil }
	if tok, err := c.token(); err != nil || tok != "from-keychain" {
		t.Fatalf("keychain only: %q %v", tok, err)
	}

	// both present and diverged: the one that expires last wins
	os.WriteFile(c.CredentialsPath, cred("from-file", later), 0o600)
	if tok, _ := c.token(); tok != "from-file" {
		t.Fatalf("diverged: got %q, want the later-expiring file token", tok)
	}

	// expired everywhere
	c.Keychain = nil
	os.WriteFile(c.CredentialsPath, cred("old", time.Now().Add(-time.Minute).UnixMilli()), 0o600)
	if _, err := c.token(); err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}

	// nothing anywhere
	os.Remove(c.CredentialsPath)
	if _, err := c.token(); err == nil || !strings.Contains(err.Error(), "log in") {
		t.Fatalf("want a log-in hint, got %v", err)
	}

	// a long-lived token from `claude setup-token`
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "from-env")
	if tok, _ := c.token(); tok != "from-env" {
		t.Fatalf("env: got %q", tok)
	}
}

func TestParseLimitsIgnoresUnexpectedFields(t *testing.T) {
	body := `{"five_hour":{"utilization":10,"resets_at":"2026-11-18T16:48:00Z"},"extra_usage":{"is_enabled":false},"note":"x","seven_day":null}`
	l, err := parseLimits([]byte(body), time.Now())
	if err != nil || !l.Session.Known || l.Week.Known {
		t.Fatalf("%+v %v", l, err)
	}
}
