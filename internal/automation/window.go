package automation

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/go-vgo/robotgo"
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procEnumWindows              = user32.NewProc("EnumWindows")
)

// GetActiveWindowTitle returns the title of the currently focused window.
func GetActiveWindowTitle() string {
	pid := robotgo.GetPid()
	title := robotgo.GetTitle(pid)
	return title
}

// GetActivePID returns the process ID of the currently focused window.
func GetActivePID() int {
	return robotgo.GetPid()
}

// ForceFocusPID attempts to find the main window for a given PID and bring it to the foreground.
func ForceFocusPID(targetPID uint32) error {
	var targetHwnd uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid == targetPID {
			targetHwnd = hwnd
			return 0 // stop enumeration
		}
		return 1 // continue
	})
	procEnumWindows.Call(cb, 0)

	if targetHwnd != 0 {
		procSetForegroundWindow.Call(targetHwnd)
		return nil
	}
	return fmt.Errorf("could not find window for PID %d", targetPID)
}

// VerifyFocusLock returns an error if the current active window PID does not match the expected PID.
func VerifyFocusLock(expectedPID int) error {
	currentPID := robotgo.GetPid()
	// PID 0 can sometimes happen momentarily during transitions or on desktop
	if expectedPID != 0 && currentPID != 0 && currentPID != expectedPID {
		return fmt.Errorf("security abort: active window focus changed from PID %d to PID %d mid-automation", expectedPID, currentPID)
	}
	return nil
}
