# Voice Agent

A powerful, native Windows desktop assistant written in Go. The Voice Agent listens to your commands, understands your desktop context, and can natively automate applications, interact with the web, and manage your files.

## 🚀 Features

* **Voice First (Local ASR)**: Uses local `whisper.cpp` bindings for fast, private, and offline speech-to-text.
* **Native Desktop UI**: Features a sleek, unobtrusive "Dynamic Island" overlay built with WebView that sits on top of your desktop without stealing window focus.
* **Intelligent Planning**: Powered by LLMs (e.g., Gemini 2.5 Flash), it translates natural language into multi-step execution graphs.
* **Native Accessibility Automation**: Integrates directly with Windows UIAutomation (UIA) COM APIs to find and click native buttons without relying on brittle coordinate guessing.
* **Deep Browser Integration**: Uses the Chrome DevTools Protocol (CDP) to natively read DOM text and navigate websites reliably.
* **Multi-Turn Memory**: Remembers context across turns. If you ask it to "Open a file" and then say "Summarize it", it knows what "it" refers to.
* **Sandboxed Python Execution**: Can write and execute Python scripts on the fly to process data or perform complex logic.
* **Persistent Knowledge**: Uses a local SQLite database for long-term semantic memory and audit logging.

## 🏗️ Architecture

The agent is built around a reactive, event-driven engine (`internal/engine`):

1. **Trigger**: User clicks the UI pill or uses a wake-word.
2. **Context Gathering**: The agent immediately grabs the active window title, PID, and clipboard contents.
3. **Planning (LLM)**:
   - **Fast Path**: For simple commands (e.g., "Open Notepad"), it generates a JSON plan directly.
   - **Screen Path**: For complex tasks, it takes a screenshot and uses Vision models to understand the screen state.
4. **Execution**: The `GraphExecutor` runs the generated tools sequentially, with full support for conditional branching (fallback tasks if a step fails).
5. **Feedback**: The UI overlay updates in real-time, showing executing steps, thinking status, and highlighting targeted UI elements.

## 🛠️ Setup & Build

### Getting the source
```bash
git clone https://github.com/eshwar2111/voice-agent.git
cd voice-agent
```

The default build is **voice-free** (text/typed commands only) and needs no extra native
setup — the local Whisper speech-to-text path is opt-in behind the `whisper` build tag. To
produce the voice-enabled binary (whisper.cpp rebuild, model download, wake word), follow
**[docs/BUILD-VOICE.md](docs/BUILD-VOICE.md)**.

### Prerequisites
* Go 1.21+
* Windows OS (Currently heavily optimized for Windows APIs like `user32.dll` and UIA).
* GCC (MinGW-w64) for SQLite CGO bindings.

### Configuration
Rename `config.example.json` to `config.json` and add your API keys:
```json
{
  "llm_provider": "gemini",
  "api_key": "YOUR_GEMINI_API_KEY",
  "model": "gemini-2.5-flash",
  "whisper_path": "C:/whisper-local/build/bin/whisper-cli.exe",
  ...
}
```
*(Note: You can also update the LLM Provider and API key directly through the UI settings panel by clicking the ⚙ gear icon).*

### Building
```bash
# Format the code
go fmt ./...

# Build the executable. -s -w strips the symbol/debug tables to shrink the binary;
# -H windowsgui hides the background terminal.
go build -ldflags="-s -w -H windowsgui" -o voice-agent.exe ./cmd/app
```

> For the voice-enabled build (`-tags whisper`), see **[docs/BUILD-VOICE.md](docs/BUILD-VOICE.md)**.

> **CGO note:** the build links C dependencies (SQLite, WebView2, robotgo, whisper.cpp),
> so a matching MinGW-w64 GCC toolchain must be on `PATH` and pointed to by `go env CC`.
> The prebuilt `whisper.cpp/build/**/*.a` static libs must have been compiled with the
> *same* GCC as the Go build; a toolchain mismatch surfaces as `undefined reference to
> std::...` at link time — rebuild `whisper.cpp` with your current GCC to resolve it.

### Architecture: tiered dispatch (SP1)

Commands (voice transcripts and typed input alike) flow through a single dispatcher
(`internal/dispatch`):

