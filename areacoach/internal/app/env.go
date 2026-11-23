package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"areacoach/internal/geom"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
	"areacoach/internal/platform"
)

// Env is what was found on the computer.
type Env struct {
	OsuDir    string
	Game      *osu.GameConfig
	SongsDir  string
	LazerDir  string
	LazerData string
	Lazer     *osu.LazerConfig
	OTDPath   string
	Profile   otd.Profile
	HasProf   bool
	Connected []string // tablets OpenTabletDriver sees now (nil: not asked)
	ScreenW   int
	ScreenH   int
	Problems  []string
}

// detect looks for osu!, osu!lazer and OpenTabletDriver.
func detect(s Settings) Env {
	var e Env
	e.ScreenW, e.ScreenH = platform.ScreenSize()
	e.OsuDir = s.OsuDir
	if e.OsuDir == "" {
		e.OsuDir = platform.FindOsuDir()
	}
	if e.OsuDir != "" {
		if _, err := os.Stat(e.OsuDir); err != nil {
			e.OsuDir = ""
		}
	}
	if e.OsuDir != "" {
		if p := osu.FindUserConfig(e.OsuDir); p != "" {
			if c, err := osu.ReadGameConfig(p); err == nil {
				e.Game = c
			}
		}
		e.SongsDir = filepath.Join(e.OsuDir, "Songs")
		if e.Game != nil {
			e.SongsDir = e.Game.SongsDir(e.OsuDir)
		}
	}
	e.LazerDir = s.LazerDir
	if e.LazerDir == "" {
		e.LazerDir = platform.LazerDefaultDir()
	}
	if e.LazerDir != "" {
		e.LazerData = osu.LazerDataDir(e.LazerDir)
	}
	if e.LazerData != "" {
		if c, err := osu.ReadLazerConfig(e.LazerDir, e.LazerData); err == nil {
			e.Lazer = c
		}
	}
	if e.OsuDir == "" && e.LazerData == "" {
		e.Problems = append(e.Problems, "I can't find osu! (neither stable nor lazer): set its folder in Settings.")
	}
	// OpenTabletDriver: the settings in use, from the program itself when
	// it runs, otherwise from its file
	e.OTDPath = s.OTDPath
	if e.OTDPath == "" {
		e.OTDPath = otd.Find(platform.ProcessDir("OpenTabletDriver.Daemon.exe"), platform.ProcessDir(platform.OTDWindowName()))
	}
	var st *otd.Settings
	if d, err := otd.DialDaemon(1500 * time.Millisecond); err == nil {
		if names, err := d.Tablets(); err == nil {
			e.Connected = names
		}
		if live, err := d.GetSettings(); err == nil {
			if b, err := json.Marshal(live); err == nil {
				st, _ = otd.Parse(b)
			}
		}
		d.Close()
	}
	if st == nil && e.OTDPath != "" {
		if f, err := otd.Load(e.OTDPath); err == nil {
			st = f
		} else {
			e.Problems = append(e.Problems, "I can't read the OpenTabletDriver settings: "+err.Error())
		}
	}
	if st == nil {
		e.Problems = append(e.Problems, "I can't find OpenTabletDriver: start it (version 0.6), or set its settings.json in Settings.")
		return e
	}
	pick := s.Tablet
	if pick == "" {
		for _, n := range e.Connected {
			if slices.ContainsFunc(st.Profiles, func(p otd.Profile) bool { return p.Tablet == n }) {
				pick = n
				break
			}
		}
	}
	if p, ok := st.Best(pick); ok {
		e.Profile, e.HasProf = p, true
	}
	switch {
	case !e.HasProf:
		e.Problems = append(e.Problems, "OpenTabletDriver has no tablet profile with an area.")
	case !e.Profile.Absolute:
		e.Problems = append(e.Problems, "OpenTabletDriver isn't in absolute mode: osu! needs the absolute output.")
	case !e.Profile.SizeKnown:
		e.Problems = append(e.Problems, fmt.Sprintf("I don't know the size of the %q tablet: the area is kept on it as best I can.", e.Profile.Tablet))
	}
	if e.Connected != nil && len(e.Connected) == 0 {
		e.Problems = append(e.Problems, "OpenTabletDriver sees no tablet: plug it in (and check no other driver is using it).")
	}
	if g := e.Game; g != nil && math.Abs(g.MouseSpeed-1) > 0.001 && g.MouseSpeed > 0 {
		e.Problems = append(e.Problems, fmt.Sprintf("The sensitivity in osu! is %.2f: with a tablet it should be 1.0.", g.MouseSpeed))
	}
	if c := e.Lazer; c != nil && c.TabletInput {
		e.Problems = append(e.Problems, "osu!lazer's own tablet support is on: turn it off (Settings → Input → Tablet) while OpenTabletDriver runs.")
	}
	return e
}

