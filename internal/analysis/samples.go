package analysis

import (
	"math"
	"sort"
	"time"
)

// Map-section categories.
const (
	CatFastJump = iota
	CatWideJump
	CatStream
	CatSmall
	CatOther
	NumCats
)

// CatNames are the user-facing category names.
var CatNames = [NumCats]string{"Fast jumps", "Wide jumps", "Streams and bursts", "Small circles", "Other"}

// CatKeys are stable identifiers.
var CatKeys = [NumCats]string{"fast", "wide", "stream", "small", "other"}

// Categorize classifies a hit by the pattern that leads to it.
func Categorize(h *Hit) uint8 {
	r := float64(h.Radius)
	if r <= 30.5 {
		return CatSmall
	}
	if math.IsNaN(float64(h.PX)) || math.IsNaN(float64(h.Dt)) {
		return CatOther
	}
	d := math.Hypot(float64(h.TX-h.PX), float64(h.TY-h.PY))
	dt := float64(h.Dt)
	if dt <= 125 && d <= 5*r {
		return CatStream
	}
	if d >= 2.2*r {
		if dt <= 230 {
			return CatFastJump
		}
		return CatWideJump
	}
	return CatOther
}

// PlayData is a judged play with its context.
type PlayData struct {
	ID          int
	Time        time.Time
	Epoch       int // 0 = area unknown
	Setup       Setup
	Rate        float64
	Calibration string // calibration variant, "" for normal maps
	MapKey      string // beatmap and difficulty-changing mods, to compare the same map across areas
	Hits        []Hit
	Duration    float64
}

// Sample is one hit prepared for the models.
type Sample struct {
	Play, Session, Epoch int
	Calib                string
	Cat                  uint8
	Result               uint8
	Kind                 uint8
	When                 float64 // unix seconds
	Frac                 float64 // position in the play 0..1
	Rpx                  float64
	ENx, ENy             float64 // click error / radius (NaN if no click)
	OverN, SettleN       float64 // normalised by radius
	HasMM                bool
	Tx, Ty, Cx, Cy       float64 // tablet mm
	Ox, Oy               float64 // origin mm (NaN if none)
	Ax, Ay               float64 // area centre of the epoch
	Rmm                  float64
	Dmm                  float64 // |target - origin| mm (NaN if none)
	DmapMM               float64 // |target - previous object end| mm
	Dpx                  float64 // |target - previous object end| osu!px (NaN after breaks)
	DtReal               float64
}

// HasClick reports whether the sample has a press.
func (s *Sample) HasClick() bool { return !math.IsNaN(s.ENx) }

// HasOrigin reports whether the movement origin is known.
func (s *Sample) HasOrigin() bool { return !math.IsNaN(s.Ox) }

// Session groups consecutive plays.
type Session struct {
	ID    int
	Start time.Time
	End   time.Time
	Plays []int
}

// BuildSessions groups plays separated by less than 40 minutes.
func BuildSessions(plays []*PlayData) ([]Session, map[int]int) {
	sorted := append([]*PlayData(nil), plays...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
	var sessions []Session
	of := map[int]int{}
	for _, p := range sorted {
		n := len(sessions)
		end := p.Time
		if n > 0 && p.Time.Sub(sessions[n-1].End) < 40*time.Minute {
			sessions[n-1].Plays = append(sessions[n-1].Plays, p.ID)
			if end.After(sessions[n-1].End) {
				sessions[n-1].End = end
			}
		} else {
			start := p.Time.Add(-time.Duration(p.Duration/p.rateOr1()) * time.Millisecond)
			sessions = append(sessions, Session{ID: n, Start: start, End: end, Plays: []int{p.ID}})
		}
		of[p.ID] = len(sessions) - 1
	}
	return sessions, of
}

func (p *PlayData) rateOr1() float64 {
	if p.Rate <= 0 {
		return 1
	}
	return p.Rate
}

// BuildSamples converts plays to samples (mm where the area is known).
func BuildSamples(plays []*PlayData, sessionOf map[int]int) []Sample {
	total := 0
	for _, p := range plays {
		total += len(p.Hits)
	}
	out := make([]Sample, 0, total)
	for _, p := range plays {
		m := p.Setup.Mapper()
		hasMM := p.Epoch > 0 && m.OK()
		dur := p.Duration
		if dur <= 0 && len(p.Hits) > 0 {
			dur = float64(p.Hits[len(p.Hits)-1].Time)
		}
		start := float64(p.Time.Unix()) - dur/1000/p.rateOr1()
		for i := range p.Hits {
			h := &p.Hits[i]
			s := Sample{Play: p.ID, Session: sessionOf[p.ID], Epoch: p.Epoch, Calib: p.Calibration,
				Cat: Categorize(h), Result: h.Result, Kind: h.Kind,
				Rpx: float64(h.Radius), ENx: math.NaN(), ENy: math.NaN(), OverN: math.NaN(), SettleN: math.NaN(),
				Ox: math.NaN(), Oy: math.NaN(), Dmm: math.NaN(), DmapMM: math.NaN(), Dpx: math.NaN(), DtReal: float64(h.Dt)}
			if !math.IsNaN(float64(h.PX)) {
				s.Dpx = math.Hypot(float64(h.TX-h.PX), float64(h.TY-h.PY))
			}
			if dur > 0 {
				s.Frac = clamp(float64(h.Time)/dur, 0, 1)
			}
			s.When = start + float64(h.Time)/1000/p.rateOr1()
			if h.HasClick() {
				s.ENx = float64(h.CX-h.TX) / s.Rpx
				s.ENy = float64(h.CY-h.TY) / s.Rpx
			}
			if !math.IsNaN(float64(h.Over)) {
				s.OverN = float64(h.Over) / s.Rpx
			}
			if !math.IsNaN(float64(h.Settle)) {
				s.SettleN = float64(h.Settle) / s.Rpx
			}
			if hasMM {
				s.HasMM = true
				s.Tx, s.Ty = m.Point(float64(h.TX), float64(h.TY))
				s.Ax, s.Ay = p.Setup.Area.X, p.Setup.Area.Y
				s.Rmm = s.Rpx * m.MMPerPx()
				if h.HasClick() {
					s.Cx, s.Cy = m.Point(float64(h.CX), float64(h.CY))
				} else {
					s.Cx, s.Cy = math.NaN(), math.NaN()
				}
				if h.HasOrigin() {
					s.Ox, s.Oy = m.Point(float64(h.OX), float64(h.OY))
					s.Dmm = math.Hypot(s.Tx-s.Ox, s.Ty-s.Oy)
				}
				if !math.IsNaN(float64(h.PX)) {
					px, py := m.Point(float64(h.PX), float64(h.PY))
					s.DmapMM = math.Hypot(s.Tx-px, s.Ty-py)
				}
			}
			out = append(out, s)
		}
	}
	return out
}

// isAimHit says whether a sample is usable for aim error statistics: it has
// a press reasonably close to the target (far-off presses are rhythm or
// reading mistakes, not aim).
func isAimHit(s *Sample) bool {
	if !s.HasClick() {
		return false
	}
	return math.Hypot(s.ENx, s.ENy) <= 3
}