- **Tier 0 — local resolver** (`internal/resolver`): a prioritized chain of deterministic
  matchers (app launch, file open, web, media, system, window, datetime) resolves the common
  cases **instantly, offline, with zero LLM tokens**, then runs the tasks via the
  `GraphExecutor`.
- **Tier 1 — cloud LLM**: anything the resolver can't confidently handle (confidence < 0.7)
  falls back to the existing multi-agent Orchestrator.

## 🧰 Built-in Tools

The agent comes with a massive registry of tools (`internal/tools/registry.go`):
* `native_click`: Programmatic OS-level clicking via UIAutomation.
* `browser_read_page` / `browser_navigate`: CDP-based Chrome control.
* `run_python`: Sandboxed script execution.
* `keyboard_combo` / `keyboard_type`: Low-level key emulation.
* File Operations (`read_file`, `write_file`, `list_files`, `open_file`).
* System Context (`screenshot_analysis`, `explain_selection`).
* `web_search`: searches DuckDuckGo and returns the top results (title, URL, snippet) as text to reason over — no longer just opens a browser.

### 🎧 Spotify

Playback, search, queue, playlists, devices, and AI curation via the Spotify Web API, plus:

* `spotify_seek`: seek within the current track — absolute (`1:30`, `90`) or relative (`+30s`, `-15s`).
* `spotify_save_track`: save (like) / remove / check the current or a given track in your Liked Songs.
* `spotify_transfer`: transfer playback to a device by name (e.g. "play on my Laptop").
* **Automatic no-device recovery:** if playback fails with `NO_ACTIVE_DEVICE`, the agent transfers to an available device and retries once — play/pause/next/previous/volume all self-heal.

> **One-time re-link for saving:** liking songs adds the `user-library-modify` scope. Existing links will get a `403` until you **re-link Spotify (⚙ → Spotify)** once; the agent will prompt you.

## 🔒 Security
The agent operates with permission profiles. Destructive actions (like `delete_file`) require explicit UI confirmation before the agent is allowed to execute them.

## 🛡️ Trustworthy execution (SP4)

Every plan is wrapped by a dependency-free trust layer (`internal/trust`) at the
`GraphExecutor` choke point, giving one-shot execution you can actually trust:

- **Risk classification** — each step is tagged `Safe`/`Risky` by a name table plus a
  param-aware bump (e.g. `system_control{action:"shutdown"}` becomes `Risky`); unknown tools
  default to `Risky`.
- **One up-front approval gate** — a single `workflow_approval` card fires **before any side
  effect** when a plan has ≥2 steps or any risky step. Reject ⇒ **zero** side effects. Re-planned
  tail steps do not trigger a second gate.
- **Cheap-first verification** — deterministic post-conditions (file created / deleted, non-empty
  result) run with no LLM cost; only fuzzy GUI/vision steps consult the optional LLM judge, which
  never blocks if the provider is unavailable.
- **Bounded recovery ladder** — retry (≤2) → re-plan (once) → ask the user (Retry/Stop card).
  No auto-rollback.

**Config flag:** `"trusted_execution"` in `config.json` **defaults to `true`** when the key is
absent; set it to `false` to run the legacy executor loop with no gate/verification.

> **Known limitation (v1):** LLM re-plan is not yet wired — `Replan` is a no-op, so the ladder
> degrades `Replan → Ask` (spec-correct fallthrough). A real re-plan is deferred to a follow-up.

## 🔔 Proactive suggestions (SP3)

The agent can proactively surface one-tap suggestions — "ZIP downloaded, unzip it?",
"meeting in 10 min, join?", "link copied, open it?" — as small dark-glass cards on the pill.

- **Opt-in:** set `"enable_proactive": true` in `config.json` (off by default). Suppressed when
  `"privacy_mode": true`.
- **Sources:** Downloads (filesystem watcher), Calendar (Google/Microsoft, needs linked OAuth),
  Clipboard (URL / tracking number / error).
- **Never noisy:** one card at a time, deduped, rate-limited, and suppressed while the assistant
  is busy or speaking. No LLM runs just because a trigger fired — only an accepted action that
  needs it (e.g. "explain this error") reaches the cloud.
