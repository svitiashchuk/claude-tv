package usage

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	// Keychain reads the macOS Keychain item; nil or an error means "not there".
	Keychain func() ([]byte, error)
}

func NewLimitsClient() *LimitsClient {
	home, _ := os.UserHomeDir()
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".claude")
	}
	return &LimitsClient{
		CredentialsPath: filepath.Join(dir, ".credentials.json"),
		HTTP:            &http.Client{Timeout: 30 * time.Second, Transport: NewTransport(true)},
		Keychain:        readKeychain,
	}
}

// NewTransport builds the HTTPS transport for Anthropic's API.
//
// With classic=true it offers only classic key exchanges (X25519, P-256). Go 1.24+ also offers a
// post-quantum one by default, which makes the TLS ClientHello larger than one packet; some
// routers, VPNs and firewalls drop such packets, and the handshake times out.
func NewTransport(classic bool) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = http.ProxyFromEnvironment
	t.TLSHandshakeTimeout = 15 * time.Second
	if classic {
		t.TLSClientConfig = &tls.Config{CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256}}
	}
	return t
}

// ErrTokenExpired means Claude Code hasn't refreshed its token yet; it does so next time it runs.
var ErrTokenExpired = errors.New("Claude Code login token expired; it refreshes the next time you use Claude Code")

type oauthCred struct {
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt"` // unix ms
}

func parseCred(b []byte) (oauthCred, bool) {
	var c struct {
		OAuth oauthCred `json:"claudeAiOauth"`
	}
	if json.Unmarshal(bytes.TrimSpace(b), &c) != nil || c.OAuth.AccessToken == "" {
		return oauthCred{}, false
	}
	return c.OAuth, true
}

// Login describes where the token came from, for `claude-tv doctor`. It never holds the token.
type Login struct {
	Source    string    // "CLAUDE_CODE_OAUTH_TOKEN", the file path, or "macOS Keychain"
	ExpiresAt time.Time // zero when unknown
}

// Login finds Claude Code's login: $CLAUDE_CODE_OAUTH_TOKEN, ~/.claude/.credentials.json (Linux,
// Windows), or the macOS Keychain. The file and the Keychain can disagree, so the token that
// expires last wins.
func (c *LimitsClient) Login() (Login, error) {
	l, _, err := c.login()
	return l, err
}

func (c *LimitsClient) token() (string, error) {
	_, tok, err := c.login()
	return tok, err
}

func (c *LimitsClient) login() (Login, string, error) {
	if t := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); t != "" {
		return Login{Source: "CLAUDE_CODE_OAUTH_TOKEN"}, t, nil
	}
	type cand struct {
		src string
		cr  oauthCred
	}
	var found []cand
	if b, err := os.ReadFile(c.CredentialsPath); err == nil {
		if cr, ok := parseCred(b); ok {
			found = append(found, cand{c.CredentialsPath, cr})
		}
	}
	if c.Keychain != nil {
		if b, err := c.Keychain(); err == nil {
			if cr, ok := parseCred(b); ok {
				found = append(found, cand{"macOS Keychain", cr})
			}
		}
	}
	if len(found) == 0 {
		where := c.CredentialsPath
		if runtime.GOOS == "darwin" {
			where = `the macOS Keychain ("Claude Code-credentials") or ` + where
		}
		return Login{}, "", fmt.Errorf("no Claude Code login found in %s; run `claude` and log in with your claude.ai account", where)
	}
	best := found[0]
	for _, f := range found[1:] {
		if f.cr.ExpiresAt > best.cr.ExpiresAt {
			best = f
		}
	}
	l := Login{Source: best.src}
	if best.cr.ExpiresAt > 0 {
		l.ExpiresAt = time.UnixMilli(best.cr.ExpiresAt)
		if l.ExpiresAt.Before(time.Now()) {
			return l, "", ErrTokenExpired
		}
	}
	return l, best.cr.AccessToken, nil
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
		return Limits{}, fmt.Errorf("%w (run `claude-tv doctor` to check the connection)", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return Limits{}, fmt.Errorf("usage endpoint: HTTP %d", resp.StatusCode)
	}
	return parseLimits(body, time.Now())
}

func parseLimits(body []byte, now time.Time) (Limits, error) {
	// Decode each window separately so unexpected extra fields can't break the ones we use.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return Limits{}, fmt.Errorf("usage endpoint: %w", err)
	}
	win := func(key string, length time.Duration) Window {
		var r struct {
			Utilization *float64 `json:"utilization"`
			ResetsAt    string   `json:"resets_at"`
		}
		if json.Unmarshal(raw[key], &r) != nil || r.Utilization == nil {
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
