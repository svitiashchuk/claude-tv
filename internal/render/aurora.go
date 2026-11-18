package render

import (
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/svitiashchuk/claude-tv/internal/usage"

	"github.com/fogleman/gg"
)

// Aurora: glowing violet / pink / mint light on deep plum, thin rounded type.
type aurora struct{}

func init() { register(aurora{}) }

var (
	auBase   = hex("#170E2B")
	auViolet = hex("#7C5CFF")
	auPink   = hex("#FF6FB5")
	auMint   = hex("#3DE0C8")
	auText   = hex("#F4EEFF")
	auMuted  = hex("#BFB3DD")
	auFaint  = color.NRGBA{255, 255, 255, 26}

	auSession = []color.NRGBA{auViolet, auPink}
	auWeek    = []color.NRGBA{auMint, auViolet}
	auFamily  = map[string]color.NRGBA{
		"opus": auPink, "sonnet": auMint, "fable": hex("#FFC86B"), "haiku": hex("#8FB7FF"), "other": auMuted,
	}
)

const (
	auDisplay = "Unbounded-Light"
	auDispMed = "Unbounded-Medium"
	auBody    = "Manrope-Medium"
	auBold    = "Manrope-Bold"
)

func (aurora) Name() string { return "aurora" }

func (a aurora) Render(screen string, s *usage.Snapshot) image.Image {
	dc := gg.NewContext(Size, Size)
	a.background(dc, s)
	switch screen {
	case "today":
		a.today(dc, s)
	case "pace":
		a.pace(dc, s)
	case "week":
		a.week(dc, s)
	case "clock":
		a.clock(dc, s)
	default:
		a.now(dc, s)
	}
	return dc.Image()
}

// background: three soft lights that drift slowly through the day, so the screen feels alive.
func (aurora) background(dc *gg.Context, s *usage.Snapshot) {
	dc.SetColor(auBase)
	dc.Clear()
	t := float64(s.Now.Hour()*60+s.Now.Minute()) / (24 * 60) * 2 * math.Pi
	blob(dc, 50+18*math.Sin(t), 40+12*math.Cos(t*2), 165, auViolet, 0.62)
	blob(dc, 200+14*math.Cos(t), 70+16*math.Sin(t), 140, auPink, 0.42)
	blob(dc, 140+20*math.Sin(t*3), 235, 150, auMint, 0.30)
}

func (a aurora) noLimits(dc *gg.Context, s *usage.Snapshot, y float64) {
	msg := "usage limits unavailable"
	if s.LimitsErr != nil && strings.Contains(s.LimitsErr.Error(), "expired") {
		msg = "open Claude Code to refresh login"
	}
	text(dc, msg, auBody, 10, auMuted, 120, y, 0.5, 0.5)
}

// now: two concentric rings, outer = 5-hour session, inner = week, with an even-pace marker.
func (a aurora) now(dc *gg.Context, s *usage.Snapshot) {
	ses, wk := s.Limits.Session, s.Limits.Week
	cx, cy := 120.0, 120.0
	dc.SetLineWidth(12)
	dc.SetColor(auFaint)
	dc.DrawCircle(cx, cy, 98)
	dc.Stroke()
	dc.SetLineWidth(7)
	dc.DrawCircle(cx, cy, 78)
	dc.Stroke()
	glow(dc, 5, 2, func(l *gg.Context) {
		arcGradient(l, cx, cy, 98, 12, ses.Util/100, auSession)
		arcGradient(l, cx, cy, 78, 7, wk.Util/100, auWeek)
	})
	if ses.Known && !ses.ResetsAt.IsZero() {
		// where you'd be at a perfectly even pace through this window
		ang := -math.Pi/2 + 2*math.Pi*ses.EvenPace(s.Now)/100
		dc.SetColor(auText)
		dc.DrawCircle(cx+113*math.Cos(ang), cy+113*math.Sin(ang), 2.2)
		dc.Fill()
	}
	if !ses.Known {
		text(dc, "—", auDisplay, 46, auText, cx, cy-6, 0.5, 0.5)
		a.noLimits(dc, s, cy+28)
		return
	}
	n := pct(ses.Util)
	w := measure(dc, n, auDisplay, 50) + measure(dc, "%", auDisplay, 18) + 2
	x0 := cx - w/2
	text(dc, n, auDisplay, 50, auText, x0, cy+8, 0, 0)
	text(dc, "%", auDisplay, 18, auText, x0+measure(dc, n, auDisplay, 50)+2, cy+8, 0, 0)
	text(dc, "session · "+fmtDur(ses.ResetsAt.Sub(s.Now))+" left", auBold, 10.5, auText, cx, cy+28, 0.5, 0.5)
	if wk.Known {
		text(dc, "week "+pct(wk.Util)+"%", auBold, 10, auMint, cx, cy+45, 0.5, 0.5)
	}
}

