package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Settings are the player's choices.
type Settings struct {
	OsuDir   string `json:"osuDir"`
	LazerDir string `json:"lazerDir"`
	OTDPath  string `json:"otdPath"`
	Tablet   string `json:"tablet"` // OpenTabletDriver profile ("": the connected tablet)
	// height / width of the area (0: the one of the area in use)
	Ratio float64 `json:"ratio"`
	// the widths allowed (0: no limit)
	MinW float64 `json:"minW"`
	MaxW float64 `json:"maxW"`
	// live: the keys that click ("": osu!'s), and whether the pen tip does
	// ("auto": as osu! says, "on", "off")
	Keys   string `json:"keys"`
	PenTip string `json:"penTip"`
	Sound  bool   `json:"sound"`
	// live: how big and how often each change is: "gentle", "normal",
	// "strong"
	Strength string `json:"strength"`
	// live: it may play a stretch at a nearby width to measure it, while it
	// isn't sure of the best one yet
	TryWidths bool `json:"tryWidths"`
}

func defaultSettings() Settings {
	return Settings{PenTip: "auto", Sound: true, Strength: "normal", TryWidths: true}
}

const settingsFile = "settings-v3.json"

func loadSettings(dir string) Settings {
	s := defaultSettings()
	b, err := os.ReadFile(filepath.Join(dir, settingsFile))
	if err != nil {
		importOld(dir, &s)
		return s
	}
	_ = json.Unmarshal(b, &s)
	s.fix()
	return s
}

// importOld takes the folders, the tablet, the width range and the fixed
// shape from the settings of version 2, the first time.
func importOld(dir string, s *Settings) {
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return
	}
	var old struct {
		Settings struct {
			OsuDir          string  `json:"osuDir"`
			LazerDir        string  `json:"lazerDir"`
			OTDPath         string  `json:"otdPath"`
			PreferredTablet string  `json:"preferredTablet"`
			DiagMinW        float64 `json:"diagMinW"`
			DiagMaxW        float64 `json:"diagMaxW"`
		} `json:"settings"`
		Calib *struct {
			Locked bool    `json:"locked"`
			Ratio  float64 `json:"ratio"`
		} `json:"calib"`
	}
	if json.Unmarshal(b, &old) != nil {
		return
	}
	o := old.Settings
	s.OsuDir, s.LazerDir, s.OTDPath, s.Tablet = o.OsuDir, o.LazerDir, o.OTDPath, o.PreferredTablet
	s.MinW, s.MaxW = o.DiagMinW, o.DiagMaxW
	if c := old.Calib; c != nil && c.Locked && c.Ratio > 0.2 && c.Ratio < 2 {
		s.Ratio = c.Ratio
	}
	s.fix()
}

func (s *Settings) fix() {
	if s.Ratio < 0.2 || s.Ratio > 2 {
		s.Ratio = 0
	}
	if s.MinW < 0 {
		s.MinW = 0
	}
	if s.MaxW < 0 {
		s.MaxW = 0
	}
	if s.MinW > 0 && s.MaxW > 0 && s.MinW > s.MaxW {
		s.MinW, s.MaxW = s.MaxW, s.MinW
	}
	switch s.PenTip {
	case "auto", "on", "off":
	default:
		s.PenTip = "auto"
	}
	switch s.Strength {
	case "gentle", "normal", "strong":
	default:
		s.Strength = "normal"
	}
	s.Keys = strings.Join(strings.FieldsFunc(s.Keys, func(r rune) bool { return r == ' ' || r == ',' || r == ';' }), " ")
}

func saveSettings(dir string, s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, settingsFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, settingsFile))
}