// setup is how osu! draws the playfield on the screen, from its settings
// (lazer: osu!lazer's), with OpenTabletDriver's area and display. When the
// osu! window is on screen and not full screen, its real place is used.
func (e *Env) setup(lazer bool, area otd.Rect) geom.Setup {
	su := geom.Setup{StretchX: 1, StretchY: 1, Area: area}
	if e.HasProf {
		su.TabletName = e.Profile.Tablet
		su.Display = e.Profile.Display
		su.TabletW, su.TabletH = e.Profile.TabletW, e.Profile.TabletH
	}
	sw, sh := e.ScreenW, e.ScreenH
	gw, gh := sw, sh
	full := true
	if lazer && e.Lazer != nil {
		c := e.Lazer
		switch c.WindowMode {
		case "Borderless":
		case "Fullscreen":
			if c.FullW > 0 && c.FullH > 0 {
				gw, gh = c.FullW, c.FullH
			}
		default:
			gw, gh, full = c.WinW, c.WinH, false
		}
	} else if !lazer && e.Game != nil {
		gw, gh = e.Game.GameResolution()
		full = e.Game.Fullscreen
	}
	if gw <= 0 || gh <= 0 {
		gw, gh = sw, sh
	}
	su.GameW, su.GameH = float64(gw), float64(gh)
	switch {
	case lazer && e.Lazer != nil:
		c := e.Lazer
		switch c.WindowMode {
		case "Fullscreen":
			if gw != sw || gh != sh {
				su.StretchX, su.StretchY = float64(sw)/float64(gw), float64(sh)/float64(gh)
			}
		case "Borderless":
		default:
			su.WinX = float64(sw-gw) * math.Max(-0.5, math.Min(1.5, c.PosX))
			su.WinY = float64(sh-gh) * math.Max(-0.5, math.Min(1.5, c.PosY))
		}
	case !lazer && e.Game != nil:
		g := e.Game
		switch {
		case g.Fullscreen && !g.Letterboxing:
			if gw != sw || gh != sh {
				su.StretchX, su.StretchY = float64(sw)/float64(gw), float64(sh)/float64(gh)
			}
		case g.Fullscreen && g.Letterboxing:
			su.WinX = float64(sw-gw) * (g.LetterboxX + 100) / 200
			su.WinY = float64(sh-gh) * (g.LetterboxY + 100) / 200
		default:
			su.WinX, su.WinY = float64(sw-gw)/2, float64(sh-gh)/2
		}
	}
	if w, ok := platform.OsuWindow(); ok && w.Lazer == lazer && !full {
		su.GameW, su.GameH, su.WinX, su.WinY = float64(w.W), float64(w.H), float64(w.X), float64(w.Y)
		su.StretchX, su.StretchY = 1, 1
	}
	if lazer && e.Lazer != nil {
		if w, h, x, y, ok := e.Lazer.Scaled(); ok {
			su.WinX += x * su.GameW * su.StretchX
			su.WinY += y * su.GameH * su.StretchY
			su.GameW *= w
			su.GameH *= h
		}
	}
	return su
}

// liveSetup is the setup while playing, from the osu! window as it is; and
// how to bring a cursor position into the coordinates of that setup (osu!
// in full screen at another resolution changes the screen's).
func (e *Env) liveSetup(w platform.Window, area otd.Rect) (geom.Setup, func(x, y int) (float64, float64)) {
	full := false
	if w.Lazer {
		full = e.Lazer != nil && e.Lazer.WindowMode == "Fullscreen"
	} else {
		full = e.Game != nil && e.Game.Fullscreen
	}
	if full {
		su := e.setup(w.Lazer, area)
		sx, sy := 1.0, 1.0
		if w.W > 0 && w.H > 0 && (w.W != e.ScreenW || w.H != e.ScreenH) {
			sx, sy = float64(e.ScreenW)/float64(w.W), float64(e.ScreenH)/float64(w.H)
		}
		return su, func(x, y int) (float64, float64) { return su.ScreenToOsu(float64(x)*sx, float64(y)*sy) }
	}
	su := e.setup(w.Lazer, area)
	su.GameW, su.GameH, su.WinX, su.WinY = float64(w.W), float64(w.H), float64(w.X), float64(w.Y)
	su.StretchX, su.StretchY = 1, 1
	if w.Lazer && e.Lazer != nil {
		if sw, sh, x, y, ok := e.Lazer.Scaled(); ok {
			su.WinX += x * su.GameW
			su.WinY += y * su.GameH
			su.GameW *= sw
			su.GameH *= sh
		}
	}
	return su, func(x, y int) (float64, float64) { return su.ScreenToOsu(float64(x), float64(y)) }
}
