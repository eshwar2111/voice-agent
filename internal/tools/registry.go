package tools

import (
	"encoding/json"
	"log"

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

// DumpSchemas returns a JSON string representing all registered tools and their parameters.
// The result is cached and invalidated when new tools are registered.
func (r *Registry) DumpSchemas() string {
	if r.cachedSchemas != "" {
		return r.cachedSchemas
	}
	schemas := make(map[string]map[string]interface{})
	for name, tool := range r.tools {
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
	r.cachedSchemas = string(b)
	return r.cachedSchemas
}

// DefaultRegistry returns a standard registry with all core tools
func DefaultRegistry(provider llm.Provider) *Registry {
	r := NewRegistry()
	r.Register(&OpenAppTool{})
	r.Register(&WebSearchTool{})
	r.Register(&ResearchTool{Provider: provider})
	r.Register(&OpenWebsiteTool{})
	r.Register(&CreateFileTool{})
	r.Register(&OpenExplorerTool{})
	r.Register(&OpenFileTool{})
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
	r.Register(&MouseMoveTool{})
	r.Register(&MouseClickTool{})
	r.Register(&MouseDragTool{})
	r.Register(&GetMousePositionTool{})
	r.Register(&KeyboardTypeTool{})
	r.Register(&KeyboardPressTool{})
	r.Register(&KeyboardComboTool{})
	r.Register(&WaitTool{})

	// Vision Automation Tools (Phase 8)
	uiDetector := &vision.UIDetector{Provider: provider}
	r.Register(&FindAndClickTool{Detector: uiDetector})
	r.Register(&ScrollAndFindTool{Detector: uiDetector})
	r.Register(&VerifyScreenStateTool{Provider: provider})

	return r
}
