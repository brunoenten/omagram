package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/brunoenten/omagram/internal/tele"
)

type fileState int

const (
	fileIdle fileState = iota
	fileDownloading
	fileReady
	fileFailed
)

type fileEntry struct {
	state fileState
	path  string
	err   string
	done  int64
	total int64
}

// files downloads documents into the user's Downloads folder and opens them.
type files struct {
	mu         sync.Mutex
	m          map[int64]*fileEntry
	dir        string
	invalidate func()
}

func newFiles(dir string, invalidate func()) *files {
	return &files{m: map[int64]*fileEntry{}, dir: dir, invalidate: invalidate}
}

func (f *files) path(file *tele.File) string {
	name := sanitizeName(file.Name)
	if name == "" {
		name = fmt.Sprintf("%d", file.ID)
	}
	return filepath.Join(f.dir, fmt.Sprintf("%d_%s", file.ID, name))
}

func sanitizeName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == 0:
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), " .")
	if len([]rune(s)) > 120 {
		s = string([]rune(s)[:120])
	}
	return s
}

func (f *files) get(id int64) fileEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.m[id]; ok {
		return *e
	}
	return fileEntry{}
}

// open downloads the file if needed and hands it to xdg-open.
func (f *files) open(c *tele.Client, file *tele.File, status func(string)) {
	path := f.path(file)
	f.mu.Lock()
	if e, ok := f.m[file.ID]; ok && e.state == fileDownloading {
		f.mu.Unlock()
		status("already downloading…")
		return
	}
	if _, err := os.Stat(path); err == nil {
		e := &fileEntry{state: fileReady, path: path, total: file.Size, done: file.Size}
		f.m[file.ID] = e
		f.mu.Unlock()
		f.launch(path, status)
		return
	}
	e := &fileEntry{state: fileDownloading, path: path, total: file.Size}
	f.m[file.ID] = e
	f.mu.Unlock()
	f.invalidate()
	status(fmt.Sprintf("downloading %s…", displayName(file)))

	go func() {
		err := c.DownloadFile(*file, path, func(done, total int64) {
			f.mu.Lock()
			e.done, e.total = done, total
			f.mu.Unlock()
			if total > 0 {
				status(fmt.Sprintf("downloading %s… %d%%", displayName(file), done*100/total))
			}
			f.invalidate()
		})
		f.mu.Lock()
		if err != nil {
			e.state, e.err = fileFailed, err.Error()
			f.mu.Unlock()
			status("download: " + err.Error())
			f.invalidate()
			return
		}
		e.state, e.done = fileReady, file.Size
		f.mu.Unlock()
		f.invalidate()
		f.launch(path, status)
	}()
}

func (f *files) launch(path string, status func(string)) {
	status("opening " + filepath.Base(path))
	cmd := exec.Command("xdg-open", path)
	if err := cmd.Start(); err != nil {
		status("xdg-open: " + err.Error())
		return
	}
	go cmd.Wait()
}

func displayName(file *tele.File) string {
	if file.Title != "" {
		return file.Title
	}
	if file.Name != "" {
		return file.Name
	}
	return file.Label()
}