// today: tokens per hour as glowing pill bars; the current hour is outlined.
func (a aurora) today(dc *gg.Context, s *usage.Snapshot) {
	text(dc, "today", auBody, 10, auMuted, 16, 26, 0, 0)
	big := fmtTokens(s.Today.Tokens)
	text(dc, big, auDisplay, fitSize(dc, big, auDisplay, 32, 140), auText, 15, 62, 0, 0)
	text(dc, fmtMoney(s.Today.Cost)+" at API prices", auBody, 9.5, auMuted, 16, 80, 0, 0)

	names, share := s.FamilyShare()
	for i := 0; i < len(names) && i < 3; i++ {
		y := 28 + float64(i)*15
		label := strings.ToUpper(names[i][:1]) + names[i][1:] + " " + pct(share[i]*100) + "%"
		text(dc, label, auBold, 10, auFamily[names[i]], 224, y, 1, 0)
	}

	var peak int64 = 1
	for _, b := range s.Today.Hours {
		peak = max(peak, b.Tokens)
	}
	const base, height, left, step = 204.0, 100.0, 14.0, 9.2
	cur := s.Now.Hour()
	glow(dc, 4, 1, func(l *gg.Context) {
		for h, b := range s.Today.Hours {
			x := left + float64(h)*step
			if b.Tokens == 0 {
				l.SetColor(auFaint)
				l.DrawCircle(x+3.2, base-2, 1.3)
				l.Fill()
				continue
			}
			bh := math.Max(6, float64(b.Tokens)/float64(peak)*height)
			g := gg.NewLinearGradient(0, base, 0, base-height)
			g.AddColorStop(0, auViolet)
			g.AddColorStop(1, auPink)
			l.SetFillStyle(g)
			l.DrawRoundedRectangle(x, base-bh, 6.4, bh, 3.2)
			l.Fill()
		}
	})
	if b := s.Today.Hours[cur]; b.Tokens > 0 {
		bh := math.Max(6, float64(b.Tokens)/float64(peak)*height)
		dc.SetColor(auText)
		dc.SetLineWidth(1.2)
		dc.DrawRoundedRectangle(left+float64(cur)*step-0.6, base-bh-0.6, 7.6, bh+1.2, 3.8)
		dc.Stroke()
	}
	for _, h := range []int{0, 6, 12, 18} {
		text(dc, pad2(h), auBody, 8.5, auMuted, left+float64(h)*step, 222, 0, 0)
	}
	text(dc, "now", auBold, 8.5, auText, left+float64(cur)*step+3.2, 222, 0.5, 0)
}

// pace: the 5-hour window as a curve, projected to the limit.
func (a aurora) pace(dc *gg.Context, s *usage.Snapshot) {
	text(dc, "spent today · API prices", auBody, 9.5, auMuted, 120, 24, 0.5, 0.5)
	m := fmtMoney(s.Today.Cost)
	text(dc, m, auDispMed, fitSize(dc, m, auDispMed, 38, 200), auText, 120, 64, 0.5, 0)

	ses := s.Limits.Session
	const x0, x1, yTop, yBot = 16.0, 224.0, 104.0, 196.0
	X := func(f float64) float64 { return x0 + (x1-x0)*f }
	Y := func(p float64) float64 { return yBot - (yBot-yTop)*math.Min(p, 100)/100 }

	dc.SetColor(alpha(auText, 0.28))
	dc.SetDash(2, 3)
	dc.SetLineWidth(1)
	dc.DrawLine(x0, yTop, x1, yTop)
	dc.Stroke()
	dc.SetDash()
	text(dc, "limit", auBody, 8.5, auMuted, x0, yTop-5, 0, 0)
	dc.SetColor(auFaint)
	dc.DrawLine(x0, yBot, x1, yBot)
	dc.Stroke()

	if !ses.Known || len(s.SessionCurve) < 2 {
		a.noLimits(dc, s, 150)
		return
	}
	last := s.SessionCurve[len(s.SessionCurve)-1]
	eta, hits, ok := ses.Forecast(s.Now)
	if ok {
		// projection: straight line at the current average pace, clipped at the limit or the reset
		endX, endY := projection(last)
		dc.SetColor(auPink)
		dc.SetLineWidth(1.6)
		dc.SetDash(3, 4)
		dc.DrawLine(X(last.X), Y(last.Y), X(endX), Y(endY))
		dc.Stroke()
		dc.SetDash()
		if hits {
			dc.SetLineWidth(2)
			dc.DrawCircle(X(endX), Y(endY), 4.5)
			dc.Stroke()
		}
	}
	glow(dc, 4, 2, func(l *gg.Context) {
		stops := []color.NRGBA{auMint, auViolet, auPink}
		for i := 1; i < len(s.SessionCurve); i++ {
			p, q := s.SessionCurve[i-1], s.SessionCurve[i]
			l.SetColor(gradientAt(stops, q.X))
			l.SetLineWidth(3)
			l.SetLineCapRound()
			l.DrawLine(X(p.X), Y(p.Y), X(q.X), Y(q.Y))
			l.Stroke()
		}
	})
	dc.SetColor(auText)
	dc.DrawCircle(X(last.X), Y(last.Y), 3.6)
	dc.Fill()

	switch {
	case ok && hits:
		pre := "limit at "
		tm := eta.Local().Format("15:04")
		w := measure(dc, pre, auBold, 11.5) + measure(dc, tm, auBold, 11.5)
		text(dc, pre, auBold, 11.5, auText, 120-w/2, 222, 0, 0)
		text(dc, tm, auBold, 11.5, auPink, 120-w/2+measure(dc, pre, auBold, 11.5), 222, 0, 0)
	default:
		text(dc, "on pace · resets "+ses.ResetsAt.Local().Format("15:04"), auBold, 11.5, auMint, 120, 222, 0.5, 0)
	}
}

