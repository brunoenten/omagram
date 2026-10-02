package ui

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"gioui.org/op/paint"
	"golang.org/x/image/draw"

	"github.com/brunoenten/omagram/internal/tele"
)

const (
	maxDecodedSide = 720
	maxDecoded     = 48
	maxDownloads   = 4
)

type imgState int

const (
	imgLoading imgState = iota
	imgReady
	imgFailed
)

type imgEntry struct {
	state imgState
	op    paint.ImageOp
	used  time.Time
}

// images downloads, decodes and caches inline photos.
type images struct {
	mu         sync.Mutex
	m          map[string]*imgEntry
	sem        chan struct{}
	dir        string
	invalidate func()
}

func newImages(dir string, invalidate func()) *images {
	return &images{
		m:          map[string]*imgEntry{},
		sem:        make(chan struct{}, maxDownloads),
		dir:        dir,
		invalidate: invalidate,
	}
}

func (im *images) path(p *tele.Photo, size string) string {
	return filepath.Join(im.dir, fmt.Sprintf("%d_%s.jpg", p.ID, size))
}

// fetch returns the cached file for a photo size, downloading it if needed.
func (im *images) fetch(c *tele.Client, p *tele.Photo, size string) (string, error) {
	path := im.path(p, size)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(im.dir, 0o700); err != nil {
		return "", err
	}
	im.sem <- struct{}{}
	defer func() { <-im.sem }()
	return path, c.DownloadPhoto(*p, size, path)
}

// get returns a snapshot of the inline image for p, starting a load on first use.
func (im *images) get(c *tele.Client, p *tele.Photo) imgEntry {
	key := im.path(p, p.Thumb)
	im.mu.Lock()
	defer im.mu.Unlock()
	if e, ok := im.m[key]; ok {
		e.used = time.Now()
		return *e
	}
	e := &imgEntry{state: imgLoading, used: time.Now()}
	im.m[key] = e
	im.evictLocked()
	go func() {
		img, err := im.load(c, p)
		im.mu.Lock()
		if err != nil {
			e.state = imgFailed
		} else {
			e.op, e.state = paint.NewImageOp(img), imgReady
		}
		im.mu.Unlock()
		im.invalidate()
	}()
	return *e
}

func (im *images) load(c *tele.Client, p *tele.Photo) (image.Image, error) {
	path, err := im.fetch(c, p, p.Thumb)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	long := max(b.Dx(), b.Dy())
	if long <= maxDecodedSide {
		return src, nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx()*maxDecodedSide/long, b.Dy()*maxDecodedSide/long))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst, nil
}

func (im *images) evictLocked() {
	for len(im.m) > maxDecoded {
		var oldest string
		var at time.Time
		for k, e := range im.m {
			if e.state != imgLoading && (oldest == "" || e.used.Before(at)) {
				oldest, at = k, e.used
			}
		}
		if oldest == "" {
			return
		}
		delete(im.m, oldest)
	}
}

// open downloads the full-size photo and hands it to the default viewer.
func (im *images) open(c *tele.Client, p *tele.Photo, status func(string)) {
	status("opening photo…")
	go func() {
		path, err := im.fetch(c, p, p.Full)
		if err != nil {
			status("photo: " + err.Error())
			return
		}
		cmd := exec.Command("xdg-open", path)
		if err := cmd.Start(); err != nil {
			status("xdg-open: " + err.Error())
			return
		}
		go cmd.Wait()
	}()
}
