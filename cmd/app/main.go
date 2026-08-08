package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/ambient"
	"github.com/yourname/voice-agent/internal/asr"
	"github.com/yourname/voice-agent/internal/audit"
	"github.com/yourname/voice-agent/internal/command"
	agentctx "github.com/yourname/voice-agent/internal/context"
	"github.com/yourname/voice-agent/internal/engine"
	"github.com/yourname/voice-agent/internal/island"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/memory"
	"github.com/yourname/voice-agent/internal/search"
	"github.com/yourname/voice-agent/internal/security"
	"github.com/yourname/voice-agent/internal/tools"
	"github.com/yourname/voice-agent/internal/trust"
	"github.com/yourname/voice-agent/internal/ui"
	"github.com/yourname/voice-agent/internal/wakeword"
)

// nextMeetingFromCalendar adapts the existing Google Calendar integration
// (GoogleCalendarListTool) to the island.MeetingSource contract, reusing its
// fetch path (OAuth client + calendar.Events.List) rather than opening a
// second Calendar client.
//
// Contract: returns the soonest event that has not yet started, or (nil,
// nil) when there is none. An unlinked Google account is the common steady
// state for most users, not a failure — returning an error there would make
// the runner's backoff retry (and log) forever for the life of every session
// for every user who never connected the account. A genuine API failure
// (network, auth refresh, quota) IS an error: returning (nil, nil) there
// would look identical to "no meetings" and the backoff would never engage.
func nextMeetingFromCalendar(ctx context.Context, cfg *config.Config) (*island.NextMeeting, error) {
	if cfg.GoogleToken == "" {
		return nil, nil // not linked — no meetings is the correct answer, not an error
	}

	raw, err := (&tools.GoogleCalendarListTool{Cfg: cfg}).Execute(ctx, json.RawMessage(`{"maxResults":10}`))
	if err != nil {
		return nil, fmt.Errorf("calendar fetch: %w", err)
	}

	// Mirrors GoogleCalendarListTool.Execute's EventInfo shape (see
	// internal/tools/google_calendar.go), wrapped in the ToolResult envelope.
	var result struct {
		Artifacts struct {
			Data []struct {
				Summary   string `json:"summary"`
				StartTime string `json:"startTime"`
				JoinLink  string `json:"joinLink"`
			} `json:"data"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("calendar response: %w", err)
	}

	now := time.Now()
	var best *island.NextMeeting
	for _, ev := range result.Artifacts.Data {
		if ev.StartTime == "" {
			continue
		}
		t, perr := time.Parse(time.RFC3339, ev.StartTime)
		if perr != nil {
			// All-day events carry a date-only Start.Date ("2026-08-10"), not a
			// timestamp — not a meaningful countdown target, so skip rather than
			// error the whole poll over one malformed entry.
			continue
		}
		if !t.After(now) {
			continue // already started
		}
		if best == nil || t.Before(best.StartsAt) {
			best = &island.NextMeeting{Title: ev.Summary, StartsAt: t, JoinURL: ev.JoinLink}
		}
	}
	return best, nil
}

func main() {
	// Debug log to a file next to the exe — the -H windowsgui build has no console,
	// so this is the only way to see logs. Tail voice-agent.log while testing.
	if lf, lerr := os.OpenFile("voice-agent.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); lerr == nil {
		// File only — the -H windowsgui build has no valid os.Stderr, and writing to a
		// bad stderr handle via MultiWriter aborts the whole log line.
		log.SetOutput(lf)
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Println("========== voice-agent starting ==========")

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

	// Trust layer: build a single *trust.TrustedExecutor and inject it into every
	// execution path (dispatch → engine + router AI commands), gated by config.
	// The LLM judge only fires for fuzzy GUI/vision steps; it never blocks on an
	// unavailable provider (returns verified=true on error).
	//
	// Known limitation (v1): Replan is a no-op — no LLM re-plan is wired yet, so
	// the recovery ladder degrades Replan → Ask (spec-correct fallthrough).
	var trustedExec *trust.TrustedExecutor
	if cfg.TrustedExecution {
		judge := func(ctx context.Context, goal, obs string) (bool, string) {
			out, jerr := provider.Generate(ctx, "Goal: "+goal+"\nObservation: "+obs+
				"\nDid the action succeed? Answer 'yes' or 'no' then a short reason.", nil)
			if jerr != nil {
				return true, "" // never block on an unavailable judge
			}
			yes := strings.HasPrefix(strings.ToLower(strings.TrimSpace(out)), "yes")
			return yes, out
		}
		trustedExec = &trust.TrustedExecutor{
			Classifier: trust.NewRiskClassifier(),
			Verifier:   trust.NewStepVerifier(judge),
			Recoverer:  trust.NewLadderRecoverer(2),
			Confirm:    ui.RequestConfirmationCard,
			Describe:   trust.DefaultDescribe,
			Narrate:    ui.ShowNotification,
			Ask: func(s trust.Step, reason string) trust.Decision {
				if ui.AskStepChoice(reason) {
					return trust.Retry
				}
				return trust.Abort
			},
			Replan: func(ctx context.Context, remaining []trust.Step, failed trust.Step, err error) []trust.Step {
				return nil // v1: no LLM re-plan wired yet → ladder falls through to Ask
			},
		}
		log.Println("[trust] trustworthy execution layer enabled")
	} else {
		log.Println("[trust] trustworthy execution layer disabled (trusted_execution=false)")
	}
	command.SetTrusted(trustedExec)

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
	engineApp := engine.NewEngine(cfg, provider, registry, memStore, memRetriever, rateLimiter, &profile, trustedExec)
	go engineApp.Start(rootCtx)

	// Start the ambient (proactive suggestions) engine, gated by config + privacy mode.
	if cfg.EnableProactive && !cfg.PrivacyMode {
		amb := &ambient.Engine{
			Sources: []ambient.Source{
				ambient.NewDownloadsSource(),
				&ambient.CalendarSource{Cfg: cfg},
				&ambient.ClipboardSource{OnExplain: func(ctx context.Context, text string) error {
					return engineApp.Dispatch.Handle(ctx, "explain this error: "+text, agentctx.Capture{})
				}},
			},
			Policy:  ambient.NewPolicy(90 * time.Second),
			UI:      ambient.DelivererFunc(ui.ShowSuggestion),
			Busy:    engineApp.IsBusy,
			Enabled: func() bool { return cfg.EnableProactive && !cfg.PrivacyMode },
		}
		ui.OnSuggestionAccept = amb.Accept
		ui.OnSuggestionDismiss = amb.Dismiss
		go amb.Run(rootCtx)
	}

	// Start wake-word loop when voice is enabled and a Porcupine key is configured.
	if cfg.EnableVoice && cfg.PorcupineAccessKey != "" {
		go func() {
			onDetect := func() { engineApp.TriggerAndWait(60 * time.Second) }
			if err := wakeword.StartWakeWordLoop(rootCtx, cfg.PorcupineAccessKey, onDetect, engineApp.IsBusy); err != nil {
				log.Printf("wake word stopped: %v", err)
			}
		}()
	}

	// Live activities (SP6): registry + providers, started on rootCtx so every
	// provider goroutine stops cleanly on shutdown alongside everything else.
	islandReg := island.NewRegistry(island.SystemClock{}, ui.PublishActivities)
	ui.SetIslandRegistry(islandReg) // lets the dismiss binding reach it

	meetings := island.NewMeetingProvider(island.SystemClock{}, func(ctx context.Context) (*island.NextMeeting, error) {
		return nextMeetingFromCalendar(ctx, cfg)
	})
	islandRunner := island.NewRunner(islandReg, island.DefaultTimers, meetings)
	islandRunner.Start(rootCtx)

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
