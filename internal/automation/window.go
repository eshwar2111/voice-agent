package automation

import (
	"fmt"

	"github.com/go-vgo/robotgo"
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

// VerifyFocusLock returns an error if the current active window PID does not match the expected PID.
func VerifyFocusLock(expectedPID int) error {
	currentPID := robotgo.GetPid()
	// PID 0 can sometimes happen momentarily during transitions or on desktop
	if expectedPID != 0 && currentPID != 0 && currentPID != expectedPID {
		return fmt.Errorf("security abort: active window focus changed from PID %d to PID %d mid-automation", expectedPID, currentPID)
	}
	return nil
}
