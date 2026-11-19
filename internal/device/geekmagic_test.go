package device

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeClock answers like one firmware family, recording calls and stored uploads.
type fakeClock struct {
	fw    Firmware
	mu    sync.Mutex
	calls []string
	files map[string][]byte // "field:filename" → bytes
}

func (f *fakeClock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	ok := func(s string) { io.WriteString(w, s) }
	p := r.URL.Path
	upload := func(field string) {
		file, hdr, err := r.FormFile(field)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		b, _ := io.ReadAll(file)
		f.files[field+":"+hdr.Filename] = b
		ok("Upload successful!")
	}
	switch {
	case p == "/":
		if f.fw == Legacy {
			ok(`<html><select id="giflist"></select><form action='/connect'></form></html>`)
		} else {
			ok("<html>GeekMagic</html>")
		}
	case f.fw == Ultra && p == "/v.json":
		ok(`{"m":"SmallTV-Ultra","v":"Ultra-V9.0.43"}`)
	case f.fw == Pro && p == "/.sys/app.json": // newer Pro firmware: no /v.json
		ok(`{"theme":4}`)
	case f.fw == SDPro && p == "/theme/list":
		ok(`{"themes":[{"id":0,"name":"Classic","enabled":true},{"id":2,"name":"Photo","enabled":false}]}`)
	case f.fw == SDPro && p == "/photo/list":
		ok(`{"files":[{"name":"holiday.jpg","enabled":true},{"name":"claude-tv.jpg","enabled":false}],"total":1000,"used":10}`)
	case (f.fw == Ultra || f.fw == Pro) && p == "/doUpload":
		upload("file")
	case f.fw == SDPro && p == "/photo/upload":
		upload("file")
	case f.fw == Legacy && p == "/upload":
		upload("imageFile")
	case (f.fw == Ultra || f.fw == Pro) && (p == "/set" || p == "/delete"),
		f.fw == SDPro && (strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/photo/")),
		f.fw == Legacy && (strings.HasSuffix(p, "-switch") || p == "/themeselect" || p == "/updateLEDBrightness"):
		ok("OK")
	default:
		http.Error(w, "Not found: "+p, 404)
	}
}

func (f *fakeClock) joined() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
}

func TestEachFirmware(t *testing.T) {
	cases := []struct {
		fw       Firmware
		named    bool
		wantFile string
		want     []string
	}{
		{Ultra, true, "file:ctv-now.jpg", []string{"GET /set?theme=3", "GET /set?brt=40", "POST /doUpload?dir=/image/", "GET /set?img=/image/ctv-now.jpg"}},
		{Pro, false, "file:claude-tv.jpg", []string{"GET /set?theme=4", "POST /doUpload?dir=/image/"}},
		{SDPro, false, "file:claude-tv.jpg", []string{"GET /photo/toggle?name=holiday.jpg&state=0", "GET /photo/toggle?name=claude-tv.jpg&state=1",
			"GET /api/set?key=theme&value=2", "GET /api/set?key=lcd_brightness&value=40", "POST /photo/upload"}},
		{Legacy, false, "imageFile:file1.jpg", []string{"GET /file1-switch?getstate=1", "GET /file3-switch?getstate=0",
			"GET /themeselect?getstate=2", "GET /updateLEDBrightness?ledBrightness=40", "POST /upload"}},
	}
	for _, c := range cases {
		t.Run(string(c.fw), func(t *testing.T) {
			fc := &fakeClock{fw: c.fw, files: map[string][]byte{}}
			srv := httptest.NewServer(fc)
			defer srv.Close()
			g := NewGeekMagic(strings.TrimPrefix(srv.URL, "http://"))
			ctx := context.Background()

			if _, err := g.Detect(ctx); err != nil {
				t.Fatal(err)
			}
			if g.Firmware != c.fw || g.Named() != c.named {
				t.Fatalf("detected %s (named %v)", g.Firmware, g.Named())
			}
			if err := g.Prepare(ctx, 40); err != nil {
				t.Fatal(err)
			}
			if err := g.Upload(ctx, "ctv-now.jpg", []byte("jpegdata")); err != nil {
				t.Fatal(err)
			}
			if err := g.Show(ctx, "ctv-now.jpg"); err != nil {
				t.Fatal(err)
			}
			if string(fc.files[c.wantFile]) != "jpegdata" {
				t.Fatalf("stored files %v, want %s", keys(fc.files), c.wantFile)
			}
			for _, w := range c.want {
				if !strings.Contains(fc.joined(), w) {
					t.Errorf("missing %q in:\n%s", w, fc.joined())
				}
			}
		})
	}
}

func TestUnknownFirmwareExplainsWhatToDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			io.WriteString(w, "<html>something else</html>")
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := NewGeekMagic(srv.URL).Detect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "claude-tv probe") {
		t.Fatalf("want a hint to run probe, got %v", err)
	}
}

// The firmware's HTTP server sends replies net/http refuses (duplicate Content-Length).
func TestToleratesMalformedReplies(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				line, _ := r.ReadString('\n')
				for { // drain headers
					h, err := r.ReadString('\n')
					if err != nil || h == "\r\n" {
						break
					}
				}
				body := `{"m":"SmallTV-Ultra","v":"Ultra-V9.0.40"}`
				if !strings.Contains(line, "/v.json") {
					body = "OK"
				}
				fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%sjunk-after-close", len(body), len(body), body)
			}(c)
		}
	}()
	g := NewGeekMagic(ln.Addr().String())
	if _, err := g.Detect(context.Background()); err != nil || g.Firmware != Ultra {
		t.Fatalf("detect over malformed replies: %v (firmware %q)", err, g.Firmware)
	}
	if err := g.Show(context.Background(), "x.jpg"); err != nil {
		t.Fatal(err)
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
