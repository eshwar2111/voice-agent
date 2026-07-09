package ambient

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

type DownloadsSource struct{ Dir string }

func NewDownloadsSource() *DownloadsSource {
	return &DownloadsSource{Dir: filepath.Join(os.Getenv("USERPROFILE"), "Downloads")}
}

func (d *DownloadsSource) Name() string { return "downloads" }

func (d *DownloadsSource) Start(ctx context.Context, out chan<- Suggestion) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[ambient/downloads] watcher: %v", err)
		return
	}
	defer w.Close()
	if err := w.Add(d.Dir); err != nil {
		log.Printf("[ambient/downloads] watch %s: %v", d.Dir, err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// A finished download appears as a Create (final name) or a Rename to the final name.
			if ev.Op&(fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			name := filepath.Base(ev.Name)
			m, ok := ClassifyDownload(name)
			if !ok {
				continue
			}
			if fi, err := os.Stat(ev.Name); err != nil || fi.IsDir() {
				continue
			}
			out <- d.suggest(m, ev.Name, name)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("[ambient/downloads] error: %v", err)
		}
	}
}

func (d *DownloadsSource) suggest(m FileMatch, path, name string) Suggestion {
	s := Suggestion{Source: "downloads", Icon: m.Icon, Action: m.Action, Title: "Download finished", DedupKey: "dl:" + path}
	switch m.Kind {
	case "archive":
		s.Message = name + " — unzip it here?"
		dest := path[:len(path)-len(filepath.Ext(path))]
		s.Run = func(ctx context.Context) error { return unzip(path, dest) }
	case "image":
		s.Message = name + " — open it?"
		s.Run = func(ctx context.Context) error { return openPath(path) }
	case "installer":
		s.Title = "Installer downloaded"
		s.Message = name + " — run it?"
		s.Run = func(ctx context.Context) error { return openPath(path) }
	}
	return s
}
