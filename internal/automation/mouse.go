package automation

import (
	"fmt"
	"runtime"
	"syscall"
	"time"

	"github.com/go-vgo/robotgo"
)

// getDPIScale returns the scaling factor for Windows, or 1.0 for others.
func getDPIScale() float64 {
	if runtime.GOOS != "windows" {
		return 1.0
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	getDpiForSystem := user32.NewProc("GetDpiForSystem")
	if err := getDpiForSystem.Find(); err == nil {
		dpi, _, _ := getDpiForSystem.Call()
		if dpi > 0 {
			return float64(dpi) / 96.0
		}
	}
	return 1.0
}

func scaleCoords(x, y int) (int, int) {
	scale := getDPIScale()
	return int(float64(x) / scale), int(float64(y) / scale)
}

// MoveMouse moves the cursor to the given screen coordinates.
func MoveMouse(x, y int) error {
	expectedPID := GetActivePID()
	sx, sy := scaleCoords(x, y)
	robotgo.Move(sx, sy)
	return VerifyFocusLock(expectedPID)
}

// ClickMouse moves to (x,y) then clicks the given button ("left", "right", "center").
func ClickMouse(x, y int, button string) error {
	if button == "" {
		button = "left"
	}
	expectedPID := GetActivePID()
	sx, sy := scaleCoords(x, y)
	robotgo.Move(sx, sy)
	time.Sleep(150 * time.Millisecond) // longer settle time for target window to register hover/focus
	if err := VerifyFocusLock(expectedPID); err != nil {
		return err
	}
	robotgo.Click(button)
	return nil
}

// DoubleClick moves to (x,y) and performs a double click.
func DoubleClick(x, y int) error {
	expectedPID := GetActivePID()
	sx, sy := scaleCoords(x, y)
	robotgo.Move(sx, sy)
	time.Sleep(150 * time.Millisecond)
	if err := VerifyFocusLock(expectedPID); err != nil {
		return err
	}
	robotgo.Click("left", true) // true = double
	return nil
}

// DragMouse drags from (x1,y1) to (x2,y2) with the left button held.
func DragMouse(x1, y1, x2, y2 int) error {
	expectedPID := GetActivePID()
	sx1, sy1 := scaleCoords(x1, y1)
	robotgo.Move(sx1, sy1)
	time.Sleep(80 * time.Millisecond)
	if err := VerifyFocusLock(expectedPID); err != nil {
		return err
	}
	sx2, sy2 := scaleCoords(x2, y2)
	robotgo.DragSmooth(sx2, sy2)
	return nil
}

// ScrollMouse scrolls up (positive amount) or down (negative).
func ScrollMouse(amount int) error {
	expectedPID := GetActivePID()
	dir := "up"
	if amount < 0 {
		dir = "down"
		amount = -amount
	}
	if err := VerifyFocusLock(expectedPID); err != nil {
		return err
	}
	robotgo.ScrollDir(amount, dir)
	return nil
}

// GetMousePosition returns the current cursor position.
func GetMousePosition() (int, int) {
	return robotgo.Location()
}

// GetScreenSize returns the primary monitor dimensions.
func GetScreenSize() (int, int) {
	w, h := robotgo.GetScreenSize()
	return w, h
}

// MousePositionString formats the current position as a readable string.
func MousePositionString() string {
	x, y := GetMousePosition()
	w, h := GetScreenSize()
	return fmt.Sprintf("Mouse at (%d, %d) — Screen: %dx%d", x, y, w, h)
}
