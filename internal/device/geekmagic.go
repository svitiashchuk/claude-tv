// Package device talks to GeekMagic SmallTV clocks over plain HTTP on the LAN, without
// replacing their firmware. GeekMagic ships several incompatible firmwares; the endpoints below
// follow the device reports collected by the geekmagic-hacs project
// (github.com/adrienbrault/geekmagic-hacs, docs/devices and profiles.py).
//
//	ultra   stock SmallTV Ultra   GET /v.json or /app.json   POST /doUpload?dir=/image/ (file)   GET /set?img=/image/<name>
//	pro     stock SmallTV Pro     GET /.sys/app.json         POST /doUpload?dir=/image/ (file)   album slideshow, theme 4
//	sdpro   SD_PRO-style firmware GET /theme/list            POST /photo/upload (file)           photo slideshow, theme 2
//	legacy  "Smart Weather Clock" root page fingerprint      POST /upload (imageFile) → file1    photo mode, slot 1
//
// Only "ultra" can switch between stored images by name. The others show one picture slot,
// so every screen change is a fresh upload.
package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"strings"
)

type Firmware string

const (
	Ultra  Firmware = "ultra"
	Pro    Firmware = "pro"
	SDPro  Firmware = "sdpro"
	Legacy Firmware = "legacy"
)

var Firmwares = []Firmware{Ultra, Pro, SDPro, Legacy}

// SlotName is the file used on firmwares with a single picture slot.
const SlotName = "claude-tv.jpg"

type GeekMagic struct {
	Base     string // e.g. http://192.168.1.50
	Firmware Firmware
	Model    string // what the device reported, for logs

	base *url.URL
}

func NewGeekMagic(host string) *GeekMagic {
	if !strings.HasPrefix(host, "http") {
		host = "http://" + host
	}
	host = strings.TrimRight(host, "/")
	u, _ := url.Parse(host)
	return &GeekMagic{Base: host, base: u}
}

// Named reports whether the firmware can display a stored image by name.
func (g *GeekMagic) Named() bool { return g.Firmware == Ultra }

func (g *GeekMagic) get(ctx context.Context, path string) (int, []byte, error) {
	return rawDo(ctx, g.base, "GET", path, "", nil)
}

