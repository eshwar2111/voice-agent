package auth

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

var (
	callbackMu   sync.Mutex
	activeServer *http.Server
)

// StopCallbackServer shuts down any running callback server.
func StopCallbackServer() {
	callbackMu.Lock()
	defer callbackMu.Unlock()
	if activeServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		activeServer.Shutdown(shutdownCtx)
		activeServer = nil
	}
}

// StartCallbackServer runs a temporary server to handle the OAuth callback.
// It first stops any existing callback server to avoid port conflicts.
func StartCallbackServer(port int, stateExpected string, resultChan chan<- string, errorChan chan<- error) *http.Server {
	// Kill any leftover server from a previous attempt
	StopCallbackServer()

	callbackMu.Lock()
	defer callbackMu.Unlock()

	mux := http.NewServeMux()
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}
	activeServer = server

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		code := r.URL.Query().Get("code")
		errStr := r.URL.Query().Get("error")

		if errStr != "" {
			errorChan <- fmt.Errorf("auth error: %s", errStr)
			fmt.Fprintf(w, "Authentication failed: %s. You can close this tab.", errStr)
			go func() {
				time.Sleep(1 * time.Second)
				StopCallbackServer()
			}()
			return
		}

		if state != stateExpected {
			errorChan <- fmt.Errorf("invalid state: expected %s, got %s", stateExpected, state)
			http.Error(w, "Invalid state", http.StatusBadRequest)
			return
		}

		if code == "" {
			errorChan <- fmt.Errorf("no code received")
			http.Error(w, "Code not found", http.StatusBadRequest)
			return
		}

		resultChan <- code

		fmt.Fprintf(w, `<html><body style="font-family:sans-serif;text-align:center;padding-top:80px;background:#0a0a0a;color:#00FF94">
			<h2>&#x2705; Connected!</h2><p>You can now close this tab and return to Voice Agent.</p></body></html>`)

		go func() {
			time.Sleep(1 * time.Second)
			StopCallbackServer()
		}()
	})

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			select {
			case errorChan <- fmt.Errorf("callback server error: %w", err):
			default:
			}
		}
	}()

	return server
}
