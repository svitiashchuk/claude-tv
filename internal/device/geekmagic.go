// Package device talks to a GeekMagic SmallTV running its stock firmware, over plain HTTP on the LAN.
//
// Endpoints (stock firmware, documented by the geekmagic-stats project, tested there on Ultra-V9.0.43):
//
//	GET  /v.json                         model and firmware version
//	GET  /set?theme=3                    switch to Photo Album (3 on Ultra, 4 on Pro)
//	GET  /set?autoplay=0                 stop the album's own slideshow
//	GET  /set?brt=60                     brightness 0–100
//	POST /doUpload?dir=/image/           multipart field "file": store a 240×240 JPEG
//	GET  /set?img=/image//name.jpg       show a stored image
//	GET  /delete?file=/image//name.jpg   remove a stored image
package device

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type GeekMagic struct {
	Base string // e.g. http://192.168.1.50
	HTTP *http.Client
}

func NewGeekMagic(host string) *GeekMagic {
	if !strings.HasPrefix(host, "http") {
		host = "http://" + host
	}
	return &GeekMagic{Base: strings.TrimRight(host, "/"), HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (g *GeekMagic) get(ctx context.Context, path string, q url.Values) (string, error) {
	u := g.Base + path
	if q != nil {
		// The firmware wants the literal "//" in image paths, so don't let Encode escape slashes.
		u += "?" + strings.ReplaceAll(q.Encode(), "%2F", "/")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return string(b), fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return string(b), nil
}

// Info returns the device's /v.json, which also proves it's reachable.
func (g *GeekMagic) Info(ctx context.Context) (string, error) {
	return g.get(ctx, "/v.json", nil)
}

// Prepare switches to the photo album, stops its slideshow, and optionally sets brightness (<0 leaves it).
func (g *GeekMagic) Prepare(ctx context.Context, albumTheme, brightness int) error {
	if _, err := g.get(ctx, "/set", url.Values{"theme": {fmt.Sprint(albumTheme)}}); err != nil {
		return err
	}
	if _, err := g.get(ctx, "/set", url.Values{"autoplay": {"0"}}); err != nil {
		return err
	}
	if brightness >= 0 {
		if _, err := g.get(ctx, "/set", url.Values{"brt": {fmt.Sprint(min(brightness, 100))}}); err != nil {
			return err
		}
	}
	return nil
}

// Upload stores a JPEG under /image/<name>, replacing any previous file of that name.
func (g *GeekMagic) Upload(ctx context.Context, name string, jpeg []byte) error {
	g.get(ctx, "/delete", url.Values{"file": {"/image//" + name}}) // fine if it didn't exist

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	part.Write(jpeg)
	mw.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, g.Base+"/doUpload?dir=/image/", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	// The firmware's HTTP server sometimes answers 400 "Duplicate Content-Length" / "Data after"
	// even though the file was stored.
	if resp.StatusCode >= 300 && !(resp.StatusCode == 400 && (bytes.Contains(b, []byte("Duplicate")) || bytes.Contains(b, []byte("Data after")))) {
		return fmt.Errorf("upload %s: HTTP %d %s", name, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// Show displays a previously uploaded image.
func (g *GeekMagic) Show(ctx context.Context, name string) error {
	_, err := g.get(ctx, "/set", url.Values{"img": {"/image//" + name}})
	return err
}
