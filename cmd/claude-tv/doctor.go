package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/svitiashchuk/claude-tv/internal/device"
	"github.com/svitiashchuk/claude-tv/internal/usage"
)

const apiHost = "api.anthropic.com"

// doctorCmd checks each step between claude-tv and its data, so failures can be pinned down.
// It never prints the login token.
func doctorCmd(args []string) {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dev := fs.String("device", os.Getenv("CLAUDE_TV_DEVICE"), "also check the clock at this address")
	fs.Parse(args)
	ctx := context.Background()
	report := func(ok bool, what, detail string) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
		}
		fmt.Printf("%s  %-22s %s\n", mark, what, detail)
	}

	// 1. login
	lc := usage.NewLimitsClient()
	if l, err := lc.Login(); err != nil {
		report(false, "Claude Code login", err.Error())
	} else {
		d := "found in " + l.Source
		if !l.ExpiresAt.IsZero() {
			d += ", valid for " + time.Until(l.ExpiresAt).Round(time.Minute).String()
		}
		report(true, "Claude Code login", d)
	}

	// 2. proxy settings (Go reads these; it does not read macOS system proxy settings)
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
		if v := os.Getenv(k); v != "" {
			if u, err := url.Parse(v); err == nil && u.User != nil {
				u.User = url.User("***")
				v = u.String()
			}
			report(true, "proxy "+k, v)
		}
	}

	// 3. DNS and TCP
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, apiHost)
	if err != nil {
		report(false, "DNS "+apiHost, err.Error())
	} else {
		report(true, "DNS "+apiHost, fmt.Sprint(ips))
	}
	start := time.Now()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(apiHost, "443"), 10*time.Second)
	if err != nil {
		report(false, "TCP connect :443", err.Error())
	} else {
		report(true, "TCP connect :443", fmt.Sprintf("%s via %s", time.Since(start).Round(time.Millisecond), conn.RemoteAddr()))
		conn.Close()
	}

	// 4. TLS, with classic and with post-quantum key exchange
	handshake := func(curves []tls.CurveID) (time.Duration, error) {
		start := time.Now()
		d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: apiHost, CurvePreferences: curves}}
		tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		c, err := d.DialContext(tctx, "tcp", net.JoinHostPort(apiHost, "443"))
		if err != nil {
			return 0, err
		}
		c.Close()
		return time.Since(start), nil
	}
	if d, err := handshake([]tls.CurveID{tls.X25519, tls.CurveP256}); err != nil {
		report(false, "TLS (classic)", err.Error())
	} else {
		report(true, "TLS (classic)", d.Round(time.Millisecond).String()+"  ← claude-tv uses this")
	}
	if d, err := handshake(nil); err != nil {
		report(false, "TLS (post-quantum)", err.Error()+"  (fine: claude-tv doesn't need it)")
	} else {
		report(true, "TLS (post-quantum)", d.Round(time.Millisecond).String())
	}

	// 5. the real request
	fctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if l, err := lc.Fetch(fctx); err != nil {
		report(false, "plan limits", err.Error())
	} else {
		report(true, "plan limits", fmt.Sprintf("session %.0f%%, week %.0f%%", l.Session.Util, l.Week.Util))
	}

	// 6. the clock
	if *dev != "" {
		g := device.NewGeekMagic(*dev)
		if desc, err := g.Detect(fctx); err != nil {
			report(false, "clock", err.Error())
		} else {
			report(true, "clock", desc)
		}
	}
}
