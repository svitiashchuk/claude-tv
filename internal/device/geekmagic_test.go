package device

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeClock mimics the stock firmware's endpoints, including its bogus 400 after uploads.
type fakeClock struct {
	mu    sync.Mutex
	calls []string
	files map[string][]byte
	shown string
}

func (f *fakeClock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	switch r.URL.Path {
	case "/v.json":
		io.WriteString(w, `{"m":"SmallTV-Ultra","v":"Ultra-V9.0.43"}`)
	case "/delete":
		delete(f.files, r.URL.Query().Get("file"))
		io.WriteString(w, "OK")
	case "/doUpload":
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		b, _ := io.ReadAll(file)
		f.files[r.URL.Query().Get("dir")+"/"+hdr.Filename] = b
		http.Error(w, "Duplicate Content-Length", http.StatusBadRequest)
	case "/set":
		if img := r.URL.Query().Get("img"); img != "" {
			if _, ok := f.files[img]; !ok {
				http.Error(w, "no such file", 404)
				return
			}
			f.shown = img
		}
		io.WriteString(w, "OK")
	default:
		http.NotFound(w, r)
	}
}

func TestUploadAndShow(t *testing.T) {
	fc := &fakeClock{files: map[string][]byte{}}
	srv := httptest.NewServer(fc)
	defer srv.Close()
	g := NewGeekMagic(strings.TrimPrefix(srv.URL, "http://"))
	ctx := context.Background()

	if _, err := g.Info(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.Prepare(ctx, 3, 40); err != nil {
		t.Fatal(err)
	}
	if err := g.Upload(ctx, "ctv-now.jpg", []byte("jpegdata")); err != nil {
		t.Fatalf("a 400 'Duplicate Content-Length' after upload must count as success: %v", err)
	}
	if err := g.Show(ctx, "ctv-now.jpg"); err != nil {
		t.Fatal(err)
	}
	if fc.shown != "/image//ctv-now.jpg" {
		t.Fatalf("shown %q; the firmware needs the literal double slash", fc.shown)
	}
	want := []string{"/set?theme=3", "/set?autoplay=0", "/set?brt=40", "/delete?file=/image//ctv-now.jpg", "/doUpload?dir=/image/"}
	joined := strings.Join(fc.calls, "\n")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing call %s in:\n%s", w, joined)
		}
	}
}

func TestUploadRealError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "flash full", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if err := NewGeekMagic(srv.URL).Upload(context.Background(), "x.jpg", []byte("x")); err == nil {
		t.Fatal("a real failure must be reported")
	}
}
