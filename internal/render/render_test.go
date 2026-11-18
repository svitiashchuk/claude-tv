package render

import (
	"errors"
	"testing"
	"time"

	"github.com/svitiashchuk/claude-tv/internal/usage"
)

// Every screen of every theme must draw without panicking, with full data and with none at all
// (fresh install, Claude Code logged out, limits endpoint down).
func TestAllScreensRender(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 20, 0, 0, time.Local)
	empty, _ := usage.OpenStore("")
	snaps := map[string]*usage.Snapshot{
		"demo":  usage.Demo(now),
		"empty": usage.Build(now, empty, nil, usage.Limits{}, errors.New("token expired")),
		"maxed": func() *usage.Snapshot {
			s := usage.Demo(now)
			s.Limits.Session.Util, s.Limits.Week.Util = 140, 100
			return s
		}(),
	}
	for _, tn := range ThemeNames() {
		for name, s := range snaps {
			for _, sc := range Screens {
				img := ThemeByName(tn).Render(sc, s)
				if b := img.Bounds(); b.Dx() != Size || b.Dy() != Size {
					t.Fatalf("%s/%s/%s: %v", tn, name, sc, b)
				}
			}
		}
	}
}

func TestFormatting(t *testing.T) {
	cases := map[string]string{
		fmtTokens(999): "999", fmtTokens(812_400): "812k", fmtTokens(1_840_000): "1.84M",
		fmtTokens(2_000_000): "2M", fmtTokens(3_500_000_000): "3.5B",
		fmtMoney(14.2): "$14.20", fmtMoney(142.4): "$142", fmtMoney(1420): "$1.4k",
		fmtDur(108 * time.Minute): "1h 48m", fmtDur(9 * time.Minute): "9m", fmtDur(77 * time.Hour): "3d 5h",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}
