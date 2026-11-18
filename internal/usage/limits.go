package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Window is one plan limit: how much of it is used and when it resets.
type Window struct {
	Known    bool
	Util     float64 // percent, 0..100
	ResetsAt time.Time
	Length   time.Duration
}

// Limits are the plan limits shown on claude.ai under Settings → Usage.
type Limits struct {
	Session   Window // rolling 5 hours
	Week      Window // 7 days
	FetchedAt time.Time
}

const usageURL = "https://api.anthropic.com/api/oauth/usage"

// LimitsClient reads Claude Code's own OAuth token (read-only: it never refreshes or rewrites it).
type LimitsClient struct {
	CredentialsPath string
	HTTP            *http.Client
}

func NewLimitsClient() *LimitsClient {
	home, _ := os.UserHomeDir()
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude")
	}
	return &LimitsClient{CredentialsPath: filepath.Join(dir, ".credentials.json"), HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// ErrTokenExpired means Claude Code hasn't refreshed its token yet; it does so next time it runs.
var ErrTokenExpired = errors.New("Claude Code login token expired; it refreshes the next time you use Claude Code")

func (c *LimitsClient) token() (string, error) {
	b, err := os.ReadFile(c.CredentialsPath)
	if err != nil {
		return "", fmt.Errorf("reading Claude Code credentials: %w", err)
	}
	var cred struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(b, &cred); err != nil || cred.OAuth.AccessToken == "" {
		return "", fmt.Errorf("no claude.ai login in %s", c.CredentialsPath)
	}
	if cred.OAuth.ExpiresAt > 0 && time.UnixMilli(cred.OAuth.ExpiresAt).Before(time.Now()) {
		return "", ErrTokenExpired
	}
	return cred.OAuth.AccessToken, nil
}

func (c *LimitsClient) Fetch(ctx context.Context) (Limits, error) {
	tok, err := c.token()
	if err != nil {
		return Limits{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, usageURL, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Limits{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Limits{}, fmt.Errorf("usage endpoint: HTTP %d", resp.StatusCode)
	}
	return parseLimits(body, time.Now())
}

func parseLimits(body []byte, now time.Time) (Limits, error) {
	var raw map[string]*struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    string   `json:"resets_at"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Limits{}, fmt.Errorf("usage endpoint: %w", err)
	}
	win := func(key string, length time.Duration) Window {
		r := raw[key]
		if r == nil || r.Utilization == nil {
			return Window{Length: length}
		}
		w := Window{Known: true, Util: *r.Utilization, Length: length}
		if t, err := time.Parse(time.RFC3339Nano, r.ResetsAt); err == nil {
			w.ResetsAt = t
		}
		return w
	}
	return Limits{
		Session:   win("five_hour", 5*time.Hour),
		Week:      win("seven_day", 7*24*time.Hour),
		FetchedAt: now,
	}, nil
}

// Forecast extrapolates the current window's average pace to 100%.
// ok is false when there's too little data; hits is false when the window resets first.
func (w Window) Forecast(now time.Time) (eta time.Time, hits, ok bool) {
	if !w.Known || w.ResetsAt.IsZero() || w.Util <= 0 {
		return time.Time{}, false, false
	}
	if w.Util >= 100 {
		return now, true, true
	}
	elapsed := now.Sub(w.ResetsAt.Add(-w.Length))
	if elapsed < 10*time.Minute {
		return time.Time{}, false, false
	}
	perSec := w.Util / elapsed.Seconds()
	eta = now.Add(time.Duration((100 - w.Util) / perSec * float64(time.Second)))
	return eta, eta.Before(w.ResetsAt), true
}

// EvenPace is how much of the window would be used by now at a perfectly even pace.
func (w Window) EvenPace(now time.Time) float64 {
	if w.ResetsAt.IsZero() {
		return 0
	}
	f := now.Sub(w.ResetsAt.Add(-w.Length)).Seconds() / w.Length.Seconds()
	return max(0, min(1, f)) * 100
}