// getOK is a GET that must succeed (2xx and not a firmware "FAIL").
func (g *GeekMagic) getOK(ctx context.Context, path string) error {
	status, body, err := g.get(ctx, path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	if status >= 300 {
		return fmt.Errorf("GET %s: HTTP %d", path, status)
	}
	if strings.TrimSpace(string(body)) == "FAIL" {
		return fmt.Errorf("GET %s: device answered FAIL", path)
	}
	return nil
}

func (g *GeekMagic) getJSON(ctx context.Context, path string, v any) bool {
	status, body, err := g.get(ctx, path)
	// Decode only the first JSON value: some firmwares append bytes after the body.
	return err == nil && status == 200 && json.NewDecoder(bytes.NewReader(body)).Decode(v) == nil
}

// Detect works out which firmware the clock runs, unless Firmware is already set.
// It returns a one-line description for logs.
func (g *GeekMagic) Detect(ctx context.Context) (string, error) {
	if g.Firmware != "" {
		if _, _, err := g.get(ctx, "/"); err != nil {
			return "", fmt.Errorf("clock not reachable at %s: %w", g.Base, err)
		}
		return fmt.Sprintf("firmware %s (set by -firmware)", g.Firmware), nil
	}
	if _, _, err := g.get(ctx, "/"); err != nil {
		return "", fmt.Errorf("clock not reachable at %s: %w", g.Base, err)
	}
	var v struct{ M, V string }
	if g.getJSON(ctx, "/v.json", &v) {
		g.Model = strings.TrimSpace(v.M + " " + v.V)
		switch m := strings.ToLower(v.M); {
		case strings.Contains(m, "pro"):
			g.Firmware = Pro
		case strings.Contains(m, "ultra"):
			g.Firmware = Ultra
		}
	}
	var state map[string]any
	if g.Firmware == "" && g.getJSON(ctx, "/.sys/app.json", &state) {
		g.Firmware = Pro
	}
	if g.Firmware == "" && g.getJSON(ctx, "/app.json", &state) {
		g.Firmware = Ultra
	}
	var themes struct{ Themes []any }
	if g.Firmware == "" && g.getJSON(ctx, "/theme/list", &themes) && themes.Themes != nil {
		g.Firmware = SDPro
	}
	if g.Firmware == "" {
		if _, body, err := g.get(ctx, "/"); err == nil &&
			bytes.Contains(body, []byte(`id="giflist"`)) && bytes.Contains(body, []byte(`action='/connect'`)) {
			g.Firmware = Legacy
		}
	}
	if g.Firmware == "" {
		return "", fmt.Errorf("the clock at %s answers, but its firmware isn't one claude-tv knows "+
			"(tried /v.json, /.sys/app.json, /app.json, /theme/list and the home page). "+
			"Run `claude-tv probe -device %s` and open an issue with its output, or force one with -firmware %s",
			g.Base, g.base.Host, strings.Join(firmwareNames(), "|"))
	}
	desc := "firmware " + string(g.Firmware)
	if g.Model != "" {
		desc += " (" + g.Model + ")"
	}
	return desc, nil
}

func firmwareNames() []string {
	var s []string
	for _, f := range Firmwares {
		s = append(s, string(f))
	}
	return s
}

// Prepare switches the clock to its picture mode and optionally sets brightness (0–100, <0 leaves it).
func (g *GeekMagic) Prepare(ctx context.Context, brightness int) error {
	switch g.Firmware {
	case Ultra:
		if err := g.getOK(ctx, "/set?theme=3"); err != nil {
			return err
		}
		g.getOK(ctx, "/set?autoplay=0") // stop the album's own slideshow; older firmware ignores it
	case Pro:
		if err := g.getOK(ctx, "/set?theme=4"); err != nil {
			return err
		}
	case SDPro:
		if err := g.enableOnlySDProPhoto(ctx, SlotName); err != nil {
			return err
		}
		g.getOK(ctx, "/photo/interval?val=1")
		if err := g.getOK(ctx, "/api/set?key=theme&value=2"); err != nil {
			return err
		}
	case Legacy:
		if err := g.getOK(ctx, "/file1-switch?getstate=1"); err != nil {
			return err
		}
		for n := 2; n <= 5; n++ {
			g.getOK(ctx, fmt.Sprintf("/file%d-switch?getstate=0", n))
		}
		if err := g.getOK(ctx, "/themeselect?getstate=2"); err != nil {
			return err
		}
	}
	if brightness >= 0 {
		b := min(brightness, 100)
		var path string
		switch g.Firmware {
		case SDPro:
			path = fmt.Sprintf("/api/set?key=lcd_brightness&value=%d", max(2, min(b, 99)))
		case Legacy:
			path = fmt.Sprintf("/updateLEDBrightness?ledBrightness=%d", max(2, min(b, 99)))
		default:
			path = fmt.Sprintf("/set?brt=%d", b)
		}
		if err := g.getOK(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

// enableOnlySDProPhoto pins the slideshow to our photo (once it exists) without deleting anything.
func (g *GeekMagic) enableOnlySDProPhoto(ctx context.Context, name string) error {
	var list struct {
		Files []struct {
			Name    string
			Enabled bool
		}
	}
	if !g.getJSON(ctx, "/photo/list", &list) {
		return fmt.Errorf("GET /photo/list failed")
	}
	for _, f := range list.Files {
		if want := f.Name == name; want != f.Enabled {
			state := map[bool]string{true: "1", false: "0"}[want]
			g.getOK(ctx, "/photo/toggle?name="+url.QueryEscape(f.Name)+"&state="+state)
		}
	}
	return nil
}

// Upload stores a JPEG. On single-slot firmwares the name is ignored and the one slot is replaced.
func (g *GeekMagic) Upload(ctx context.Context, name string, jpeg []byte) error {
	var path, field string
	switch g.Firmware {
	case SDPro:
		path, field, name = "/photo/upload", "file", SlotName
	case Legacy:
		path, field, name = "/upload", "imageFile", "file1.jpg"
	case Pro:
		path, field, name = "/doUpload?dir=/image/", "file", SlotName
		g.get(ctx, "/delete?file=/image/"+name)
	default:
		path, field = "/doUpload?dir=/image/", "file"
		g.get(ctx, "/delete?file=/image/"+name) // fine if it didn't exist
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, name))
	h.Set("Content-Type", "image/jpeg")
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	part.Write(jpeg)
	mw.Close()

	status, resp, err := rawDo(ctx, g.base, "POST", path, mw.FormDataContentType(), body.Bytes())
	if err != nil {
		return fmt.Errorf("upload %s: %w", name, err)
	}
	// Some firmwares answer 400 "Duplicate Content-Length" / "Data after" although the file was stored.
	if status >= 300 && !(status == 400 && (bytes.Contains(resp, []byte("Duplicate")) || bytes.Contains(resp, []byte("Data after")))) {
		return fmt.Errorf("upload %s: HTTP %d %s", name, status, truncate(strings.TrimSpace(string(resp)), 80))
	}
	if g.Firmware == SDPro {
		return g.enableOnlySDProPhoto(ctx, SlotName) // a new upload may arrive disabled
	}
	return nil
}

// Show displays a stored image. Single-slot firmwares already show the latest upload.
func (g *GeekMagic) Show(ctx context.Context, name string) error {
	switch g.Firmware {
	case Ultra:
		return g.getOK(ctx, "/set?img=/image/"+name)
	case Pro:
		g.getOK(ctx, "/set?img=/image/"+SlotName) // some Pro versions answer FAIL; the upload already shows
	}
	return nil
}

// Probe lists what common endpoints answer, for bug reports about unknown firmware.
func (g *GeekMagic) Probe(ctx context.Context) []string {
	var out []string
	for _, p := range []string{"/", "/v.json", "/app.json", "/.sys/app.json", "/theme/list", "/config", "/photo/list", "/album.json", "/space.json", "/home?num=1"} {
		status, body, err := g.get(ctx, p)
		line := fmt.Sprintf("%-16s ", p)
		if err != nil {
			line += "error: " + err.Error()
		} else {
			line += fmt.Sprintf("HTTP %d  %s", status, truncate(strings.Join(strings.Fields(string(body)), " "), 100))
		}
		out = append(out, line)
	}
	return out
}
