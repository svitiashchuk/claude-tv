package render

import (
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/svitiashchuk/claude-tv/internal/usage"

	"github.com/fogleman/gg"
)

// Signal: a light Swiss poster — tall condensed numerals, flat cobalt and signal-orange blocks.
type signal struct{}

func init() { register(signal{}) }

var (
	sgPaper  = hex("#E4E7E1")
	sgInk    = hex("#1B1F3B")
	sgCobalt = hex("#2D4BFF")
	sgOrange = hex("#FF5A1F")
	sgTrack  = hex("#C9CEC6")
	sgMuted  = hex("#5E6378")
)

const (
	sgNum  = "Archivo-CondBlack"
	sgSemi = "Archivo-SemiBold"
	sgReg  = "Archivo-Regular"
)

func (signal) Name() string { return "signal" }

func (g signal) Render(screen string, s *usage.Snapshot) image.Image {
	dc := gg.NewContext(Size, Size)
	dc.SetColor(sgPaper)
	dc.Clear()
	switch screen {
	case "today":
		g.today(dc, s)
	case "pace":
		g.pace(dc, s)
	case "week":
		g.week(dc, s)
	case "clock":
		g.clock(dc, s)
	default:
		g.now(dc, s)
	}
	return dc.Image()
}

func rect(dc *gg.Context, x, y, w, h float64, c color.Color) {
	dc.SetColor(c)
	dc.DrawRectangle(x, y, w, h)
	dc.Fill()
}

// bigPct draws "62%" with a smaller, wider percent sign sitting on the baseline.
func (signal) bigPct(dc *gg.Context, v float64, size float64, c color.Color, x, baseline float64, alignRight bool) {
	n := pct(v)
	w := measure(dc, n, sgNum, size) + measure(dc, "%", sgSemi, size*0.38) + 1
	if alignRight {
		x -= w
	}
	text(dc, n, sgNum, size, c, x, baseline, 0, 0)
	text(dc, "%", sgSemi, size*0.38, c, x+measure(dc, n, sgNum, size)+1, baseline, 0, 0)
}

func (g signal) unavailable(dc *gg.Context, s *usage.Snapshot, y float64) {
	msg := "Usage limits unavailable"
	if s.LimitsErr != nil && strings.Contains(s.LimitsErr.Error(), "expired") {
		msg = "Open Claude Code to refresh login"
	}
	text(dc, msg, sgSemi, 10, sgMuted, 14, y, 0, 0)
}

func (g signal) now(dc *gg.Context, s *usage.Snapshot) {
	ses, wk := s.Limits.Session, s.Limits.Week
	text(dc, "Session", sgSemi, 10, sgInk, 14, 22, 0, 0)
	if !ses.Known {
		text(dc, "—", sgNum, 120, sgInk, 10, 140, 0, 0)
		g.unavailable(dc, s, 160)
		return
	}
	text(dc, "resets "+ses.ResetsAt.Local().Format("15:04"), sgSemi, 10, sgInk, 226, 22, 1, 0)
	g.bigPct(dc, ses.Util, 132, sgInk, 9, 138, false)

	// progress track with an even-pace notch
	rect(dc, 14, 150, 212, 14, sgTrack)
	rect(dc, 14, 150, 212*math.Min(ses.Util, 100)/100, 14, sgCobalt)
	ex := 14 + 212*ses.EvenPace(s.Now)/100
	rect(dc, ex-1, 146, 2, 22, sgInk)

	rect(dc, 14, 176, 212, 1, sgInk)
	text(dc, "Week", sgSemi, 10, sgInk, 14, 194, 0, 0)
	if wk.Known {
		text(dc, wk.ResetsAt.Local().Format("Mon 15:04"), sgReg, 10, sgInk, 14, 210, 0, 0)
		g.bigPct(dc, wk.Util, 44, sgOrange, 226, 222, true)
	}
}

func (g signal) today(dc *gg.Context, s *usage.Snapshot) {
	big := fmtTokens(s.Today.Tokens)
	text(dc, big, sgNum, fitSize(dc, big, sgNum, 62, 150), sgInk, 12, 60, 0, 0)
	text(dc, "tokens today · "+fmtMoney(s.Today.Cost), sgSemi, 10, sgInk, 14, 78, 0, 0)

	names, share := s.FamilyShare()
	for i := 0; i < len(names) && i < 3; i++ {
		y := 22 + float64(i)*14
		c := sgInk
		if i == 0 {
			c = sgCobalt
		}
		label := strings.ToUpper(names[i][:1]) + names[i][1:] + " " + pct(share[i]*100) + "%"
		rect(dc, 226-measure(dc, label, sgSemi, 9.5)-11, y-7, 7, 7, c)
		text(dc, label, sgSemi, 9.5, sgInk, 226, y, 1, 0)
	}

	var peak int64 = 1
	for _, b := range s.Today.Hours {
		peak = max(peak, b.Tokens)
	}
	const base, height, left, step = 206.0, 108.0, 14.0, 8.9
	cur := s.Now.Hour()
	top, _ := s.FamilyShare()
	for h, b := range s.Today.Hours {
		if b.Tokens == 0 {
			continue
		}
		x := left + float64(h)*step
		bh := math.Max(3, float64(b.Tokens)/float64(peak)*height)
		var lead int64
		if len(top) > 0 {
			lead = b.ByFamily[top[0]]
		}
		lh := bh * float64(lead) / float64(b.Tokens)
		c := sgCobalt
		if h == cur {
			c = sgOrange
		}
		rect(dc, x, base-bh, step-1.5, bh-lh, sgInk)
		rect(dc, x, base-lh, step-1.5, lh, c)
	}
	rect(dc, 14, base, 212, 1, sgInk)
	for _, h := range []int{0, 6, 12, 18} {
		text(dc, pad2(h), sgReg, 9, sgInk, left+float64(h)*step, 224, 0, 0)
	}
}

