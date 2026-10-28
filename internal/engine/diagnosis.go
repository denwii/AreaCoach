package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/otd"
	"areacoach/internal/store"
)

// DiagStatus is what the Diagnosis page shows.
type DiagStatus struct {
	Exists    bool         `json:"exists"`
	Active    bool         `json:"active"`
	LiveOK    bool         `json:"liveOk"`  // OpenTabletDriver answers now
	LiveErr   string       `json:"liveErr"` // why it does not
	Started   time.Time    `json:"started"`
	Base      otd.Rect     `json:"base"`
	Mode      string       `json:"mode"`
	Adapt     *AdaptStatus `json:"adapt"`
	StartLock otd.Rect     `json:"startLock"` // where a new diagnosis would start, forced proportion on
	StartFree otd.Rect     `json:"startFree"` // and off
}

func sameArea(a, b otd.Rect) bool {
	return math.Abs(a.W-b.W) < 0.3 && math.Abs(a.H-b.H) < 0.3 && math.Hypot(a.X-b.X, a.Y-b.Y) < 0.5 && math.Abs(a.Rot-b.Rot) < 0.2
}

// daemon connects to OpenTabletDriver for live changes.
func (e *Engine) daemon() (*otd.Daemon, map[string]any, error) {
	d, err := otd.DialDaemon(2 * time.Second)
	if err != nil {
		return nil, nil, err
	}
	s, err := d.GetSettings()
	if err != nil {
		d.Close()
		return nil, nil, err
	}
	return d, s, nil
}

// setLiveArea applies an area through the daemon, without saving it.
func (e *Engine) setLiveArea(r otd.Rect) error {
	d, s, err := e.daemon()
	if err != nil {
		return err
	}
	defer d.Close()
	ns, err := otd.WithArea(s, e.Env().Setup.TabletName, r)
	if err != nil {
		return err
	}
	if err := d.SetSettings(ns); err != nil {
		return err
	}
	_ = d.Resync()
	return nil
}

// diagResume is called at start: a diagnosis left running is paused, with
// the area found so far kept; sessions of older versions are dropped.
func (e *Engine) diagResume() {
	c := e.st.State().Calib
	switch {
	case c == nil:
		return
	case c.Mode != diagMode:
		e.st.Update(func(s *store.State) { s.Calib = nil })
	case c.Active:
		e.st.Update(func(s *store.State) { s.Calib.Active = false })
	default:
		return
	}
	_ = e.st.Save()
}

// CalibAction drives the diagnosis.
func (e *Engine) CalibAction(action string) error {
	c := e.st.State().Calib
	if c == nil {
		return errors.New("no diagnosis in progress")
	}
	return e.adaptAction(c, action)
}

// applyArea sets an area for good: live and in settings.json (with a
// backup of the original file the first time).
func (e *Engine) applyArea(r otd.Rect) error {
	live := e.setLiveArea(r) == nil
	if err := e.writeOTDArea(r); err != nil {
		if live && errors.Is(err, errNoFile) {
			return nil
		}
		return err
	}
	e.st.Update(func(s *store.State) { c := r; s.LastCompromise = &c })
	return nil
}

var errNoFile = errors.New("cannot find the OpenTabletDriver settings file")

