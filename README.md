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

# Build the executable with Windows GUI flags (hides the background terminal)
go build -ldflags="-H windowsgui" -o voice-agent.exe ./cmd/app
```

## 🧰 Built-in Tools

The agent comes with a massive registry of tools (`internal/tools/registry.go`):
* `native_click`: Programmatic OS-level clicking via UIAutomation.
* `browser_read_page` / `browser_navigate`: CDP-based Chrome control.
* `run_python`: Sandboxed script execution.
* `keyboard_combo` / `keyboard_type`: Low-level key emulation.
* File Operations (`read_file`, `write_file`, `list_files`, `open_file`).
* System Context (`screenshot_analysis`, `explain_selection`).

## 🔒 Security
The agent operates with permission profiles. Destructive actions (like `delete_file`) require explicit UI confirmation before the agent is allowed to execute them.
