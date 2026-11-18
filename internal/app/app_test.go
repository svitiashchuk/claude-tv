package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/svitiashchuk/claude-tv/internal/device"
	"github.com/svitiashchuk/claude-tv/internal/usage"
)

func TestShowUploadsOnlyChangedScreens(t *testing.T) {
	var uploads, shows atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/doUpload":
			uploads.Add(1)
		case strings.Contains(r.URL.RawQuery, "img="):
			shows.Add(1)
		}
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 18, 15, 20, 0, 0, time.Local)
	a := New(Config{Theme: "aurora", MinUpload: time.Hour, JPEGQuality: 90, Device: device.NewGeekMagic(srv.URL)})
	a.snap = usage.Demo(now)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := a.show(ctx, "now"); err != nil {
			t.Fatal(err)
		}
	}
	if uploads.Load() != 1 || shows.Load() != 3 {
		t.Fatalf("unchanged screen: %d uploads, %d shows; want 1 upload, 3 shows", uploads.Load(), shows.Load())
	}

	// content changed, but inside MinUpload: keep showing the stored image
	a.snap = usage.Demo(now.Add(time.Minute))
	a.show(ctx, "now")
	if uploads.Load() != 1 {
		t.Fatalf("changed within min-upload: %d uploads, want 1", uploads.Load())
	}

	// a theme switch forces a fresh upload
	a.SetTheme("signal")
	a.show(ctx, "now")
	if uploads.Load() != 2 {
		t.Fatalf("after theme switch: %d uploads, want 2", uploads.Load())
	}
}

func TestJPEGIsSmall(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 20, 0, 0, time.Local)
	a := New(Config{Theme: "aurora"})
	for _, sc := range []string{"now", "today", "pace", "week", "clock"} {
		jpg, err := EncodeJPEG(a.Theme().Render(sc, usage.Demo(now)), 90)
		if err != nil {
			t.Fatal(err)
		}
		if len(jpg) > 60<<10 {
			t.Errorf("%s: %d bytes; the clock holds ~1 MB of images in total", sc, len(jpg))
		}
	}
}
