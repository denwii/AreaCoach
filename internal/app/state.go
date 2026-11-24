package app

import (
	"areacoach/internal/calib"
	"areacoach/internal/otd"
)

// State is everything the interface shows.
type State struct {
	Version   string    `json:"version"`
	Settings  Settings  `json:"settings"`
	Area      *otd.Rect `json:"area"`
	AreaSrc   string    `json:"areaSrc"`
	AreaErr   string    `json:"areaErr,omitempty"`
	Undo      *otd.Rect `json:"undo,omitempty"`
	Note      string    `json:"note,omitempty"` // about the last area set
	Ratio     float64   `json:"ratio"`          // height / width the advice keeps
	Tablet    string    `json:"tablet"`
	TabletW   float64   `json:"tabletW"`
	TabletH   float64   `json:"tabletH"`
	Display   otd.Rect  `json:"display"`
	Profiles  []string  `json:"profiles"`
	OsuDir    string    `json:"osuDir"`
	LazerData string    `json:"lazerData"`
	OTDPath   string    `json:"otdPath"`
	Problems  []string  `json:"problems"`
	Report    *Report   `json:"report"`
	ReplayMsg string    `json:"replayMsg,omitempty"`
	Busy      bool      `json:"busy"`
	LazerScan float64   `json:"lazerScan"` // how far the first look at osu!lazer's files got (0-1)
	Live      LiveState `json:"live"`
	// what the calibration remembers of the widths, for the area in use
	Memory *calib.Report `json:"memory,omitempty"`
	// the area that went best so far, and how sure that is (0-1)
	Best        *otd.Rect `json:"best,omitempty"`
	Reliability float64   `json:"reliability"`
}

// State returns the state for the interface.
func (a *App) State() State {
	live := a.live.state()
	var mem *calib.Report
	if r, ok := a.current(); ok {
		m := a.memReport(r)
		mem = &m
	}
	best, rel, hasBest := a.BestArea()
	a.mu.Lock()
	defer a.mu.Unlock()
	s := State{Version: a.version, Settings: a.set, AreaSrc: a.areaSrc, AreaErr: a.areaErr, OsuDir: a.env.OsuDir,
		LazerData: a.env.LazerData, OTDPath: a.env.OTDPath, Problems: append([]string{}, a.env.Problems...),
		Report: a.report, ReplayMsg: a.replayMsg, Note: a.note, Busy: a.busy, Live: live, LazerScan: 1}
	if a.areaOK {
		r := a.area
		s.Area = &r
		s.Ratio = a.set.Ratio
		if s.Ratio <= 0 && r.W > 0 {
			s.Ratio = r.H / r.W
		}
	}
	if a.prev != nil {
		p := *a.prev
		s.Undo = &p
	}
	if a.env.HasProf {
		p := a.env.Profile
		s.Tablet, s.Display = p.Tablet, p.Display
		if p.SizeKnown {
			s.TabletW, s.TabletH = p.TabletW, p.TabletH
		}
	}
	s.Profiles = append(s.Profiles, a.env.Connected...)
	if a.lazer != nil {
		_, _, s.LazerScan = a.lazer.Counts()
	}
	s.Memory = mem
	if mem != nil {
		s.Reliability = rel
	}
	if hasBest {
		s.Best = &best
	}
	if s.Problems == nil {
		s.Problems = []string{}
	}
	return s
}
