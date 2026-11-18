// claude-tv draws your Claude Code usage and pushes it to a GeekMagic SmallTV desk clock.
package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/svitiashchuk/claude-tv/internal/app"
	"github.com/svitiashchuk/claude-tv/internal/device"
	"github.com/svitiashchuk/claude-tv/internal/render"
	"github.com/svitiashchuk/claude-tv/internal/usage"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "render" {
		renderCmd(os.Args[2:])
		return
	}
	runCmd(os.Args[1:])
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("claude-tv", flag.ExitOnError)
	dev := fs.String("device", os.Getenv("CLAUDE_TV_DEVICE"), "clock's IP address or hostname; empty = preview page only")
	theme := fs.String("theme", "aurora", "look: "+strings.Join(render.ThemeNames(), ", "))
	screens := fs.String("screens", strings.Join(render.Screens, ","), "screens to rotate through, in order")
	rotate := fs.Duration("rotate", 20*time.Second, "how long each screen stays on the clock")
	minUpload := fs.Duration("min-upload", 60*time.Second, "re-send a changed screen at most this often (saves the clock's flash)")
	limitsEvery := fs.Duration("limits-every", 2*time.Minute, "how often to ask Anthropic for your plan limits")
	preview := fs.String("preview", "127.0.0.1:8787", "preview page address; empty to disable")
	brightness := fs.Int("brightness", -1, "set the clock's brightness 0–100 on start; -1 leaves it")
	albumTheme := fs.Int("album-theme", 3, "the firmware's Photo Album theme number (3 on SmallTV Ultra, 4 on Pro)")
	quality := fs.Int("jpeg-quality", 90, "JPEG quality for uploads")
	storePath := fs.String("history", usage.DefaultStorePath(), "where hourly history is kept")
	demo := fs.Bool("demo", false, "show sample data instead of your real usage")
	limits := fs.Bool("limits", true, "fetch plan limits from Anthropic using Claude Code's login (false: local history only)")
	fs.Parse(args)

	if render.ThemeByName(*theme) == nil {
		log.Fatalf("unknown theme %q (have %s)", *theme, strings.Join(render.ThemeNames(), ", "))
	}
	list := strings.Split(*screens, ",")
	for _, s := range list {
		if !contains(render.Screens, s) {
			log.Fatalf("unknown screen %q (have %s)", s, strings.Join(render.Screens, ", "))
		}
	}
	store, err := usage.OpenStore(*storePath)
	if err != nil {
		log.Fatalf("history: %v", err)
	}
	cfg := app.Config{
		Theme: *theme, Screens: list, Rotate: *rotate, MinUpload: *minUpload,
		ScanEvery: 30 * time.Second, LimitsEvery: *limitsEvery, Demo: *demo,
		AlbumTheme: *albumTheme, Brightness: *brightness, JPEGQuality: *quality,
		Store: store, Scanner: usage.NewScanner(usage.DefaultRoot()),
	}
	if *limits {
		cfg.LimitsClient = usage.NewLimitsClient()
	}
	if *dev != "" {
		cfg.Device = device.NewGeekMagic(*dev)
	}
	a := app.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *preview != "" {
		srv := &http.Server{Addr: *preview, Handler: a.Handler()}
		go func() {
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				log.Printf("preview: %v", err)
			}
		}()
		go func() { <-ctx.Done(); srv.Close() }()
		log.Printf("preview at http://%s", *preview)
	}
	if cfg.Device == nil {
		log.Printf("no -device given: preview only")
	}
	a.Run(ctx)
}

// renderCmd writes every screen as PNG, for design checks without a device.
func renderCmd(args []string) {
	fs := flag.NewFlagSet("render", flag.ExitOnError)
	demo := fs.Bool("demo", false, "use sample data instead of your real usage")
	theme := fs.String("theme", "", "themes to render, comma-separated (default: all)")
	out := fs.String("out", "screens", "output directory")
	at := fs.String("at", "", "with -demo: pretend it's this time of day, e.g. 15:20")
	limits := fs.Bool("limits", true, "fetch plan limits from Anthropic (false: local history only)")
	fs.Parse(args)

	now := time.Now()
	if t, err := time.ParseInLocation("15:04", *at, time.Local); err == nil {
		now = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
	}
	var snap *usage.Snapshot
	if *demo {
		snap = usage.Demo(now)
	} else {
		store, err := usage.OpenStore(usage.DefaultStorePath())
		if err != nil {
			log.Fatalf("history: %v", err)
		}
		events, _ := usage.NewScanner(usage.DefaultRoot()).Scan()
		store.Merge(events) // in memory only; the running server owns the history file
		var lim usage.Limits
		var limErr error
		if *limits {
			if lim, limErr = usage.NewLimitsClient().Fetch(context.Background()); limErr != nil {
				log.Printf("usage limits: %v", limErr)
			}
		}
		snap = usage.Build(now, store, events, lim, limErr)
	}
	names := render.ThemeNames()
	if *theme != "" {
		names = strings.Split(*theme, ",")
	}
	os.MkdirAll(*out, 0o755)
	for _, tn := range names {
		t := render.ThemeByName(tn)
		if t == nil {
			log.Fatalf("unknown theme %q (have %s)", tn, strings.Join(render.ThemeNames(), ", "))
		}
		for _, sc := range render.Screens {
			p := filepath.Join(*out, tn+"-"+sc+".png")
			f, err := os.Create(p)
			if err != nil {
				log.Fatal(err)
			}
			png.Encode(f, t.Render(sc, snap))
			f.Close()
			fmt.Println(p)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
