package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// LazerDefaultDir is the folder where osu!lazer keeps its settings (and its
// data, unless it was moved: storage.ini there says where). LAZER_DIR
// overrides it.
func LazerDefaultDir() string {
	if d := os.Getenv("LAZER_DIR"); d != "" {
		return d
	}
	switch runtime.GOOS {
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "osu")
		}
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Application Support", "osu")
		}
	default:
		if x := os.Getenv("XDG_DATA_HOME"); x != "" {
			return filepath.Join(x, "osu")
		}
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, ".local", "share", "osu")
		}
	}
	return ""
}