func (g signal) pace(dc *gg.Context, s *usage.Snapshot) {
	ses := s.Limits.Session
	eta, hits, ok := ses.Forecast(s.Now)
	panel, ink := sgCobalt, color.Color(color.White)
	label, value := "Limit at this pace", "—"
	switch {
	case ok && hits:
		value = eta.Local().Format("15:04")
	case ses.Known:
		panel, ink = sgInk, sgPaper
		label, value = "On pace. Resets at", ses.ResetsAt.Local().Format("15:04")
	}
	rect(dc, 0, 0, Size, 140, panel)
	text(dc, label, sgSemi, 10, ink, 14, 22, 0, 0)
	text(dc, value, sgNum, fitSize(dc, value, sgNum, 116, 214), ink, 10, 128, 0, 0)
	if !ses.Known {
		text(dc, "Usage limits unavailable", sgSemi, 10, ink, 14, 40, 0, 0)
	}

	text(dc, "Spent today", sgSemi, 10, sgInk, 14, 160, 0, 0)
	m := fmtMoney(s.Today.Cost)
	text(dc, m, sgNum, fitSize(dc, m, sgNum, 56, 140), sgInk, 12, 214, 0, 0)
	text(dc, fmtTokens(s.BurnTokensPerHour)+" tok/h", sgSemi, 10, sgInk, 226, 160, 1, 0)
	text(dc, fmtMoney(s.BurnCostPerHour)+"/h", sgReg, 10, sgInk, 226, 176, 1, 0)
	if ok && hits {
		tw := measure(dc, "Fast", sgSemi, 10)
		dc.SetColor(sgOrange)
		dc.MoveTo(226-tw-12, 196)
		dc.LineTo(226-tw-4, 196)
		dc.LineTo(226-tw-8, 189)
		dc.ClosePath()
		dc.Fill()
		text(dc, "Fast", sgSemi, 10, sgOrange, 226, 196, 1, 0)
	}
	text(dc, "API prices", sgReg, 9, sgMuted, 226, 222, 1, 0)
}

func (g signal) week(dc *gg.Context, s *usage.Snapshot) {
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
	big := fmtTokens(total)
	m := fmtMoney(cost)
	ms := fitSize(dc, m, sgNum, 30, 76)
	text(dc, m, sgNum, ms, sgOrange, 226, 44, 1, 0)
	room := 226 - measure(dc, m, sgNum, ms) - 12 - 12
	text(dc, big, sgNum, fitSize(dc, big, sgNum, 56, room), sgInk, 12, 56, 0, 0)
	text(dc, "tokens · 7 days", sgSemi, 10, sgInk, 14, 72, 0, 0)
	if wk := s.Limits.Week; wk.Known {
		text(dc, "week "+pct(wk.Util)+"%", sgSemi, 10, sgInk, 226, 62, 1, 0)
	}

	const left, top, cw, ch = 30.0, 86.0, 8.0, 17.0
	for r, d := range s.Week {
		y := top + float64(r)*ch
		c := sgInk
		if r == len(s.Week)-1 {
			c = sgOrange
		}
		text(dc, d.Date.Format("Mon")[:2], sgSemi, 9, c, 14, y+ch/2-1, 0, 0.5)
		for h, b := range d.Hours {
			x := left + float64(h)*cw
			if b.Tokens == 0 {
				rect(dc, x+3, y+ch/2-1.5, 2, 2, sgTrack)
				continue
			}
			f := math.Pow(float64(b.Tokens)/float64(peak), 0.8)
			sz := 2 + (cw-2)*f // area encodes volume
			cell := sgCobalt
			if b.Tokens == peak {
				cell = sgOrange
			}
			rect(dc, x+(cw-sz)/2, y+(ch-2-sz)/2, sz, sz, cell)
		}
	}
	for _, h := range []int{0, 6, 12, 18} {
		text(dc, pad2(h), sgReg, 9, sgInk, left+float64(h)*cw, 222, 0, 0)
	}
}

func (g signal) clock(dc *gg.Context, s *usage.Snapshot) {
	now := s.Now.Local()
	text(dc, now.Format("Monday"), sgSemi, 10, sgInk, 14, 22, 0, 0)
	text(dc, now.Format("2 Jan"), sgSemi, 10, sgInk, 226, 22, 1, 0)
	text(dc, now.Format("15:04"), sgNum, fitSize(dc, "00:00", sgNum, 128, 214), sgInk, 10, 132, 0, 0)

	if s.Active {
		rect(dc, 14, 144, 6, 6, sgOrange)
		text(dc, "Working · "+s.Project, sgSemi, 10, sgInk, 25, 151, 0, 0)
	}
	bar := func(y float64, label string, w usage.Window, c color.Color) {
		text(dc, label, sgSemi, 10, sgInk, 14, y-6, 0, 0)
		v := "—"
		if w.Known {
			v = pct(w.Util) + "%"
		}
		text(dc, v, sgSemi, 10, sgInk, 226, y-6, 1, 0)
		rect(dc, 14, y, 212, 9, sgTrack)
		if w.Known {
			rect(dc, 14, y, 212*math.Min(w.Util, 100)/100, 9, c)
		}
	}
	bar(184, "Session", s.Limits.Session, sgCobalt)
	bar(216, "Week", s.Limits.Week, sgOrange)
}
