// Package render draws 240×240 screens for the GeekMagic SmallTV.
package render

import (
	"embed"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"sync"
	"time"

	"claude-tv/internal/usage"

	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
)

const Size = 240

// Screens in their default rotation order.
var Screens = []string{"now", "today", "pace", "week", "clock"}

// Theme draws every screen in one visual style.
type Theme interface {
	Name() string
	Render(screen string, s *usage.Snapshot) image.Image
}

var themes = map[string]Theme{}

func register(t Theme) { themes[t.Name()] = t }

// ThemeByName returns nil for an unknown theme.
func ThemeByName(name string) Theme { return themes[name] }

func ThemeNames() []string {
	var out []string
	for k := range themes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- fonts ----

//go:embed fonts/*.ttf
var fontFS embed.FS

var (
	fontMu    sync.Mutex
	fontCache = map[string]*truetype.Font{}
	faceCache = map[string]font.Face{}
)

func face(name string, size float64) font.Face {
	fontMu.Lock()
	defer fontMu.Unlock()
	key := fmt.Sprintf("%s@%.1f", name, size)
	if f, ok := faceCache[key]; ok {
		return f
	}
	tt, ok := fontCache[name]
	if !ok {
		b, err := fontFS.ReadFile("fonts/" + name + ".ttf")
		if err != nil {
			panic(err)
		}
		if tt, err = truetype.Parse(b); err != nil {
			panic(err)
		}
		fontCache[name] = tt
	}
	f := truetype.NewFace(tt, &truetype.Options{Size: size, DPI: 72, Hinting: font.HintingNone})
	faceCache[key] = f
	return f
}

// ---- colour ----

func hex(s string) color.NRGBA {
	var r, g, b uint8
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return color.NRGBA{r, g, b, 255}
}

func alpha(c color.NRGBA, a float64) color.NRGBA { c.A = uint8(a * 255); return c }

func lerp(a, b color.NRGBA, t float64) color.NRGBA {
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return color.NRGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), f(a.A, b.A)}
}

// gradientAt samples a multi-stop gradient at t in [0,1].
func gradientAt(stops []color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	if len(stops) == 1 {
		return stops[0]
	}
	p := t * float64(len(stops)-1)
	i := int(p)
	if i >= len(stops)-1 {
		return stops[len(stops)-1]
	}
	return lerp(stops[i], stops[i+1], p-float64(i))
}

// ---- drawing ----

// arcGradient strokes an arc from the 12 o'clock position clockwise, colour following the arc.
func arcGradient(dc *gg.Context, cx, cy, r, width, frac float64, stops []color.NRGBA) {
	if frac <= 0 {
		return
	}
	frac = math.Min(frac, 1)
	start := -math.Pi / 2
	// Each slice is a filled annular quad; slightly overlapped so no seams show.
	n := int(math.Max(8, 180*frac))
	step := 2 * math.Pi * frac / float64(n)
	ri, ro := r-width/2, r+width/2
	for i := 0; i < n; i++ {
		a0 := start + float64(i)*step
		a1 := a0 + step*1.3
		if i == n-1 {
			a1 = a0 + step
		}
		dc.SetColor(gradientAt(stops, float64(i)/float64(n)))
		dc.NewSubPath()
		dc.MoveTo(cx+ri*math.Cos(a0), cy+ri*math.Sin(a0))
		dc.LineTo(cx+ro*math.Cos(a0), cy+ro*math.Sin(a0))
		dc.LineTo(cx+ro*math.Cos(a1), cy+ro*math.Sin(a1))
		dc.LineTo(cx+ri*math.Cos(a1), cy+ri*math.Sin(a1))
		dc.ClosePath()
		dc.Fill()
	}
	// round caps
	dc.SetColor(stops[0])
	dc.DrawCircle(cx+r*math.Cos(start), cy+r*math.Sin(start), width/2)
	dc.Fill()
	end := start + 2*math.Pi*frac
	dc.SetColor(gradientAt(stops, 1))
	dc.DrawCircle(cx+r*math.Cos(end), cy+r*math.Sin(end), width/2)
	dc.Fill()
}

