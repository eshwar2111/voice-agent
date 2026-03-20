package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/yourname/voice-agent/internal/automation"
	"github.com/yourname/voice-agent/internal/ui"
)

// MouseMoveTool moves the cursor to a screen coordinate.
type MouseMoveTool struct{}

func (t *MouseMoveTool) Name() string               { return "mouse_move" }
func (t *MouseMoveTool) RequiresConfirmation() bool { return false }
func (t *MouseMoveTool) Description() string {
	return "Move the mouse cursor to an absolute screen coordinate."
}
func (t *MouseMoveTool) Parameters() string {
	return `{"x": {"type": "string", "description": "X screen coordinate"}, "y": {"type": "string", "description": "Y screen coordinate"}}`
}

type MouseMoveArgs struct {
	X string `json:"x"`
	Y string `json:"y"`
}

func (t *MouseMoveTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params MouseMoveArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	x, err := strconv.Atoi(params.X)
	if err != nil {
		return "", fmt.Errorf("mouse_move: invalid x: %w", err)
	}
	y, err := strconv.Atoi(params.Y)
	if err != nil {
		return "", fmt.Errorf("mouse_move: invalid y: %w", err)
	}
	ui.ShowAutomationStep(fmt.Sprintf("Moving mouse to (%d, %d)", x, y))
	if err := automation.MoveMouse(x, y); err != nil {
		return "", err
	}
	return fmt.Sprintf("Mouse moved to (%d, %d)", x, y), nil
}

// MouseClickTool clicks at a screen coordinate.
type MouseClickTool struct{}

func (t *MouseClickTool) Name() string               { return "mouse_click" }
func (t *MouseClickTool) RequiresConfirmation() bool { return false }
func (t *MouseClickTool) Description() string {
	return "Click the mouse at a screen coordinate. Button can be 'left', 'right', or 'center'."
}
func (t *MouseClickTool) Parameters() string {
	return `{"x": {"type": "string"}, "y": {"type": "string"}, "button": {"type": "string", "description": "left|right|center, default: left"}}`
}

type MouseClickArgs struct {
	X      string `json:"x"`
	Y      string `json:"y"`
	Button string `json:"button"`
}

func (t *MouseClickTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params MouseClickArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	x, err := strconv.Atoi(params.X)
	if err != nil {
		return "", fmt.Errorf("mouse_click: invalid x: %w", err)
	}
	y, err := strconv.Atoi(params.Y)
	if err != nil {
		return "", fmt.Errorf("mouse_click: invalid y: %w", err)
	}
	btn := params.Button
	if btn == "" {
		btn = "left"
	}
	ui.ShowAutomationStep(fmt.Sprintf("Clicking %s button at (%d, %d)", btn, x, y))
	if err := automation.ClickMouse(x, y, btn); err != nil {
		return "", err
	}
	return fmt.Sprintf("Clicked %s at (%d, %d)", btn, x, y), nil
}

// MouseDragTool drags the mouse from one coordinate to another.
type MouseDragTool struct{}

func (t *MouseDragTool) Name() string               { return "mouse_drag" }
func (t *MouseDragTool) RequiresConfirmation() bool { return false }
func (t *MouseDragTool) Description() string {
	return "Drag the mouse from (x1,y1) to (x2,y2) with the left button held."
}
func (t *MouseDragTool) Parameters() string {
	return `{"x1": {"type": "string"}, "y1": {"type": "string"}, "x2": {"type": "string"}, "y2": {"type": "string"}}`
}

type MouseDragArgs struct {
	X1 string `json:"x1"`
	Y1 string `json:"y1"`
	X2 string `json:"x2"`
	Y2 string `json:"y2"`
}

func (t *MouseDragTool) Execute(_ context.Context, rawParams json.RawMessage) (string, error) {
	var params MouseDragArgs
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}

	x1, _ := strconv.Atoi(params.X1)
	y1, _ := strconv.Atoi(params.Y1)
	x2, _ := strconv.Atoi(params.X2)
	y2, _ := strconv.Atoi(params.Y2)
	ui.ShowAutomationStep(fmt.Sprintf("Dragging from (%d,%d) → (%d,%d)", x1, y1, x2, y2))
	if err := automation.DragMouse(x1, y1, x2, y2); err != nil {
		return "", err
	}
	return fmt.Sprintf("Dragged from (%d,%d) to (%d,%d)", x1, y1, x2, y2), nil
}

// GetMousePositionTool returns the current mouse position.
type GetMousePositionTool struct{}

func (t *GetMousePositionTool) Name() string               { return "get_mouse_position" }
func (t *GetMousePositionTool) RequiresConfirmation() bool { return false }
func (t *GetMousePositionTool) Description() string {
	return "Returns the current mouse cursor position and screen size."
}
func (t *GetMousePositionTool) Parameters() string { return `{}` }
func (t *GetMousePositionTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return automation.MousePositionString(), nil
}
