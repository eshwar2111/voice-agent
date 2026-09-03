package tools

import (
	"encoding/json"
	"log"
	"sort"

	"github.com/yourname/voice-agent/config"
	"github.com/yourname/voice-agent/internal/llm"
	"github.com/yourname/voice-agent/internal/vision"
)

type Registry struct {
	tools         map[string]Tool
	cachedSchemas string
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (r *Registry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
	r.cachedSchemas = "" // invalidate cache
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) ToolNames() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DumpSchemas returns a JSON string representing all registered tools and their parameters.
// The result is cached and invalidated when new tools are registered.
func (r *Registry) DumpSchemas() string {
	if r.cachedSchemas == "" {
		r.cachedSchemas = r.dumpSchemas(nil)
	}
	return r.cachedSchemas
}

// DumpSchemasFiltered returns a JSON string representing only the tools that match the predicate.
func (r *Registry) DumpSchemasFiltered(allow func(name string, tool Tool) bool) string {
	return r.dumpSchemas(allow)
}

func (r *Registry) dumpSchemas(allow func(name string, tool Tool) bool) string {
	schemas := make(map[string]map[string]interface{}, len(r.tools))
	for name, tool := range r.tools {
		if allow != nil && !allow(name, tool) {
			continue
		}
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(tool.Parameters()), &params); err != nil {
			log.Printf("Warning: tool %q has invalid parameter schema: %v", name, err)
			params = map[string]interface{}{"type": "object"}
		}
		schemas[name] = map[string]interface{}{
			"description": tool.Description(),
			"parameters":  params,
		}
	}
	b, err := json.MarshalIndent(schemas, "", "  ")
	if err != nil {
		log.Printf("Error marshaling tool schemas: %v", err)
		return "{}"
	}
	return string(b)
}

// DefaultRegistry returns a standard registry with all core tools
func DefaultRegistry(provider llm.Provider) *Registry {
	return DefaultRegistryWithConfig(provider, nil)
}

func DefaultRegistryWithConfig(provider llm.Provider, cfg *config.Config) *Registry {
	r := NewRegistry()
	r.Register(&OpenAppTool{})
	r.Register(&WebSearchTool{})
	r.Register(&ResearchTool{Provider: provider})
	r.Register(&OpenWebsiteTool{})
	r.Register(&CreateFileTool{})
	r.Register(&OpenExplorerTool{})
	r.Register(&OpenFileTool{})
	r.Register(&FindFileTool{})
	r.Register(&RememberFileTool{})
	r.Register(&ListFilesTool{})
	r.Register(&DeleteFileTool{})
	r.Register(&MoveFileTool{})
	r.Register(&SpeakTool{})
	r.Register(&ReadFileTool{})
	r.Register(&WriteFileTool{})
	r.Register(&DatetimeTool{})

	// New Context Tools
	r.Register(&SummarizeClipboardTool{Provider: provider})
	r.Register(&RewriteClipboardTool{Provider: provider})
	r.Register(&ExplainSelectionTool{Provider: provider})
	r.Register(&ScreenshotAnalysisTool{Provider: provider})

	// Automation Tools
	r.Register(&NativeClickTool{})
	r.Register(&MouseMoveTool{})
	r.Register(&MouseClickTool{})
	r.Register(&MouseDragTool{})
	r.Register(&GetMousePositionTool{})
	r.Register(&KeyboardTypeTool{})
	r.Register(&KeyboardPressTool{})
	r.Register(&KeyboardComboTool{})
	r.Register(&WaitTool{})
	r.Register(&MediaControlTool{})
	r.Register(&WindowControlTool{})

	// System Control
	r.Register(&SystemControlTool{})

	// Vision Automation Tools (Phase 8)
	uiDetector := &vision.UIDetector{Provider: provider}
	r.Register(&FindAndClickTool{Detector: uiDetector})
	r.Register(&ScrollAndFindTool{Detector: uiDetector})
	r.Register(&VerifyScreenStateTool{Provider: provider})

	r.Register(&RunPythonTool{})
	r.Register(&SystemStatusTool{})
	r.Register(&TimerTool{})

	// Browser Tools
	r.Register(&BrowserReadPageTool{})
	r.Register(&BrowserNavigateTool{})

	if cfg != nil {
		r.Register(&GoogleCalendarListTool{Cfg: cfg})
		r.Register(&GoogleCalendarAddEventTool{Cfg: cfg})
		r.Register(&GoogleCalendarSearchTool{Cfg: cfg})
		r.Register(&GoogleDriveListTool{Cfg: cfg})
		r.Register(&GoogleDocsReadTool{Cfg: cfg})
		r.Register(&GoogleDocsCreateTool{Cfg: cfg})
		r.Register(&GoogleDocsSearchTool{Cfg: cfg})
		r.Register(&GoogleSheetsReadTool{Cfg: cfg})
		r.Register(&GoogleSheetsWriteTool{Cfg: cfg})
		r.Register(&GoogleSheetsSearchTool{Cfg: cfg})
		r.Register(&GoogleSlidesReadTool{Cfg: cfg})
		r.Register(&GoogleSlidesCreateTool{Cfg: cfg})
		r.Register(&GoogleSlidesSearchTool{Cfg: cfg})
		r.Register(&GoogleGmailListTool{Cfg: cfg})
		r.Register(&GoogleGmailSendTool{Cfg: cfg})
		r.Register(&GoogleGmailSearchTool{Cfg: cfg})
		r.Register(&GoogleGmailGetEmailTool{Cfg: cfg})
		r.Register(&GoogleAIAssistantTool{Cfg: cfg, Provider: provider})
		r.Register(&GoogleWorkspaceAssistantTool{Cfg: cfg, Provider: provider})

		r.Register(&SpotifyNowPlayingTool{Cfg: cfg})
		r.Register(&SpotifyPlayTool{Cfg: cfg})
		r.Register(&SpotifyPauseTool{Cfg: cfg})
		r.Register(&SpotifyNextTool{Cfg: cfg})
		r.Register(&SpotifyPreviousTool{Cfg: cfg})
		r.Register(&SpotifyVolumeTool{Cfg: cfg})
		r.Register(&SpotifySearchTool{Cfg: cfg})
		r.Register(&SpotifyPlaylistsTool{Cfg: cfg})
		r.Register(&SpotifyDeviceTool{Cfg: cfg})
		r.Register(&SpotifyQueueTool{Cfg: cfg})
		r.Register(&SpotifyAccountTool{Cfg: cfg})
		r.Register(&SpotifyAICuratTool{Cfg: cfg, Provider: provider})
		r.Register(&SpotifySmartRecommendTool{Cfg: cfg, Provider: provider})
		r.Register(&SpotifyAssistantTool{Cfg: cfg, Provider: provider})
		r.Register(&SpotifyWorkflowAgentTool{Cfg: cfg, Provider: provider})
		r.Register(&SpotifyContextualMoodTool{Cfg: cfg, Provider: provider})
		r.Register(&SpotifySeekTool{Cfg: cfg})
		r.Register(&SpotifySaveTrackTool{Cfg: cfg})
		r.Register(&SpotifyTransferTool{Cfg: cfg})

		r.Register(&GoogleWorkflowAgentTool{Cfg: cfg, Provider: provider})
		r.Register(&GoogleWorkspaceBriefTool{Cfg: cfg, Provider: provider})
	}

	return r
}