// glow redraws a layer blurred underneath itself, so bright strokes bloom like light.
func glow(dc *gg.Context, radius int, strength float64, draw func(*gg.Context)) {
	layer := gg.NewContext(Size, Size)
	draw(layer)
	blurred := boxBlur(layer.Image().(*image.RGBA), radius, 3)
	dc.Push()
	for i := 0; i < int(math.Ceil(strength)); i++ {
		dc.DrawImage(blurred, 0, 0)
	}
	dc.Pop()
	dc.DrawImage(layer.Image(), 0, 0)
}

// boxBlur approximates a gaussian blur with repeated box passes (premultiplied RGBA).
func boxBlur(src *image.RGBA, r, passes int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	a := image.NewRGBA(src.Bounds())
	copy(a.Pix, src.Pix)
	b := image.NewRGBA(src.Bounds())
	pass := func(in, out *image.RGBA, horiz bool) {
		n, m := w, h
		if !horiz {
			n, m = h, w
		}
		for j := 0; j < m; j++ {
			var sum [4]int
			idx := func(i int) int {
				i = max(0, min(n-1, i))
				if horiz {
					return j*in.Stride + i*4
				}
				return i*in.Stride + j*4
			}
			for i := -r; i <= r; i++ {
				o := idx(i)
				for c := 0; c < 4; c++ {
					sum[c] += int(in.Pix[o+c])
				}
			}
			for i := 0; i < n; i++ {
				o := idx(i)
				if horiz {
					o = j*out.Stride + i*4
				} else {
					o = i*out.Stride + j*4
				}
				for c := 0; c < 4; c++ {
					out.Pix[o+c] = uint8(sum[c] / (2*r + 1))
				}
				add, sub := idx(i+r+1), idx(i-r)
				for c := 0; c < 4; c++ {
					sum[c] += int(in.Pix[add+c]) - int(in.Pix[sub+c])
				}
			}
		}
	}
	for p := 0; p < passes; p++ {
		pass(a, b, true)
		pass(b, a, false)
	}
	return a
}

// blob paints a soft radial light, the building block of the aurora background.
func blob(dc *gg.Context, x, y, r float64, c color.NRGBA, a float64) {
	g := gg.NewRadialGradient(x, y, 0, x, y, r)
	g.AddColorStop(0, alpha(c, a))
	g.AddColorStop(0.5, alpha(c, a*0.45))
	g.AddColorStop(1, alpha(c, 0))
	dc.SetFillStyle(g)
	dc.DrawRectangle(0, 0, Size, Size)
	dc.Fill()
}

func text(dc *gg.Context, s string, fnt string, size float64, c color.Color, x, y, ax, ay float64) {
	dc.SetFontFace(face(fnt, size))
	dc.SetColor(c)
	dc.DrawStringAnchored(s, x, y, ax, ay)
}

func measure(dc *gg.Context, s, fnt string, size float64) float64 {
	dc.SetFontFace(face(fnt, size))
	w, _ := dc.MeasureString(s)
	return w
}

// fitSize shrinks a font size until s fits in width.
func fitSize(dc *gg.Context, s, fnt string, size, width float64) float64 {
	for size > 8 && measure(dc, s, fnt, size) > width {
		size -= 1
	}
	return size
}

// projection extends the window's average pace from its last point, stopping at the
// limit (100%) or the reset (x = 1), whichever comes first.
func projection(last usage.Point) (x, y float64) {
	rate := last.Y / math.Max(last.X, 1e-6)
	x, y = 1, last.Y+rate*(1-last.X)
	if y > 100 {
		x, y = 100/rate, 100
	}
	return
}

// ---- formatting ----

func fmtTokens(n int64) string {
	switch {
	case n >= 1e9:
		return trim(fmt.Sprintf("%.2f", float64(n)/1e9)) + "B"
	case n >= 1e6:
		return trim(fmt.Sprintf("%.2f", float64(n)/1e6)) + "M"
	case n >= 1e3:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func trim(s string) string {
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

func fmtMoney(v float64) string {
	if v >= 1000 {
		return fmt.Sprintf("$%.1fk", v/1000)
	}
	if v >= 100 {
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h >= 24 {
		return fmt.Sprintf("%dd %dh", h/24, h%24)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func pct(v float64) string { return fmt.Sprintf("%.0f", math.Round(v)) }
