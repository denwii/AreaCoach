package osu

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// GameConfig holds the osu! user settings relevant to cursor mapping.
type GameConfig struct {
	Path             string  `json:"path"`
	Username         string  `json:"username"`
	Width            int     `json:"width"`
	Height           int     `json:"height"`
	WidthFullscreen  int     `json:"widthFullscreen"`
	HeightFullscreen int     `json:"heightFullscreen"`
	Fullscreen       bool    `json:"fullscreen"`
	Letterboxing     bool    `json:"letterboxing"`
	LetterboxX       float64 `json:"letterboxX"`
	LetterboxY       float64 `json:"letterboxY"`
	MouseSpeed       float64 `json:"mouseSpeed"`
	RawInput         bool    `json:"rawInput"`
	AbsoluteToWindow bool    `json:"absoluteToWindow"`
	BeatmapDirectory string  `json:"beatmapDirectory"`
	KeyLeft          string  `json:"keyLeft"`
	MouseButtonsOff  bool    `json:"mouseButtonsOff"` // mouse buttons do not click in play
	KeyRight         string  `json:"keyRight"`
}

// FindUserConfig returns the osu!.<user>.cfg in the install folder (the
// most recently modified one if several users exist).
func FindUserConfig(osuDir string) string {
	matches, _ := filepath.Glob(filepath.Join(osuDir, "osu!.*.cfg"))
	type fi struct {
		p string
		t int64
	}
	var list []fi
	for _, m := range matches {
		base := strings.ToLower(filepath.Base(m))
		if base == "osu!.cfg" {
			continue
		}
		st, err := os.Stat(m)
		if err != nil {
			continue
		}
		list = append(list, fi{m, st.ModTime().UnixNano()})
	}
	if len(list) == 0 {
		return ""
	}
	sort.Slice(list, func(i, j int) bool { return list[i].t > list[j].t })
	return list[0].p
}

// ReadGameConfig parses osu!.<user>.cfg.
func ReadGameConfig(path string) (*GameConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	c := &GameConfig{Path: path, MouseSpeed: 1, BeatmapDirectory: "Songs"}
	base := filepath.Base(path)
	if strings.HasPrefix(base, "osu!.") && strings.HasSuffix(base, ".cfg") {
		c.Username = strings.TrimSuffix(strings.TrimPrefix(base, "osu!."), ".cfg")
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		i, _ := strconv.Atoi(v)
		fl, _ := strconv.ParseFloat(v, 64)
		switch k {
		case "Username":
			if v != "" {
				c.Username = v
			}
		case "Width":
			c.Width = i
		case "Height":
			c.Height = i
		case "WidthFullscreen":
			c.WidthFullscreen = i
		case "HeightFullscreen":
			c.HeightFullscreen = i
		case "Fullscreen":
			c.Fullscreen = v == "1"
		case "Letterboxing":
			c.Letterboxing = v == "1"
		case "LetterboxPositionX":
			c.LetterboxX = fl
		case "LetterboxPositionY":
			c.LetterboxY = fl
		case "MouseSpeed":
			if fl > 0 {
				c.MouseSpeed = fl
			}
		case "RawInput":
			c.RawInput = v == "1"
		case "MouseDisableButtons":
			c.MouseButtonsOff = v == "1"
		case "AbsoluteToOsuWindow":
			c.AbsoluteToWindow = v == "1"
		case "BeatmapDirectory":
			if v != "" {
				c.BeatmapDirectory = v
			}
		case "keyOsuLeft":
			c.KeyLeft = v
		case "keyOsuRight":
			c.KeyRight = v
		}
	}
	return c, nil
}

// GameResolution returns the resolution osu! renders at.
func (c *GameConfig) GameResolution() (w, h int) {
	if c.Fullscreen && c.WidthFullscreen > 0 && c.HeightFullscreen > 0 {
		return c.WidthFullscreen, c.HeightFullscreen
	}
	return c.Width, c.Height
}

// SongsDir resolves the beatmap directory.
func (c *GameConfig) SongsDir(osuDir string) string {
	d := c.BeatmapDirectory
	if d == "" {
		d = "Songs"
	}
	if filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(osuDir, d)
}