// week: a 7 × 24 heat grid of hourly tokens, today at the bottom.
func (a aurora) week(dc *gg.Context, s *usage.Snapshot) {
	var total int64
	var cost float64
	var peak int64 = 1
	for _, d := range s.Week {
		total += d.Tokens
		cost += d.Cost
		for _, b := range d.Hours {
			peak = max(peak, b.Tokens)
		}
	}
	text(dc, "last 7 days", auBody, 10, auMuted, 16, 26, 0, 0)
	big := fmtTokens(total)
	text(dc, big, auDisplay, fitSize(dc, big, auDisplay, 28, 130), auText, 15, 58, 0, 0)
	text(dc, fmtMoney(cost), auBold, 13, auPink, 224, 40, 1, 0)
	if wk := s.Limits.Week; wk.Known {
		text(dc, "week "+pct(wk.Util)+"%", auBold, 10, auMint, 224, 56, 1, 0)
	}

	const left, top, cw, ch = 30.0, 78.0, 8.0, 17.0
	stops := []color.NRGBA{alpha(auViolet, 0.35), alpha(auViolet, 0.8), auPink, hex("#FFD1E8")}
	glow(dc, 3, 1, func(l *gg.Context) {
		for r, d := range s.Week {
			for h, b := range d.Hours {
				x, y := left+float64(h)*cw, top+float64(r)*ch
				if b.Tokens == 0 {
					l.SetColor(auFaint)
					l.DrawRoundedRectangle(x+1, y+1, cw-2, ch-3, 1.5)
					l.Fill()
					continue
				}
				f := math.Pow(float64(b.Tokens)/float64(peak), 1.4)
				l.SetColor(gradientAt(stops, f))
				l.DrawRoundedRectangle(x+0.5, y+0.5, cw-1, ch-2, 2)
				l.Fill()
			}
		}
	})
	for r, d := range s.Week {
		c := auMuted
		if r == len(s.Week)-1 {
			c = auText
		}
		text(dc, d.Date.Format("Mon")[:2], auBold, 8.5, c, 14, top+float64(r)*ch+ch/2-1, 0, 0.5)
	}
	for _, h := range []int{0, 6, 12, 18} {
		text(dc, pad2(h), auBody, 8, auMuted, left+float64(h)*cw, 214, 0, 0)
	}
}

// clock: the time, with the two limits as slim glowing bars.
func (a aurora) clock(dc *gg.Context, s *usage.Snapshot) {
	now := s.Now.Local()
	text(dc, now.Format("15:04"), auDisplay, 58, auText, 120, 96, 0.5, 0)
	text(dc, now.Format("Monday 2 January"), auBody, 11, auMuted, 120, 120, 0.5, 0)

	if s.Active {
		status := "working · " + s.Project
		w := measure(dc, status, auBold, 10)
		glow(dc, 3, 2, func(l *gg.Context) {
			l.SetColor(auMint)
			l.DrawCircle(120-w/2-4, 142.5, 3)
			l.Fill()
		})
		text(dc, status, auBold, 10, auMint, 120+5, 146, 0.5, 0)
	} else {
		text(dc, "idle", auBold, 10, auMuted, 120, 146, 0.5, 0)
	}

	bar := func(y float64, label string, w usage.Window, stops []color.NRGBA) {
		text(dc, label, auBold, 9.5, auMuted, 24, y-6, 0, 0)
		v := "—"
		if w.Known {
			v = pct(w.Util) + "%"
		}
		text(dc, v, auBold, 9.5, auText, 216, y-6, 1, 0)
		dc.SetColor(auFaint)
		dc.DrawRoundedRectangle(24, y, 192, 6, 3)
		dc.Fill()
		if w.Known && w.Util > 0 {
			glow(dc, 4, 1, func(l *gg.Context) {
				g := gg.NewLinearGradient(24, 0, 216, 0)
				g.AddColorStop(0, stops[0])
				g.AddColorStop(1, stops[1])
				l.SetFillStyle(g)
				l.DrawRoundedRectangle(24, y, math.Max(6, 192*math.Min(w.Util, 100)/100), 6, 3)
				l.Fill()
			})
		}
	}
	bar(184, "session", s.Limits.Session, auSession)
	bar(214, "week", s.Limits.Week, auWeek)
}

func pad2(h int) string { return string([]byte{byte('0' + h/10), byte('0' + h%10)}) }
