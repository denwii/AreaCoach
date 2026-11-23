//go:build !windows

// Package platform contains the operating-system specific helpers.
package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

// Init is a no-op outside Windows.
func Init() {}

// ScreenSize returns a configurable default resolution (for development).
func ScreenSize() (int, int) {
	w, _ := strconv.Atoi(os.Getenv("AREACOACH_SCREEN_W"))
	h, _ := strconv.Atoi(os.Getenv("AREACOACH_SCREEN_H"))
	if w == 0 || h == 0 {
		return 1920, 1080
	}
	return w, h
}

// FindOsuDir uses OSU_DIR during development.
func FindOsuDir() string { return os.Getenv("OSU_DIR") }

// ProcessDir is not implemented outside Windows.
func ProcessDir(name string) string { return "" }

// IsRunning is not implemented outside Windows.
func IsRunning(name string) bool { return false }

// Open opens a file with the desktop default handler.
func Open(target string) error { return exec.Command("xdg-open", target).Start() }

// Reveal opens the containing folder.
func Reveal(path string) error { return Open(filepath.Dir(path)) }

// MessageBox prints to stderr.
func MessageBox(title, text string, warning bool) { fmt.Fprintln(os.Stderr, title+": "+text) }

// SystemLightTheme is not implemented outside Windows.
func SystemLightTheme() bool { return false }

// OTDWindowName is the program name of the OpenTabletDriver window.
func OTDWindowName() string { return "OpenTabletDriver.UX" }

// RestartProcess does nothing outside Windows.
func RestartProcess(name string) (bool, error) { return false, nil }

// OsuWindow finds nothing outside Windows.
func OsuWindow() (Window, bool) { return Window{}, false }

// CursorPos is not available outside Windows.
func CursorPos() (int, int) { return 0, 0 }

// KeyDown is not available outside Windows.
func KeyDown(vk int) bool { return false }

// FineTimer does nothing outside Windows.
func FineTimer(on bool) {}

// PlayWav does nothing outside Windows.
func PlayWav(b []byte) {}
