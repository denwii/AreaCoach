// Package otd reads OpenTabletDriver settings and knows tablet sizes.
package otd

import (
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed tablets.json
var tabletsJSON []byte

var tablets map[string][2]float64

func init() {
	_ = json.Unmarshal(tabletsJSON, &tablets)
}

// TabletSize returns the physical active area (mm) of a tablet model.
func TabletSize(name string) (w, h float64, ok bool) {
	if s, found := tablets[name]; found {
		return s[0], s[1], true
	}
	ln := strings.ToLower(strings.TrimSpace(name))
	for k, s := range tablets {
		if strings.ToLower(k) == ln {
			return s[0], s[1], true
		}
	}
	return 0, 0, false
}

// TabletNames lists all known models (for manual selection).
func TabletNames() []string {
	out := make([]string, 0, len(tablets))
	for k := range tablets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Rect is an OTD area: size and center position, rotation in degrees.
type Rect struct {
	W   float64 `json:"w"`
	H   float64 `json:"h"`
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	Rot float64 `json:"rot"`
}

// Valid reports whether the rectangle has a usable size.
func (r Rect) Valid() bool { return r.W > 0.5 && r.H > 0.5 && !math.IsNaN(r.X) && !math.IsNaN(r.Y) }

// Same compares two areas with a small tolerance.
func (r Rect) Same(o Rect) bool {
	const e = 0.005
	return math.Abs(r.W-o.W) < e && math.Abs(r.H-o.H) < e && math.Abs(r.X-o.X) < e &&
		math.Abs(r.Y-o.Y) < e && math.Abs(r.Rot-o.Rot) < 0.01
}

// Profile is one tablet profile of the OTD settings.
type Profile struct {
	Tablet     string  `json:"tablet"`
	OutputMode string  `json:"outputMode"`
	Absolute   bool    `json:"absolute"`
	Area       Rect    `json:"area"`
	Display    Rect    `json:"display"`
	Clipping   bool    `json:"clipping"`
	TabletW    float64 `json:"tabletW"`
	TabletH    float64 `json:"tabletH"`
	SizeKnown  bool    `json:"sizeKnown"`
}

// Settings is the parsed settings.json.
type Settings struct {
	Path     string    `json:"path"`
	ModTime  time.Time `json:"modTime"`
	Profiles []Profile `json:"profiles"`
}

// CandidatePaths returns where settings.json usually lives on Windows.
func CandidatePaths(extraDirs ...string) []string {
	var out []string
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		out = append(out, filepath.Join(la, "OpenTabletDriver", "settings.json"))
	}
	for _, d := range extraDirs {
		if d == "" {
			continue
		}
		out = append(out, filepath.Join(d, "userdata", "settings.json"), filepath.Join(d, "settings.json"))
	}
	if ad := os.Getenv("APPDATA"); ad != "" {
		out = append(out, filepath.Join(ad, "OpenTabletDriver", "settings.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".config", "OpenTabletDriver", "settings.json"))
	}
	return out
}

// Find returns the first existing settings file among the candidates.
func Find(extraDirs ...string) string {
	for _, p := range CandidatePaths(extraDirs...) {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Load parses a settings.json (OpenTabletDriver 0.5 and 0.6+ formats).
func Load(path string) (*Settings, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	st, _ := os.Stat(path)
	s, err := Parse(b)
	if err != nil {
		return nil, err
	}
	s.Path = path
	if st != nil {
		s.ModTime = st.ModTime()
	}
	return s, nil
}

// Parse decodes settings content.
func Parse(b []byte) (*Settings, error) {
	b = []byte(strings.TrimPrefix(string(b), "\ufeff"))
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, errors.New("unreadable OpenTabletDriver settings.json")
	}
	s := &Settings{}
	if profs, ok := root["Profiles"].([]any); ok {
		for _, p := range profs {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			prof := Profile{Tablet: str(pm["Tablet"])}
			if om, ok := pm["OutputMode"].(map[string]any); ok {
				prof.OutputMode = str(om["Path"])
			}
			if am, ok := pm["AbsoluteModeSettings"].(map[string]any); ok {
				prof.Display = rect(am["Display"])
				prof.Area = rect(am["Tablet"])
				prof.Clipping = boolean(am["EnableClipping"], true)
			}
			prof.Absolute = isAbsolute(prof.OutputMode)
			finish(&prof)
			s.Profiles = append(s.Profiles, prof)
		}
	} else {
		// 0.5.x flat layout
		prof := Profile{Tablet: str(root["TabletName"])}
		if om, ok := root["OutputMode"].(map[string]any); ok {
			prof.OutputMode = str(om["Path"])
		} else {
			prof.OutputMode = str(root["OutputMode"])
		}
		prof.Display = Rect{W: num(root["DisplayWidth"]), H: num(root["DisplayHeight"]),
			X: numAny(root, "DisplayX", "DisplayXOffset"), Y: numAny(root, "DisplayY", "DisplayYOffset")}
		prof.Area = Rect{W: num(root["TabletWidth"]), H: num(root["TabletHeight"]),
			X: numAny(root, "TabletX", "TabletXOffset"), Y: numAny(root, "TabletY", "TabletYOffset"),
			Rot: numAny(root, "TabletRotation", "Rotation")}
		prof.Clipping = boolean(root["EnableClipping"], true)
		prof.Absolute = isAbsolute(prof.OutputMode)
		finish(&prof)
		if prof.Area.Valid() {
			s.Profiles = append(s.Profiles, prof)
		}
	}
	if len(s.Profiles) == 0 {
		return nil, errors.New("no tablet profile found in the OpenTabletDriver settings")
	}
	return s, nil
}

func finish(p *Profile) {
	if w, h, ok := TabletSize(p.Tablet); ok {
		p.TabletW, p.TabletH, p.SizeKnown = w, h, true
	}
}

func isAbsolute(path string) bool {
	l := strings.ToLower(path)
	if l == "" {
		return true
	}
	return strings.Contains(l, "absolute") || strings.Contains(l, "ink") || strings.Contains(l, "artist")
}

// Best picks the profile to use: preferred tablet name first, then the
// first absolute profile with a valid area.
func (s *Settings) Best(preferred string) (Profile, bool) {
	if preferred != "" {
		for _, p := range s.Profiles {
			if p.Tablet == preferred && p.Area.Valid() {
				return p, true
			}
		}
	}
	for _, p := range s.Profiles {
		if p.Absolute && p.Area.Valid() && p.Display.Valid() {
			return p, true
		}
	}
	for _, p := range s.Profiles {
		if p.Area.Valid() {
			return p, true
		}
	}
	return Profile{}, false
}

func rect(v any) Rect {
	m, ok := v.(map[string]any)
	if !ok {
		return Rect{}
	}
	return Rect{W: num(m["Width"]), H: num(m["Height"]), X: num(m["X"]), Y: num(m["Y"]), Rot: num(m["Rotation"])}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		var f float64
		_ = json.Unmarshal([]byte(x), &f)
		return f
	}
	return 0
}

func numAny(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return num(v)
		}
	}
	return 0
}

func boolean(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}
