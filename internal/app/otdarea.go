package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"areacoach/internal/otd"
	"areacoach/internal/platform"
)

// areaMark is an area and since when it was in use.
type areaMark struct {
	At   time.Time
	Area otd.Rect
}

// readArea reads the area in use: from OpenTabletDriver when it runs (what
// is really applied), otherwise from its settings file.
func readArea(tablet, path string) (otd.Rect, error) {
	if d, err := otd.DialDaemon(1500 * time.Millisecond); err == nil {
		s, err := d.GetSettings()
		d.Close()
		if err == nil {
			if r, ok := otd.AreaOf(s, tablet); ok {
				return r, nil
			}
		}
	}
	if path == "" {
		return otd.Rect{}, errors.New("OpenTabletDriver not found")
	}
	st, err := otd.Load(path)
	if err != nil {
		return otd.Rect{}, err
	}
	if p, ok := st.Best(tablet); ok {
		return p.Area, nil
	}
	return otd.Rect{}, errors.New("no tablet profile in the OpenTabletDriver settings")
}

// setLiveArea changes the area in OpenTabletDriver now (not saved).
func setLiveArea(tablet string, r otd.Rect) error {
	d, err := otd.DialDaemon(2 * time.Second)
	if err != nil {
		return err
	}
	defer d.Close()
	s, err := d.GetSettings()
	if err != nil {
		return err
	}
	ns, err := otd.WithArea(s, tablet, r)
	if err != nil {
		return err
	}
	if err := d.SetSettings(ns); err != nil {
		return err
	}
	_ = d.Resync()
	return nil
}

// saveArea writes the area in OpenTabletDriver's settings.json (keeping a
// copy of the original file the first time).
func saveArea(path, tablet string, r otd.Rect) error {
	if path == "" {
		return errors.New("I can't find the OpenTabletDriver settings file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s, err := otd.DecodeSettings(b)
	if err != nil {
		return errors.New("I can't read the OpenTabletDriver settings.json")
	}
	if _, ok := s["Profiles"]; !ok {
		return errors.New("this OpenTabletDriver settings.json is of an old version (0.5)")
	}
	ns, err := otd.WithArea(s, tablet, r)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path + ".areacoach-backup"); os.IsNotExist(err) {
		_ = os.WriteFile(path+".areacoach-backup", b, 0o644)
	}
	out, err := json.MarshalIndent(ns, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("I can't write the OpenTabletDriver settings: %w", err)
	}
	return nil
}

// putArea sets an area in OpenTabletDriver: now, and in its file when save
// is set (or when OpenTabletDriver does not run). note says what did not
// happen, when the area is set anyway.
func putArea(path, tablet string, r otd.Rect, save bool) (note string, err error) {
	live := setLiveArea(tablet, r)
	if !save && live == nil {
		return "", nil
	}
	if err := saveArea(path, tablet, r); err != nil {
		if live == nil {
			return "In use now, but not saved in OpenTabletDriver's file (" + err.Error() + "): press Save in OpenTabletDriver to keep it.", nil
		}
		return "", fmt.Errorf("%v (and OpenTabletDriver doesn't answer: %v)", err, live)
	}
	if live != nil {
		return "Saved in OpenTabletDriver's file, but OpenTabletDriver doesn't answer: it takes the new area when it starts again.", nil
	}
	return "", nil
}

// reopenOTD closes and opens again the OpenTabletDriver window, which
// otherwise keeps showing the values it read when it was opened.
func reopenOTD() error {
	was, err := platform.RestartProcess(platform.OTDWindowName())
	if err != nil {
		return fmt.Errorf("I can't reopen the OpenTabletDriver window: %w", err)
	}
	if !was {
		return errors.New("the OpenTabletDriver window isn't open: when you open it, it shows the new area")
	}
	return nil
}
