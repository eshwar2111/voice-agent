package context

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

type WindowContext struct {
	ProcessName string
	WindowTitle string
	PID         uint32
}

func GetActiveWindowContext() (*WindowContext, error) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return nil, fmt.Errorf("no active window")
	}

	title := make([]uint16, 256)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	windowTitle := syscall.UTF16ToString(title)

	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))

	processName := getProcessName(pid)

	return &WindowContext{
		ProcessName: processName,
		WindowTitle: windowTitle,
		PID:         pid,
	}, nil
}

func getProcessName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "unknown"
	}
	defer windows.CloseHandle(h)

	path := make([]uint16, 1024)
	size := uint32(len(path))
	err = windows.QueryFullProcessImageName(h, 0, &path[0], &size)
	if err != nil {
		return "unknown"
	}

	fullPath := syscall.UTF16ToString(path)
	lastSlash := -1
	for i := len(fullPath) - 1; i >= 0; i-- {
		if fullPath[i] == '\\' || fullPath[i] == '/' {
			lastSlash = i
			break
		}
	}
	if lastSlash >= 0 {
		return fullPath[lastSlash+1:]
	}
	return fullPath
}
