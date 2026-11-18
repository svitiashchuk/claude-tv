// Package app runs the data refresh loop, the screen rotation on the device, and the preview page.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/jpeg"
	"log"
	"sync"
	"time"

	"github.com/svitiashchuk/claude-tv/internal/device"
	"github.com/svitiashchuk/claude-tv/internal/render"
	"github.com/svitiashchuk/claude-tv/internal/usage"
)

type Config struct {
	Theme        string
	Screens      []string
	Rotate       time.Duration // how long each screen stays up
	MinUpload    time.Duration // never re-upload the same screen more often than this
	ScanEvery    time.Duration
	LimitsEvery  time.Duration
	Demo         bool
	AlbumTheme   int
	Brightness   int
	JPEGQuality  int
	Device       *device.GeekMagic // nil: preview only
	Store        *usage.Store
	Scanner      *usage.Scanner
	LimitsClient *usage.LimitsClient // nil: local history only
}

type App struct {
	cfg Config

	mu       sync.RWMutex
	snap     *usage.Snapshot
	limits   usage.Limits
	limErr   error
	events   []usage.Event
	theme    render.Theme
	current  string // screen currently on the device
	devState string // human-readable device status for the preview page
	uploaded map[string]upload
}

type upload struct {
	at   time.Time
	hash [32]byte
}

func New(cfg Config) *App {
	return &App{cfg: cfg, theme: render.ThemeByName(cfg.Theme), uploaded: map[string]upload{}, devState: "preview only (no -device)"}
}

func (a *App) Snapshot() *usage.Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snap
}

func (a *App) Theme() render.Theme {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.theme
}

func (a *App) SetTheme(name string) bool {
	t := render.ThemeByName(name)
	if t == nil {
		return false
	}
	a.mu.Lock()
	a.theme = t
	a.uploaded = map[string]upload{} // force fresh uploads in the new look
	a.mu.Unlock()
	return true
}

func (a *App) Status() (current, dev string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.current, a.devState
}

func (a *App) setDevState(s string) {
	a.mu.Lock()
	changed := a.devState != s
	a.devState = s
	a.mu.Unlock()
	if changed {
		log.Printf("device: %s", s)
	}
}

// Run blocks until ctx is cancelled.
func (a *App) Run(ctx context.Context) {
	a.refresh(ctx, true)
	go a.dataLoop(ctx)
	if a.cfg.Device != nil {
		a.displayLoop(ctx)
	} else {
		<-ctx.Done()
	}
}

func (a *App) dataLoop(ctx context.Context) {
	scan := time.NewTicker(a.cfg.ScanEvery)
	lim := time.NewTicker(a.cfg.LimitsEvery)
	tick := time.NewTicker(15 * time.Second) // keep clocks and countdowns fresh between scans
	defer scan.Stop()
	defer lim.Stop()
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-scan.C:
			a.refresh(ctx, false)
		case <-lim.C:
			a.fetchLimits(ctx)
			a.rebuild()
		case <-tick.C:
			a.rebuild()
		}
	}
}

func (a *App) refresh(ctx context.Context, withLimits bool) {
	if a.cfg.Demo {
		a.mu.Lock()
		a.snap = usage.Demo(time.Now())
		a.mu.Unlock()
		return
	}
	events, err := a.cfg.Scanner.Scan()
	if err != nil {
		log.Printf("scan: %v", err)
	}
	if a.cfg.Store.Merge(events) {
		if err := a.cfg.Store.Save(); err != nil {
			log.Printf("saving history: %v", err)
		}
	}
	a.mu.Lock()
	a.events = events
	a.mu.Unlock()
	if withLimits {
		a.fetchLimits(ctx)
	}
	a.rebuild()
}

func (a *App) fetchLimits(ctx context.Context) {
	if a.cfg.Demo || a.cfg.LimitsClient == nil {
		return
	}
	l, err := a.cfg.LimitsClient.Fetch(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		if a.limErr == nil || a.limErr.Error() != err.Error() {
			log.Printf("usage limits: %v", err)
		}
		a.limErr = err
		return
	}
	a.limits, a.limErr = l, nil
}

func (a *App) rebuild() {
	if a.cfg.Demo {
		a.mu.Lock()
		a.snap = usage.Demo(time.Now())
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	lim := a.limits
	// Stale limits are worse than none: drop them once their window has reset.
	if !lim.Session.ResetsAt.IsZero() && time.Now().After(lim.Session.ResetsAt) {
		lim.Session = usage.Window{Length: lim.Session.Length}
	}
	if !lim.Week.ResetsAt.IsZero() && time.Now().After(lim.Week.ResetsAt) {
		lim.Week = usage.Window{Length: lim.Week.Length}
	}
	a.snap = usage.Build(time.Now(), a.cfg.Store, a.events, lim, a.limErr)
}

func (a *App) displayLoop(ctx context.Context) {
	dev := a.cfg.Device
	prepared := false
	i := 0
	next := time.NewTimer(0)
	defer next.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-next.C:
		}
		if !prepared {
			info, err := dev.Info(ctx)
			if err == nil {
				err = dev.Prepare(ctx, a.cfg.AlbumTheme, a.cfg.Brightness)
			}
			if err != nil {
				a.setDevState("unreachable: " + err.Error())
				next.Reset(30 * time.Second)
				continue
			}
			log.Printf("device %s: %s", dev.Base, info)
			prepared = true
		}
		screen := a.cfg.Screens[i%len(a.cfg.Screens)]
		i++
		if err := a.show(ctx, screen); err != nil {
			a.setDevState("error: " + err.Error())
			prepared = false // re-check and re-prepare (the device may have rebooted or switched mode)
			next.Reset(30 * time.Second)
			continue
		}
		a.setDevState("ok")
		next.Reset(a.cfg.Rotate)
	}
}

// show uploads the screen if its content changed (at most once per MinUpload), then displays it.
func (a *App) show(ctx context.Context, screen string) error {
	img := a.Theme().Render(screen, a.Snapshot())
	jpg, err := EncodeJPEG(img, a.cfg.JPEGQuality)
	if err != nil {
		return err
	}
	name := "ctv-" + screen + ".jpg"
	h := sha256.Sum256(jpg)
	a.mu.RLock()
	prev, ok := a.uploaded[screen]
	a.mu.RUnlock()
	if !ok || (prev.hash != h && time.Since(prev.at) >= a.cfg.MinUpload) {
		if err := a.cfg.Device.Upload(ctx, name, jpg); err != nil {
			return err
		}
		a.mu.Lock()
		a.uploaded[screen] = upload{time.Now(), h}
		a.mu.Unlock()
	}
	if err := a.cfg.Device.Show(ctx, name); err != nil {
		return err
	}
	a.mu.Lock()
	a.current = screen
	a.mu.Unlock()
	return nil
}

// EncodeJPEG produces the baseline JPEG the firmware's decoder expects.
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes(), err
}