// writeOTDArea saves an area in settings.json of OpenTabletDriver (with a
// backup of the original file the first time).
func (e *Engine) writeOTDArea(r otd.Rect) error {
	env := e.Env()
	path := env.OTDPath
	if path == "" {
		return errNoFile
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s, err := otd.DecodeSettings(b)
	if err != nil {
		return errors.New("cannot read the OpenTabletDriver settings.json")
	}
	if _, ok := s["Profiles"]; !ok {
		return nil // older layout: applied live only
	}
	ns, err := otd.WithArea(s, env.Setup.TabletName, r)
	if err != nil {
		return err
	}
	return writeSettingsFile(path, b, ns)
}

// writeSettingsFile saves new settings of OpenTabletDriver over the file
// that held orig (keeping a copy of the original the first time).
func writeSettingsFile(path string, orig []byte, ns map[string]any) error {
	if _, err := os.Stat(path + ".areacoach-backup"); os.IsNotExist(err) {
		_ = os.WriteFile(path+".areacoach-backup", orig, 0o644)
	}
	out, err := json.MarshalIndent(ns, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("cannot write the settings: %w", err)
	}
	return nil
}

// diagStep handles the new replays for the diagnosis.
func (e *Engine) diagStep() {
	c := e.st.State().Calib
	if c == nil || c.Mode != diagMode {
		return
	}
	if e.adaptManualChange(c) {
		return
	}
	if e.adaptProcess() {
		_ = e.st.Save()
		e.emit("calib")
	}
	e.adaptFilterKick()
}

// CalibStatus returns the state of the diagnosis.
func (e *Engine) CalibStatus() DiagStatus {
	out := DiagStatus{}
	env := e.Env()
	if env.SetupOK {
		ratio := env.Setup.Area.H / env.Setup.Area.W
		if env.Setup.Display.W > 0 {
			ratio = env.Setup.Display.H / env.Setup.Display.W
		}
		set := e.st.State().Settings
		o := analysis.AdaptOpts{Locked: true, Ratio: ratio, TabletW: env.Setup.TabletW, TabletH: env.Setup.TabletH, Anchor: env.Setup.Area,
			MinW: set.DiagMinW, MaxW: set.DiagMaxW}
		out.StartLock = analysis.AdaptStart(o)
		o.Locked = false
		out.StartFree = analysis.AdaptStart(o)
	}
	out.LiveOK, out.LiveErr = e.liveProbe()
	if c := e.st.State().Calib; c != nil && c.Mode == diagMode {
		out.Exists, out.Mode = true, "adapt"
		out.Active, out.Started, out.Base = c.Active, c.Started, c.Base
		out.Adapt = e.adaptStatus(c)
	}
	return out
}

// liveProbe tells whether OpenTabletDriver answers, checked at most every
// few seconds.
func (e *Engine) liveProbe() (bool, string) {
	e.cmu.Lock()
	if time.Since(e.probeAt) < 8*time.Second {
		ok, msg := e.probeOK, e.probeErr
		e.cmu.Unlock()
		return ok, msg
	}
	e.cmu.Unlock()
	ok, msg := true, ""
	if d, err := otd.DialDaemon(800 * time.Millisecond); err == nil {
		d.Close()
	} else {
		ok, msg = false, err.Error()
	}
	e.cmu.Lock()
	e.probeAt, e.probeOK, e.probeErr = time.Now(), ok, msg
	e.cmu.Unlock()
	return ok, msg
}

// Shutdown is called when the window closes: a diagnosis that is changing
// the area live is paused, with the area found so far kept.
func (e *Engine) Shutdown() {
	if c := e.st.State().Calib; c != nil && c.Active {
		_ = e.CalibAction("pause")
	}
}

// SwitchProfile changes the grip profile: a diagnosis in progress is paused
// first, since its plays belong to the profile that started it.
func (e *Engine) SwitchProfile(id int) error {
	if c := e.st.State().Calib; c != nil && c.Active {
		_ = e.CalibAction("pause")
	}
	if err := e.st.SwitchProfile(id, time.Now()); err != nil {
		return err
	}
	e.fmu.Lock()
	e.fstate = FilterState{}
	e.fkey = ""
	e.fmu.Unlock()
	e.emit("status")
	return nil
}

// ApplyArea sets an area in OpenTabletDriver (live and saved).
func (e *Engine) ApplyArea(r otd.Rect) error {
	env := e.Env()
	if !r.Valid() || r.W < 20 || (env.Setup.TabletW > 0 && !analysis.FitsTablet(r, env.Setup.TabletW, env.Setup.TabletH)) {
		return errors.New("this area does not fit the tablet")
	}
	if err := e.applyArea(r); err != nil {
		return err
	}
	_ = e.st.Save()
	e.invalidate()
	e.Wake()
	return nil
}
