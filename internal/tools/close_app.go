package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// close_app closes a RUNNING application by name — e.g. "close notepad" — by
// finding its top-level window(s) and posting WM_CLOSE, regardless of which
// window is currently focused. This is the correct behavior for a named close:
// window_control{close} only Alt+F4's the foreground window, so "close notepad"
// used to close whatever happened to be in front (often the wrong thing).
// WM_CLOSE is graceful — an app with unsaved work still gets to prompt.

var (
	caUser32   = syscall.NewLazyDLL("user32.dll")
	caKernel32 = syscall.NewLazyDLL("kernel32.dll")

	caEnumWindows          = caUser32.NewProc("EnumWindows")
	caGetWindowTextW       = caUser32.NewProc("GetWindowTextW")
	caIsWindowVisible      = caUser32.NewProc("IsWindowVisible")
	caGetWindowThreadPID   = caUser32.NewProc("GetWindowThreadProcessId")
	caPostMessageW         = caUser32.NewProc("PostMessageW")
	caOpenProcess          = caKernel32.NewProc("OpenProcess")
	caQueryFullImageNameW  = caKernel32.NewProc("QueryFullProcessImageNameW")
	caCloseHandle          = caKernel32.NewProc("CloseHandle")
)

const (
	caWMClose                       = 0x0010
	caProcessQueryLimitedInformation = 0x1000
)

type CloseAppTool struct{}

func (CloseAppTool) Name() string { return "close_app" }
func (CloseAppTool) Description() string {
	return "Closes a running application by name (e.g. \"close notepad\"), whichever window is focused."
}
func (CloseAppTool) RequiresConfirmation() bool { return false }
func (CloseAppTool) Parameters() string {
	return `{"type":"object","properties":{"app_name":{"type":"string","description":"The application to close, e.g. \"notepad\", \"chrome\"."}},"required":["app_name"]}`
}

type closeAppArgs struct {
	AppName string `json:"app_name"`
}

func (CloseAppTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var a closeAppArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid parameters: %w", err)
	}
	q := cleanCloseQuery(a.AppName)
	if q == "" {
		return "", fmt.Errorf("no app name given")
	}

	closedProcs := map[string]bool{}
	closedTitles := 0
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if r, _, _ := caIsWindowVisible.Call(hwnd); r == 0 {
			return 1 // skip invisible; keep enumerating
		}
		title := caWindowText(hwnd)
		if title == "" {
			return 1
		}
		proc := caProcessName(hwnd)
		lt, lp := strings.ToLower(title), strings.ToLower(proc)
		// Never close our own overlay.
		if strings.Contains(lp, "voice-agent") {
			return 1
		}
		if strings.Contains(lt, q) || lp == q+".exe" || strings.Contains(lp, q) {
			caPostMessageW.Call(hwnd, caWMClose, 0, 0)
			closedTitles++
			if proc != "" {
				closedProcs[proc] = true
			}
		}
		return 1
	})
	caEnumWindows.Call(cb, 0)

	if closedTitles == 0 {
		return "", fmt.Errorf("no open window matching %q", a.AppName)
	}
	if len(closedProcs) > 0 {
		names := make([]string, 0, len(closedProcs))
		for p := range closedProcs {
			names = append(names, strings.TrimSuffix(p, ".exe"))
		}
		return fmt.Sprintf("Closed %s", strings.Join(names, ", ")), nil
	}
	return fmt.Sprintf("Closed %s", q), nil
}

// cleanCloseQuery reduces "close the notepad window" etc. to "notepad".
func cleanCloseQuery(s string) string {
	q := strings.ToLower(strings.TrimSpace(s))
	for _, v := range []string{"close ", "quit ", "exit ", "kill ", "end "} {
		q = strings.TrimPrefix(q, v)
	}
	for _, lead := range []string{"the ", "my ", "app "} {
		q = strings.TrimPrefix(q, lead)
	}
	for _, tail := range []string{" window", " app", " application"} {
		q = strings.TrimSuffix(q, tail)
	}
	return strings.TrimSpace(q)
}

func caWindowText(hwnd uintptr) string {
	buf := make([]uint16, 256)
	r, _, _ := caGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:r])
}

func caProcessName(hwnd uintptr) string {
	var pid uint32
	caGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	h, _, _ := caOpenProcess.Call(caProcessQueryLimitedInformation, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer caCloseHandle.Call(h)
	buf := make([]uint16, 260)
	sz := uint32(len(buf))
	r, _, _ := caQueryFullImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&sz)))
	if r == 0 {
		return ""
	}
	return filepath.Base(syscall.UTF16ToString(buf[:sz]))
}
