package ui

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
)

//go:embed assets
var assetFS embed.FS

// assetServer serves the embedded UI over loopback. WebView2 needs a real
// origin (not SetHtml) for ES modules to load, and the widget platform will
// need to serve additional files later.
type assetServer struct {
	URL string // e.g. http://127.0.0.1:52341/9f3a.../  — always ends in "/"
	ln  net.Listener
	srv *http.Server
}

func startAssetServer() (*assetServer, error) {
	sub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		return nil, err
	}

	// Random per-launch path prefix so another local process that guesses the
	// port still can't fetch the UI. The assets aren't secret, but the surface
	// costs nothing to close.
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	prefix := "/" + hex.EncodeToString(buf) + "/"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle(prefix, http.StripPrefix(prefix, http.FileServer(http.FS(sub))))
	srv := &http.Server{Handler: mux}
	// A dead asset server presents as a permanently blank overlay with no
	// diagnostic — the WebView just never finishes loading — so a silently
	// discarded Serve error here is exactly the kind of failure manual QA
	// (the very next step after this fix) would otherwise have no log line
	// to explain. http.ErrServerClosed is the expected return from Close()
	// and isn't worth logging.
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[ui] asset server stopped: %v", err)
		}
	}()

	return &assetServer{
		URL: "http://" + ln.Addr().String() + prefix,
		ln:  ln,
		srv: srv,
	}, nil
}

func (s *assetServer) Close() error {
	if s == nil || s.srv == nil {
		return nil
	}
	return s.srv.Close()
}
