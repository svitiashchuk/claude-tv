package app

import (
	_ "embed"
	"encoding/json"
	"html/template"
	"image/png"
	"net/http"
	"strings"

	"claude-tv/internal/render"
)

//go:embed preview.html
var previewHTML string

var previewTmpl = template.Must(template.New("p").Parse(previewHTML))

// Handler serves the preview page: every screen in every theme, live, plus a theme switch.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		previewTmpl.Execute(w, map[string]any{"Themes": render.ThemeNames(), "Screens": a.cfg.Screens})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		cur, dev := a.Status()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"theme": a.Theme().Name(), "current": cur, "device": dev})
	})
	mux.HandleFunc("GET /screen/{theme}/{screen}", func(w http.ResponseWriter, r *http.Request) {
		t := render.ThemeByName(r.PathValue("theme"))
		screen := strings.TrimSuffix(r.PathValue("screen"), ".png")
		snap := a.Snapshot()
		if t == nil || snap == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		png.Encode(w, t.Render(screen, snap))
	})
	mux.HandleFunc("POST /theme", func(w http.ResponseWriter, r *http.Request) {
		if !a.SetTheme(r.FormValue("name")) {
			http.Error(w, "unknown theme", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
