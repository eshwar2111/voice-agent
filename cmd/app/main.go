package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/asr"
	"github.com/yourname/voice-agent/internal/audit"
	"github.com/yourname/voice-agent/internal/command"
	"github.com/yourname/voice-agent/internal/engine"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/memory"
	"github.com/yourname/voice-agent/internal/search"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/ui"
)

func main() {
	configPath := filepath.Join("config.json")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config from %s: %v", configPath, err)
	}

	// Apply whisper paths from config
	if cfg.EnableVoice {
		asr.SetPaths(cfg.WhisperPath, cfg.WhisperModel)
	} else {
		log.Println("Voice disabled (enable_voice=false); skipping Whisper init.")
	}

	provider, err := llm.NewProvider(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize LLM provider: %v", err)
	}

	err = audit.InitDB("audit_logs.db")
	if err != nil {
		log.Printf("Warning: Failed to initialize audit database: %v", err)
	}

	memDB, err := sql.Open("sqlite3", "memory.db?_journal=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("Failed to open memory database: %v", err)
	}
	// Ensure connection pool limits for shared SQLite connection
	memDB.SetMaxOpenConns(1)
	memDB.SetMaxIdleConns(1)

	memStore, err := memory.NewStore(memDB)
	if err != nil {
		log.Fatalf("Failed to initialize memory store: %v", err)
	}
	memRetriever := memory.NewRetriever(memDB)

	// Setup graceful shutdown context with OS signal handling
	rootCtx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down gracefully...", sig)
		cancel()
	}()

	// Daily TTL cleanup: prune low-importance memories older than 30 days
	ttlDone := make(chan struct{})
	go func() {
		defer close(ttlDone)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-rootCtx.Done():
				log.Println("TTL cleanup goroutine stopped")
				return
			case <-ticker.C:
				// Prune memories
				nm, err := memStore.PruneOld(30*24*time.Hour, memory.ImportanceMedium)
				if err != nil {
					log.Printf("Memory TTL cleanup error: %v", err)
				} else if nm > 0 {
					log.Printf("Memory cleanup: pruned %d old low-importance memories", nm)
				}

				// Prune audit logs (e.g., older than 7 days)
				na, err := audit.PruneOld(7 * 24 * time.Hour)
				if err != nil {
					log.Printf("Audit TTL cleanup error: %v", err)
				} else if na > 0 {
					log.Printf("Audit cleanup: pruned %d old logs", na)
				}
			}
		}
	}()

	registry := tools.DefaultRegistryWithConfig(provider, cfg)
	registry.Register(&tools.SaveMemoryTool{Store: memStore})
	registry.Register(&tools.RememberTool{Store: memStore})
	registry.Register(&tools.RecallTool{Retriever: memRetriever})
	registry.Register(&tools.ListMemoriesTool{Store: memStore})

	// Security Setup
	profile := security.DeveloperProfile()
	rateLimiter := security.NewRateLimiter(5) // 5 seconds cooldown

	command.InitRouter(registry, provider, &profile)
	command.SetCancelFunc(cancel)
	ui.OnCommand = command.ProcessCommand
	ui.OnHistoryUp = command.GetPreviousHistory
	ui.OnHistoryDown = command.GetNextHistory

	// Setup Search Indexer
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile = "C:\\"
	}
	search.InitIndexer(userProfile) // async

	fmt.Println("======================================")
	fmt.Println("🤖 Voice Agent MVP - Listening Mode Active")
	fmt.Println("======================================")

	// Start Global Hotkey Listener
	go command.ListenHotkey()

	// Automation & highlight overlays now lazy-init on first use
	// (ShowAutomationStep / FlashHighlightBox), instead of launching here.

	// Initialize and run the Event-Driven Engine
	engineApp := engine.NewEngine(cfg, provider, registry, memStore, memRetriever, rateLimiter, &profile)
	go engineApp.Start(rootCtx)

	// The WebView UI Engine *must* run on the main OS thread to avoid crashes.
	// When the WebView closes, we trigger shutdown
	ui.StartOverlay(rootCtx, cfg)

	// WebView closed — trigger graceful shutdown
	log.Println("WebView closed, starting graceful shutdown...")
	cancel()
	command.Shutdown()

	// Wait for TTL cleanup to finish (with timeout)
	select {
	case <-ttlDone:
	case <-time.After(5 * time.Second):
		log.Println("TTL cleanup did not finish in time, forcing exit")
	}

	// Close databases
	audit.CloseDB()
	memDB.Close()

	log.Println("Graceful shutdown complete")
}
