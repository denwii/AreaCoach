package osu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ulikunitz/xz/lzma"
)

// LazerVersion is the first replay version written by osu!lazer: from it on
// a replay ends with lazer's own data (its mods, with their settings).
const LazerVersion = 30000000

// LazerMod is a mod as osu!lazer saves it: its short name and the settings
// changed from the default.
type LazerMod struct {
	Acronym  string                     `json:"acronym"`
	Settings map[string]json.RawMessage `json:"settings,omitempty"`
}

func (m LazerMod) num(key string) (float64, bool) {
	raw, ok := m.Settings[key]
	if !ok {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

// parseLazerInfo reads lazer's data at the end of a replay: LZMA-compressed
// JSON (the same way as the frames).
func parseLazerInfo(b []byte) []LazerMod {
	zr, err := lzma.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(zr, 4<<20))
	var info struct {
		Mods []LazerMod `json:"mods"`
	}
	if json.Unmarshal(raw, &info) != nil {
		return nil
	}
	return info.Mods
}

// Adjust is what lazer's mod settings change in a map: the speed (DT and HT
// can have any speed) and the difficulty set by hand (Difficulty Adjust).
// Zero values: as the mods say.
type Adjust struct {
	Rate       float64
	CS, AR, OD float64
	HasCS      bool
	HasAR      bool
	HasOD      bool
}

// lazerMoved are the mods of osu!lazer that move the circles, change their
// size while they are played or change the speed while the map plays: the
// aim measured would not be the one of the map.
var lazerMoved = map[string]string{
	"MR": "Mirror", "RD": "Random", "TP": "Target Practice", "DF": "Deflate", "GR": "Grow",
	"WG": "Wiggle", "TR": "Transform", "MG": "Magnetised", "RP": "Repel", "DP": "Depth",
	"BR": "Barrel Roll", "WU": "Wind Up", "WD": "Wind Down", "AS": "Adaptive Speed",
	"AP": "Autopilot", "AT": "Autoplay", "CN": "Cinema",
}

// LazerAdjust is what the lazer mods of a replay change, and the name of a
// mod that makes it unusable ("" if none).
func (h *ReplayHeader) LazerAdjust() (Adjust, string) {
	var a Adjust
	for _, m := range h.LazerMods {
		ac := strings.ToUpper(m.Acronym)
		if name, ok := lazerMoved[ac]; ok {
			return a, name
		}
		switch ac {
		case "DT", "NC":
			a.Rate = 1.5
			if f, ok := m.num("speed_change"); ok && f > 0 {
				a.Rate = f
			}
		case "HT", "DC":
			a.Rate = 0.75
			if f, ok := m.num("speed_change"); ok && f > 0 {
				a.Rate = f
			}
		case "DA":
			if f, ok := m.num("circle_size"); ok {
				a.CS, a.HasCS = f, true
			}
			if f, ok := m.num("approach_rate"); ok {
				a.AR, a.HasAR = f, true
			}
			if f, ok := m.num("overall_difficulty"); ok {
				a.OD, a.HasOD = f, true
			}
		}
	}
	return a, ""
}

// LazerModString is the mods of a lazer replay in short form, with the speed
// when it is not the usual one ("DT 1.3×").
func (h *ReplayHeader) LazerModString() string {
	var parts []string
	for _, m := range h.LazerMods {
		ac := strings.ToUpper(m.Acronym)
		if ac == "CL" {
			continue
		}
		s := ac
		if f, ok := m.num("speed_change"); ok {
			def := 1.5
			if ac == "HT" || ac == "DC" {
				def = 0.75
			}
			if math.Abs(f-def) > 1e-6 {
				s += " " + strconv.FormatFloat(f, 'f', -1, 64) + "×"
			}
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// LazerDataDir finds osu!lazer's data folder from its default place: there,
// storage.ini names the folder the data was moved to, if it was.
func LazerDataDir(def string) string {
	if def == "" {
		return ""
	}
	if b, err := os.ReadFile(filepath.Join(def, "storage.ini")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(k) == "FullPath" {
				if p := strings.TrimSpace(v); p != "" && isDir(filepath.Join(p, "files")) {
					return p
				}
			}
		}
	}
	if isDir(filepath.Join(def, "files")) {
		return def
	}
	return ""
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// LazerConfig is what osu!lazer's settings say about its window and its
// input.
type LazerConfig struct {
	Path       string  `json:"path"`
	Username   string  `json:"username,omitempty"`
	WindowMode string  `json:"windowMode"` // Windowed, Borderless, Fullscreen
	FullW      int     `json:"fullW,omitempty"`
	FullH      int     `json:"fullH,omitempty"`
	WinW       int     `json:"winW"`
	WinH       int     `json:"winH"`
	PosX       float64 `json:"posX"` // where the window is: 0 left, 1 right
	PosY       float64 `json:"posY"`
	// "Map absolute input to window"
	MapToWindow bool `json:"mapToWindow,omitempty"`
	// the screen scaling of the game (Off, Everything, ExcludeOverlays,
	// Gameplay) and the part of the window it keeps
	Scaling string  `json:"scaling,omitempty"`
	ScaleW  float64 `json:"scaleW,omitempty"`
	ScaleH  float64 `json:"scaleH,omitempty"`
	ScaleX  float64 `json:"scaleX,omitempty"`
	ScaleY  float64 `json:"scaleY,omitempty"`
	// osu!lazer's own tablet support is on
	TabletInput bool `json:"tabletInput,omitempty"`
}

func readIni(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func parseSize(s string) (int, int, bool) {
	a, b, ok := strings.Cut(s, "x")
	if !ok {
		return 0, 0, false
	}
	w, e1 := strconv.Atoi(strings.TrimSpace(a))
	h, e2 := strconv.Atoi(strings.TrimSpace(b))
	return w, h, e1 == nil && e2 == nil && w > 0 && h > 0
}

func parseF(s string, def float64) float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(f) {
		return f
	}
	return def
}

// ReadLazerConfig reads osu!lazer's settings: framework.ini and input.json
// stay in the default folder, game.ini moves with the data.
func ReadLazerConfig(defDir, dataDir string) (*LazerConfig, error) {
	fw := readIni(filepath.Join(defDir, "framework.ini"))
	if fw == nil {
		return nil, fmt.Errorf("framework.ini not found in %s", defDir)
	}
	c := &LazerConfig{Path: filepath.Join(defDir, "framework.ini"), WindowMode: "Windowed", WinW: 1366, WinH: 768, PosX: 0.5, PosY: 0.5}
	if v := fw["WindowMode"]; v != "" {
		c.WindowMode = v
	}
	if w, h, ok := parseSize(fw["SizeFullscreen"]); ok && w < 9999 && h < 9999 {
		c.FullW, c.FullH = w, h
	}
	if w, h, ok := parseSize(fw["WindowedSize"]); ok {
		c.WinW, c.WinH = w, h
	}
	c.PosX = parseF(fw["WindowedPositionX"], 0.5)
	c.PosY = parseF(fw["WindowedPositionY"], 0.5)
	c.MapToWindow = strings.EqualFold(fw["MapAbsoluteInputToWindow"], "true")
	game := readIni(filepath.Join(dataDir, "game.ini"))
	if game == nil && dataDir != defDir {
		game = readIni(filepath.Join(defDir, "game.ini"))
	}
	if game != nil {
		c.Username = game["Username"]
		c.Scaling = game["Scaling"]
		c.ScaleW = parseF(game["ScalingSizeX"], 0.8)
		c.ScaleH = parseF(game["ScalingSizeY"], 0.8)
		c.ScaleX = parseF(game["ScalingPositionX"], 0.5)
		c.ScaleY = parseF(game["ScalingPositionY"], 0.5)
	}
	if b, err := os.ReadFile(filepath.Join(defDir, "input.json")); err == nil {
		c.TabletInput = lazerTabletOn(b)
	}
	return c, nil
}

// lazerTabletOn tells whether osu!lazer's own tablet support is on in
// input.json (on by default when the file does not say).
func lazerTabletOn(b []byte) bool {
	var in struct {
		InputHandlers []map[string]json.RawMessage `json:"InputHandlers"`
	}
	if json.Unmarshal(b, &in) != nil {
		return false
	}
	for _, h := range in.InputHandlers {
		var typ string
		_ = json.Unmarshal(h["$type"], &typ)
		if !strings.Contains(typ, "OpenTabletDriverHandler") {
			continue
		}
		var on bool
		if raw, ok := h["Enabled"]; ok && json.Unmarshal(raw, &on) == nil {
			return on
		}
		return true
	}
	return false
}

// Scaled is the part of the window the game is drawn in, when its screen
// scaling is on: the share of the window it takes and where.
func (c *LazerConfig) Scaled() (w, h, x, y float64, ok bool) {
	switch c.Scaling {
	case "Everything", "ExcludeOverlays", "Gameplay":
	default:
		return 1, 1, 0, 0, false
	}
	w, h = clampF(c.ScaleW, 0.2, 1), clampF(c.ScaleH, 0.2, 1)
	if w >= 1 && h >= 1 {
		return 1, 1, 0, 0, false
	}
	return w, h, clampF(c.ScaleX, 0, 1) * (1 - w), clampF(c.ScaleY, 0, 1) * (1 - h), true
}

func clampF(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
