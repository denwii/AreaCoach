package engine

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/filter"
	"areacoach/internal/osu"
	"areacoach/internal/otd"
	"areacoach/internal/platform"
	"areacoach/internal/store"
)

// The diagnosis, map by map. The player plays maps of their choice, each
// with one area from start to end. Nothing of osu! is touched or read while
// it runs: when a map is passed, osu! saves its replay in Data/r like for any
// play, and the replay is all the diagnosis uses. From it, the area of the
// next map is worked out and set in OpenTabletDriver, between maps.

// diagMode marks sessions of this diagnosis (older ones are dropped).
const diagMode = "maps"

// adaptState is the diagnosis running in the background (under mu).
type adaptState struct {
	mu       sync.Mutex
	run      *analysis.AdaptRun
	runKey   time.Time // the session the run belongs to
	d        *otd.Daemon
	settings map[string]any
	wrote    time.Time // last time the area was written to the settings file
	setAt    time.Time // last time an area was set
	polled   time.Time // last look at the area OpenTabletDriver has
	// the misses expected by width from the maps of the diagnosis alone,
	// when the replays of the profile cannot say
	local  atomic.Pointer[analysis.MissModel]
	localN int
}

/* ---------- the loop ---------- */

func (e *Engine) adaptLoop() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for i := 0; ; i++ {
		<-tick.C
		c := e.st.State().Calib
		if c == nil || c.Mode != diagMode || !c.Active {
			e.adaptIdle()
			continue
		}
		e.adaptWatch()
		if i%3 == 0 {
			// a map just passed: its replay is looked for often
			e.Wake()
		}
		if i%5 == 0 {
			// between maps: the filter that goes with the area found
			e.adaptFilterKick()
		}
	}
}

// adaptIdle lets go of OpenTabletDriver while the diagnosis is paused.
func (e *Engine) adaptIdle() {
	ls := &e.ad
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if ls.d != nil {
		ls.d.Close()
		ls.d, ls.settings = nil, nil
	}
}

// adaptOpts are the rules of a session.
func (e *Engine) adaptOpts(c *store.CalibSession, su analysis.Setup) analysis.AdaptOpts {
	o := adaptOptsOf(c, su)
	set := e.st.State().Settings
	o.MinW, o.MaxW = set.DiagMinW, set.DiagMaxW
	o.Miss = e.missModel()
	if !o.Miss.OK() {
		o.Miss = e.ad.local.Load()
	}
	return o
}

// missModel is the model of the aim misses by width, from the replays.
func (e *Engine) missModel() *analysis.MissModel {
	e.repMu.Lock()
	defer e.repMu.Unlock()
	if e.report == nil {
		return nil
	}
	return e.report.MissModel()
}

func adaptOptsOf(c *store.CalibSession, su analysis.Setup) analysis.AdaptOpts {
	return analysis.AdaptOpts{Locked: c.Locked, Ratio: c.Ratio, TabletW: su.TabletW, TabletH: su.TabletH, Anchor: c.Base,
		FixPos: c.PosFixed, PosX: c.PosX, PosY: c.PosY, Shape: c.ShapeFrom, Cur: c.Cur,
		FixSize: c.SizeFixed, SizeW: c.SizeW, SizeH: c.SizeH}
}

// The shape of the area: that of the screen (forced proportions), one the
// player chose, or free (width and height each found on its own).
const (
	ShapeScreen = "screen"
	ShapeFixed  = "fixed"
	ShapeFree   = "free"
)

// ShapeMin and ShapeMax bound the height / width a player may choose.
const (
	ShapeMin = 0.35
	ShapeMax = 1.0
)

// screenRatio is the shape of the part of the screen the area maps to.
func screenRatio(su analysis.Setup, base otd.Rect) float64 {
	if su.Display.W > 0 && su.Display.H > 0 {
		return su.Display.H / su.Display.W
	}
	if base.W > 0 {
		return base.H / base.W
	}
	return 9.0 / 16
}

// shapeOf tells the shape rule of a session.
func shapeOf(c *store.CalibSession, su analysis.Setup) string {
	switch {
	case !c.Locked:
		return ShapeFree
	case math.Abs(c.Ratio-screenRatio(su, c.Base)) < 0.002:
		return ShapeScreen
	}
	return ShapeFixed
}

// runFor returns the run of the session, building it when needed.
func (e *Engine) runFor(ls *adaptState, c *store.CalibSession) *analysis.AdaptRun {
	if ls.run != nil && ls.runKey.Equal(c.Started) {
		return ls.run
	}
	cur := c.Cur
	if !cur.Valid() {
		cur = c.Start
	}
	ls.run = analysis.NewAdaptRun(e.adaptOpts(c, e.Env().Setup), cur, append([]analysis.CalibPoint(nil), c.Windows...))
	ls.run.Dither = c.Dither
	ls.runKey = c.Started
	return ls.run
}

// adaptWatch looks, between maps, at the area OpenTabletDriver really has:
// if it was changed from its window (Apply or Save there), the diagnosis
// stops changing it. It also keeps the other settings up to date, so that
// changing the area never undoes a change made there.
func (e *Engine) adaptWatch() {
	ls := &e.ad
	ls.mu.Lock()
	defer ls.mu.Unlock()
	now := time.Now()
	if now.Sub(ls.polled) < 3*time.Second || now.Sub(ls.setAt) < 2*time.Second {
		return
	}
	ls.polled = now
	c := e.st.State().Calib
	if c == nil || !c.Active {
		return
	}
	if ls.d == nil {
		d, s, err := e.daemon()
		if err != nil {
			return
		}
		ls.d, ls.settings = d, s
	} else if s, err := ls.d.GetSettings(); err == nil {
		ls.settings = s
	} else {
		ls.d.Close()
		ls.d, ls.settings = nil, nil
		return
	}
	e.filterSeenNow(ls, c)
	a, ok := otd.AreaOf(ls.settings, e.Env().Setup.TabletName)
	if !ok || sameArea(a, c.Cur) {
		return
	}
	if staleArea(a, c) && c.Cur.Valid() {
		// the numbers the OpenTabletDriver window still shows, sent back with
		// Apply or Save (as when the filter is changed there): the area of the
		// diagnosis goes back, the rest (the filter) stays as it came
		if err := e.adaptSet(ls, c.Cur); err == nil {
			e.adaptKeep(ls, c.Cur)
			log.Printf("diagnosis: an old area came back from the OpenTabletDriver window (%.1f × %.1f): putting the diagnosis one back, the rest stays as it is", a.W, a.H)
			e.adaptEvent(analysis.AdaptDecision{At: time.Now(), Kind: "otd", Note: fmt.Sprintf("the OpenTabletDriver window sent back an old area (%s × %s mm): the diagnosis one is back; the filter you set stays", mmTxt(a.W), mmTxt(a.H))})
			e.emit("calib")
			return
		}
	}
	log.Printf("diagnosis paused: OpenTabletDriver has another area (%.1f × %.1f)", a.W, a.H)
	e.st.Update(func(s *store.State) {
		if s.Calib != nil {
			s.Calib.Active = false
			s.Calib.PauseNote = fmt.Sprintf("Another area reached OpenTabletDriver (%s × %s mm), usually because Apply or Save was pressed in its window, which still shows the old numbers. I paused: press Resume to carry on with the area found.", mmTxt(a.W), mmTxt(a.H))
		}
	})
	_ = e.st.SaveState()
	e.emit("calib")
}

func sameExact(a, b otd.Rect) bool {
	return math.Abs(a.W-b.W) < 1e-6 && math.Abs(a.H-b.H) < 1e-6 && math.Abs(a.X-b.X) < 1e-6 && math.Abs(a.Y-b.Y) < 1e-6
}

// adaptSave stores what the run has learnt.
func (e *Engine) adaptSave(ls *adaptState, c *store.CalibSession) {
	run := ls.run
	if run == nil {
		return
	}
	obs := append([]analysis.CalibPoint(nil), run.Obs...)
	cur, dither := run.Cur, run.Dither
	e.st.Update(func(s *store.State) {
		if s.Calib != nil && s.Calib.Started.Equal(c.Started) {
			s.Calib.Windows, s.Calib.Cur, s.Calib.Dither = obs, cur, dither
		}
	})
	_ = e.st.SaveState()
	e.adaptResult()
	e.emit("calib")
}

// adaptEvent keeps a decision for the page (the last few hundred).
func (e *Engine) adaptEvent(d analysis.AdaptDecision) {
	e.st.Update(func(s *store.State) {
		if s.Calib == nil {
			return
		}
		s.Calib.Events = append(s.Calib.Events, d)
		if n := len(s.Calib.Events); n > 400 {
			s.Calib.Events = s.Calib.Events[n-400:]
		}
	})
}

// adaptSet sends an area to OpenTabletDriver, keeping the connection open.
func (e *Engine) adaptSet(ls *adaptState, r otd.Rect) error {
	tablet := e.Env().Setup.TabletName
	for attempt := 0; attempt < 2; attempt++ {
		if ls.d == nil {
			d, s, err := e.daemon()
			if err != nil {
				return err
			}
			ls.d, ls.settings = d, s
		} else if s, err := ls.d.GetSettings(); err == nil {
			// as they are now: a filter just put in from its window stays
			ls.settings = s
		}
		ns, err := otd.WithArea(ls.settings, tablet, r)
		if err != nil {
			return err
		}
		if err = ls.d.SetSettings(ns); err == nil {
			ls.settings = ns
			ls.setAt = time.Now()
			return nil
		}
		ls.d.Close()
		ls.d, ls.settings = nil, nil
	}
	return ErrLiveLost
}

// ErrLiveLost means OpenTabletDriver stopped answering.
var ErrLiveLost = errors.New("OpenTabletDriver stopped answering")

// adaptApply sets an area (live and in the settings file, so that it stays)
// and records when.
func (e *Engine) adaptApply(ls *adaptState, c *store.CalibSession, r otd.Rect) bool {
	if err := e.adaptSet(ls, r); err != nil {
		log.Printf("diagnosis paused: %v", err)
		e.st.Update(func(s *store.State) {
			if s.Calib != nil {
				s.Calib.Active = false
				s.Calib.PauseNote = "OpenTabletDriver stopped answering: I paused. Open it again and press Resume."
			}
		})
		_ = e.st.SaveState()
		e.emit("calib")
		return false
	}
	at := time.Now()
	if ls.run != nil {
		ls.run.Set(r)
	}
	e.st.Update(func(s *store.State) {
		if s.Calib != nil {
			s.Calib.Switches = append(s.Calib.Switches, analysis.LiveSwitch{At: at, Area: r})
			s.Calib.Cur = r
		}
	})
	e.adaptKeep(ls, r)
	return true
}

// adaptKeep saves an area in the settings file of OpenTabletDriver, as the
// start of a period of the diagnosis.
func (e *Engine) adaptKeep(ls *adaptState, r otd.Rect) {
	if err := e.writeOTDArea(r); err != nil {
		log.Printf("diagnosis: can't save the area in the OpenTabletDriver file: %v", err)
		return
	}
	ls.wrote = time.Now()
	su := e.Env().Setup
	su.Area = r
	if cur, ok := e.st.CurrentEpoch(); !ok || !cur.Setup.Area.Same(r) {
		e.st.StartEpoch(su, time.Now(), "calibrazione")
	}
}

/* ---------- actions ---------- */

// Where a diagnosis starts from: the area in use, all of the tablet (the
// largest area allowed), or a width the player chose.
const (
	StartCurrent = "current"
	StartFull    = "full"
	StartCustom  = "custom"
	StartFound   = "found" // starting again: the area the diagnosis had found
)

// CalibStartAdapt starts the diagnosis with a shape rule (ShapeScreen,
// ShapeFixed with ratio, ShapeFree) from an area (StartCurrent, StartFull,
// StartCustom with width w).
func (e *Engine) CalibStartAdapt(shape string, ratio float64, start string, w float64) error {
	env := e.Env()
	if !env.SetupOK {
		return errors.New("OpenTabletDriver and osu! need to be set up first")
	}
	d, _, err := e.daemon()
	if err != nil {
		return errors.New("OpenTabletDriver doesn't answer: the diagnosis needs OpenTabletDriver 0.6 running, to set the area between maps")
	}
	d.Close()
	base := env.Setup.Area
	old := e.st.State().Calib
	if old != nil && old.Mode == diagMode && old.Base.Valid() {
		base = old.Base // a reset keeps the area the player had before
	}
	locked := shape != ShapeFree
	screen := screenRatio(env.Setup, base)
	switch shape {
	case ShapeFixed:
		if ratio <= 0 {
			ratio = base.H / base.W
		}
		if !(ratio >= ShapeMin && ratio <= ShapeMax) {
			return errors.New("the proportion (height divided by width) must be between 0.35 and 1")
		}
	default:
		// forced proportions: the shape of the screen part the area maps
		// to; without, the limits of the shape are set around it
		ratio = screen
	}
	now := time.Now()
	sess := &store.CalibSession{Started: now, Resumed: now, Base: base, Active: true, Mode: diagMode, Locked: locked, Ratio: ratio}
	if old != nil && old.Mode == diagMode {
		// the guided filter goes on from the best values it had, and from the
		// filter OpenTabletDriver has now
		if fc := old.FCoach; fc != nil {
			sess.FCoach = &store.FilterCoach{Active: fc.Active, Started: now, Hold: fc.Hold, LowLag: fc.LowLag, Ask: fc.Best, AskOn: fc.BestOn, AskAt: now,
				Best: fc.Best, BestOn: fc.BestOn, Dither: 3}
		}
		if n := len(old.FSeen); n > 0 {
			sess.FSeen = []store.FilterSeen{old.FSeen[n-1]}
		}
	}
	o := e.adaptOpts(sess, env.Setup)
	switch start {
	case StartFull:
		sess.Start = analysis.AdaptStart(o)
	case StartCustom:
		if !(w > 0) {
			return errors.New("type the width to start from")
		}
		lo, hi := analysis.AdaptRange(o)
		if w < lo-0.05 || w > hi+0.05 {
			return fmt.Errorf("the width must be between %s and %s mm", mmTxt(lo), mmTxt(hi))
		}
		h := w * ratio
		if !locked {
			h = w * base.H / base.W
		}
		sess.Start = analysis.AdaptShape(otd.Rect{W: w, H: h, X: base.X, Y: base.Y, Rot: base.Rot}, o)
	default:
		from := base
		if start == StartFound && old != nil && old.Cur.Valid() {
			from = old.Cur
			from.Rot = base.Rot
		}
		h := from.W * ratio
		if !locked {
			h = from.H
		}
		sess.Start = analysis.AdaptShape(otd.Rect{W: from.W, H: h, X: from.X, Y: from.Y, Rot: base.Rot}, o)
	}
	sess.Cur = sess.Start
	ls := &e.ad
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.run, ls.localN = nil, 0
	ls.local.Store(nil)
	e.st.Update(func(s *store.State) { s.Calib = sess })
	e.runFor(ls, sess)
	if !e.adaptApply(ls, sess, sess.Start) {
		return errors.New("OpenTabletDriver doesn't accept the area")
	}
	log.Printf("diagnosis: starts with %.1f×%.1f (shape %s)", sess.Start.W, sess.Start.H, shapeOf(sess, env.Setup))
	_ = e.st.Save()
	e.emit("calib")
	return nil
}

// adaptAction drives the diagnosis.
func (e *Engine) adaptAction(c *store.CalibSession, action string) error {
	ls := &e.ad
	if strings.HasPrefix(action, "fc-") {
		if err := e.coachAction(c, action); err != nil {
			return err
		}
		_ = e.st.Save()
		e.emit("calib")
		return nil
	}
	if strings.HasPrefix(action, "shape-") {
		if err := e.adaptReshape(c, action); err != nil {
			return err
		}
		_ = e.st.Save()
		e.emit("calib")
		e.invalidate()
		return nil
	}
	switch action {
	case "pause", "apply":
		// the best area so far stays in OpenTabletDriver
		ls.mu.Lock()
		e.st.Update(func(s *store.State) { s.Calib.Active = false; s.Calib.PauseNote = "" })
		run := e.runFor(ls, c)
		rest := run.Rest()
		if err := e.adaptSet(ls, rest); err == nil {
			run.Set(rest)
			e.st.Update(func(s *store.State) {
				s.Calib.Switches = append(s.Calib.Switches, analysis.LiveSwitch{At: time.Now(), Area: rest})
				s.Calib.Cur = rest
			})
		}
		e.adaptKeep(ls, rest)
		e.adaptSave(ls, c)
		if ls.d != nil {
			ls.d.Close()
			ls.d, ls.settings = nil, nil
		}
		ls.mu.Unlock()
	case "resume":
		d, _, err := e.daemon()
		if err != nil {
			return errors.New("OpenTabletDriver doesn't answer: open it and try again")
		}
		d.Close()
		ls.mu.Lock()
		run := e.runFor(ls, c)
		// it goes on from the area found so far (whatever OpenTabletDriver
		// has now: often old values saved again from its window)
		cur := run.Rest()
		e.st.Update(func(s *store.State) {
			s.Calib.Active = true
			s.Calib.Resumed = time.Now()
			s.Calib.PauseNote = ""
		})
		ok := e.adaptApply(ls, e.st.State().Calib, cur)
		e.adaptSave(ls, e.st.State().Calib)
		ls.mu.Unlock()
		if !ok {
			return errors.New("OpenTabletDriver doesn't accept the area")
		}
	case "reset":
		return e.CalibStartAdapt(shapeOf(c, e.Env().Setup), c.Ratio, StartCurrent, 0)
	case "pos-follow", "pos-here", "pos-before", "size-fixed", "size-free":
		if strings.HasPrefix(action, "size-") {
			// the size: adapting, or kept as it is now (only the place adapts)
			ls.mu.Lock()
			run := e.runFor(ls, c)
			fixed := action == "size-fixed"
			w, h := run.Cur.W, run.Cur.H
			e.st.Update(func(s *store.State) {
				if s.Calib != nil {
					s.Calib.SizeFixed, s.Calib.SizeW, s.Calib.SizeH = fixed, w, h
				}
			})
			e.adaptRefresh(ls, e.st.State().Calib, "size")
			ls.mu.Unlock()
			if fixed {
				log.Printf("diagnosis: size fixed at %.1f×%.1f, it only moves", w, h)
			} else {
				log.Printf("diagnosis: the size adapts again")
			}
			break
		}
		// where the area sits: following the hand, or kept still
		ls.mu.Lock()
		run := e.runFor(ls, c)
		fixed, x, y := action != "pos-follow", run.Cur.X, run.Cur.Y
		if action == "pos-before" {
			x, y = c.Base.X, c.Base.Y
		}
		e.st.Update(func(s *store.State) {
			if s.Calib != nil {
				s.Calib.PosFixed, s.Calib.PosX, s.Calib.PosY = fixed, x, y
			}
		})
		e.adaptRefresh(ls, e.st.State().Calib, "position")
		ls.mu.Unlock()
		if fixed {
			log.Printf("diagnosis: position held at X %.1f Y %.1f", x, y)
		} else {
			log.Printf("diagnosis: the position follows the hand")
		}
	case "restore":
		// back to the area from before the diagnosis
		if c.Active {
			if err := e.adaptAction(c, "pause"); err != nil {
				return err
			}
		}
		if err := e.applyArea(c.Base); err != nil {
			return err
		}
		ls.mu.Lock()
		if ls.run != nil {
			ls.run.Set(c.Base)
		}
		ls.wrote = time.Now()
		ls.mu.Unlock()
		e.st.Update(func(s *store.State) {
			if s.Calib != nil {
				s.Calib.Cur = c.Base
			}
		})
	case "cancel", "stop":
		if c.Active {
			if err := e.adaptAction(c, "pause"); err != nil {
				return err
			}
		}
		if action == "cancel" {
			ls.mu.Lock()
			ls.run = nil
			ls.mu.Unlock()
			e.st.Update(func(s *store.State) { s.Calib = nil })
		}
	default:
		return errors.New("unknown action")
	}
	_ = e.st.Save()
	e.emit("calib")
	e.invalidate()
	return nil
}

// adaptRefresh works the area out again after a change of the rules (shape,
// place, widths allowed) and sets it at once: the diagnosis only sets areas
// between maps, and this is the player at the page. Callers hold ls.mu.
func (e *Engine) adaptRefresh(ls *adaptState, c *store.CalibSession, why string) {
	run := e.runFor(ls, c)
	run.SetOpts(e.adaptOpts(c, e.Env().Setup))
	if c.Active {
		cur := run.Cur
		if rest := run.Rest(); analysis.AdaptMove(cur, rest) >= 0.001 {
			if e.adaptApply(ls, c, rest) {
				e.adaptEvent(analysis.AdaptDecision{At: time.Now(), Kind: "rule", From: cur, To: rest, Note: why})
				log.Printf("diagnosis: new rule (%s), area %.1f×%.1f → %.1f×%.1f", why, cur.W, cur.H, rest.W, rest.H)
			}
		}
	}
	e.adaptSave(ls, c)
}

// adaptReshape changes the shape rule during a diagnosis: shape-screen
// (forced proportions), shape-fixed:R (height / width R, or the shape the
// area has now), shape-free. What was measured stays: the area found is
// worked out again with the new rule, and set at once.
func (e *Engine) adaptReshape(c *store.CalibSession, action string) error {
	su := e.Env().Setup
	ls := &e.ad
	ls.mu.Lock()
	defer ls.mu.Unlock()
	run := e.runFor(ls, c)
	cur := run.Cur
	if !cur.Valid() {
		cur = c.Cur
	}
	locked, ratio, from := c.Locked, c.Ratio, c.ShapeFrom
	screen := screenRatio(su, c.Base)
	switch {
	case action == "shape-screen":
		locked, ratio, from = true, screen, 0
	case action == "shape-free":
		// it goes on from the shape it has now
		locked, ratio, from = false, screen, 0
		if cur.Valid() {
			from = clampF(cur.H/cur.W, screen*0.7, screen*1.45)
		}
	case action == "shape-fixed" || strings.HasPrefix(action, "shape-fixed:"):
		r := 0.0
		if cur.Valid() {
			r = cur.H / cur.W
		}
		if v := strings.TrimPrefix(action, "shape-fixed:"); v != action {
			f, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(v), ",", ".", 1), 64)
			if err != nil {
				return errors.New("invalid proportion")
			}
			r = f
		}
		if !(r >= ShapeMin && r <= ShapeMax) {
			return errors.New("the proportion (height divided by width) must be between 0.35 and 1")
		}
		locked, ratio, from = true, math.Round(r*10000)/10000, 0
	default:
		return errors.New("unknown action")
	}
	e.st.Update(func(s *store.State) {
		if s.Calib != nil {
			s.Calib.Locked, s.Calib.Ratio, s.Calib.ShapeFrom = locked, ratio, from
		}
	})
	e.adaptRefresh(ls, e.st.State().Calib, "shape")
	return nil
}

// RangeChanged works the area out again when the widths allowed changed.
func (e *Engine) RangeChanged() {
	c := e.st.State().Calib
	if c == nil || c.Mode != diagMode {
		return
	}
	ls := &e.ad
	ls.mu.Lock()
	e.adaptRefresh(ls, c, "widths allowed")
	ls.mu.Unlock()
	_ = e.st.Save()
	e.emit("calib")
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

/* ---------- the maps ---------- */

// adaptProcess measures the replays of the maps played since the last time,
// and sets the area of the next map.
func (e *Engine) adaptProcess() bool {
	st := e.st.State()
	c := st.Calib
	if c == nil || c.Mode != diagMode || !c.Active {
		return false
	}
	done := map[int]bool{}
	for _, m := range c.Maps {
		done[m.PlayID] = true
	}
	endOf := playEnd
	var plays []*store.PlayMeta
	for _, p := range e.st.Plays() {
		if p.Status != "ok" || done[p.ID] || st.PlayProfile(p) != st.ActiveProfile || endOf(p).Before(c.Started) {
			continue
		}
		plays = append(plays, p)
	}
	if len(plays) == 0 {
		return false
	}
	sort.Slice(plays, func(i, j int) bool { return endOf(plays[i]).Before(endOf(plays[j])) })
	env := e.Env()
	ls := &e.ad
	ls.mu.Lock()
	defer ls.mu.Unlock()
	run := e.runFor(ls, c)
	// the misses expected, as they are now (the replays may have been
	// analysed since the run was built)
	e.adaptLocalModel(ls, c, nil)
	run.SetOpts(e.adaptOpts(c, env.Setup))
	var maps []store.AdaptMap
	var last *store.AdaptMap
	for _, p := range plays {
		end := endOf(p)
		rate := math.Max(p.Rate, 0.5)
		hits, herr := e.st.Hits(p.ID)
		// how long it was played: all of the map, or up to where it was
		// failed (a failed play saved with F2 on the game over screen)
		played, failed := playedMs(p, hits)
		start := end.Add(-time.Duration(played/rate)*time.Millisecond - 2*time.Second)
		m := store.AdaptMap{Title: playTitle(p), PlayID: p.ID, At: end, Failed: failed}
		a0 := analysis.AreaAt(c.Start, c.Switches, start)
		a1 := analysis.AreaAt(c.Start, c.Switches, end.Add(-time.Second))
		m.Area = a1
		switch {
		case start.Before(c.Resumed):
			m.Note = "played while the diagnosis was paused"
		case !sameArea(a0, a1):
			m.Note = "the area changed during the map: not used"
		case herr != nil:
			m.Note = "unreadable replay"
		}
		if m.Note != "" {
			maps = append(maps, m)
			continue
		}
		su := env.Setup
		su.Area = a1
		pd := &analysis.PlayData{ID: p.ID, Time: end, Rate: p.Rate, Hits: hits, Duration: p.LengthMs, Setup: su, Epoch: 1}
		ws := analysis.MapWindows(pd)
		// how straight the cursor went from circle to circle
		line, lineN := 0.0, 0
		if p.ReplayPath != "" {
			if rep, err := osu.ReadReplay(p.ReplayPath); err == nil {
				line, lineN = analysis.PathLines(hits, rep.Frames, su.Mapper(), rate)
			}
		}
		for i := range ws {
			ws[i].Map = m.Title
			ws[i].Line, ws[i].LineN = line, lineN/len(ws)
		}
		m.Windows = len(ws)
		m.Line = round4(line)
		m.G, m.DX, m.DY, m.Rot = mapMeasure(ws)
		m.Clicks, m.AimMiss, m.Edge = analysis.AimQuality(hits)
		// the aim misses expected on its notes, with that area and no filter
		if mm := run.Opts.Miss; mm.OK() {
			f := run.Fit
			if n, _, ex := mm.MapAimMisses(pd, f.BiasA, f.BiasB, f.Prec); n > 0 && m.Clicks > 0 {
				m.Exp = math.Round(ex*float64(m.Clicks)/float64(n)*100) / 100
			}
		}
		// the filter it was played with, and what the cursor did around the
		// clicks (as recorded: with that filter)
		m.Filter, m.FParams, m.FSrc = e.filterAt(c, start, end)
		if p.ReplayPath != "" {
			frate, _ := e.filterRate()
			if fw := playWindows(pd, p.ReplayPath, frate); len(fw) > 0 {
				ms := filter.Measure(fw, frate)
				m.Trem, m.Late, m.Over, m.CurN = round4(ms.Tremor), round4(ms.Late), round4(ms.Overshoot), ms.N
			}
		}
		if len(ws) > 0 {
			run.AddMap(ws)
		}
		sc := run.Fit.Score
		m.Score, m.Sure, m.Clean = sc.Pct, sc.Sure, sc.Clean
		m.Best = run.Rest()
		if len(ws) == 0 {
			m.Note = "too few measurable notes"
		}
		maps = append(maps, m)
		if len(ws) > 0 {
			last = &maps[len(maps)-1]
		}
	}
	if last != nil {
		e.adaptNextMap(ls, c, run, last, maps)
	}
	e.st.Update(func(s *store.State) {
		if s.Calib != nil && s.Calib.Started.Equal(c.Started) {
			s.Calib.Maps = append(s.Calib.Maps, maps...)
		}
	})
	e.adaptSave(ls, c)
	return true
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// playEnd is when a play ended: when osu! wrote its replay, or, for a replay
// saved later by hand (F2, as after a fail), the time written in it.
func playEnd(p *store.PlayMeta) time.Time {
	end := p.FileTime
	if end.IsZero() {
		return p.Time
	}
	if t := p.Time; !t.IsZero() && t.Before(end) && end.Sub(t) < 30*time.Minute {
		end = t
	}
	return end
}

// playedMs is how much of the map was played (map ms): to the last frame of
// the replay, which stops where the player failed; and whether it was
// failed (the replay ends well before the last circle).
func playedMs(p *store.PlayMeta, hits []analysis.Hit) (float64, bool) {
	if len(hits) == 0 {
		return p.LengthMs, false
	}
	first := float64(hits[0].Time)
	for _, h := range hits {
		first = math.Min(first, float64(h.Time))
	}
	played := p.Summary.LastTime - first
	if !(played > 0) || played > p.LengthMs {
		return p.LengthMs, false
	}
	return played, p.LengthMs-played > 3000
}

// mapScatter is how much the clicks of a map scattered against what the
// model expects (0: not known), and on how many clicks.
func mapScatter(mm *analysis.MissModel, ws []analysis.CalibPoint) (float64, int) {
	if !mm.OK() {
		return 0, 0
	}
	return mm.ScatterRatio(ws)
}

// mapMeasure is what the stretches of one map measured, on average.
func mapMeasure(ws []analysis.CalibPoint) (g, dx, dy, rot float64) {
	var n, rw, rp float64
	for _, w := range ws {
		k := float64(w.Notes)
		g += k * w.G
		dx += k * w.DX
		dy += k * w.DY
		n += k
		if w.Rot != nil {
			rw += w.Rot.S[0]
			rp += w.Rot.S[6]
		}
	}
	if n > 0 {
		g, dx, dy = g/n, dx/n, dy/n
	}
	if rw > 0 {
		rot = rp / rw * 180 / math.Pi
	}
	r := func(v float64, k float64) float64 { return math.Round(v*k) / k }
	return r(g, 1e4), r(dx, 1e3), r(dy, 1e3), r(rot, 100)
}

// adaptNextMap decides and sets the area of the next map, after last.
// Callers hold ls.mu.
func (e *Engine) adaptNextMap(ls *adaptState, c *store.CalibSession, run *analysis.AdaptRun, last *store.AdaptMap, maps []store.AdaptMap) {
	e.adaptLocalModel(ls, c, maps)
	run.HoldClean = e.st.State().Settings.DiagHold
	run.SetOpts(e.adaptOpts(c, e.Env().Setup))
	defer func() { e.coachNext(e.st.State().Calib, last, maps, run.Fit.Score.Miss, run.Fit.Target.W) }()
	if known, reaches := analysis.AreaReaches(run.Obs, c.NoReachAt); known && !reaches {
		e.adaptNoReach(ls, c, run)
		return
	}
	from := run.Cur
	next, try := run.Next(last.AimMiss == 0)
	last.Next, last.Try = next, try
	last.Blocked, last.Limit = run.Blocked, round4(run.Limit)
	last.PTX, last.PTY = run.PlaceTry[0], run.PlaceTry[1]
	last.Held = run.HoldClean && last.AimMiss == 0
	if !sameExact(next, from) {
		e.adaptApply(ls, c, next)
	}
	f := run.Fit
	note := ""
	switch {
	case last.Held:
		note = "no aim misses: area left as it was"
	case try != 0:
		note = fmt.Sprintf("you play the next one %s%% %s than the best found: every map tries a slightly different width, and all of them help to find the right one",
			fmt.Sprintf("%.1f", 100*math.Abs(math.Exp(try)-1)), map[bool]string{true: "bigger", false: "smaller"}[try > 0])
	case run.Blocked != "":
		note = blockedWords(run.Blocked, run.Limit, run.Fit.FreeW)
	}
	if pw := placeTryWords(run.PlaceTry, run.Cur.Rot); pw != "" && !last.Held {
		if note != "" {
			note += "; and I move it " + pw
		} else {
			note = "you play the next one moved " + pw
		}
	}
	e.adaptEvent(analysis.AdaptDecision{At: time.Now(), Kind: "map", Map: last.Title, From: from, To: next, Held: last.Held, Try: try, Note: note})
	g, dx, dy := last.G, last.DX, last.DY
	arc := ""
	if a := f.Arc; a.N >= 3 {
		arc = fmt.Sprintf("; aim turned %+.2f° (this map %+.2f°), changes %.2f°/10 mm (direction %+.0f°)", a.Mean, last.Rot, math.Hypot(a.GX, a.GY), a.Dir)
		if a.Shift != 0 {
			arc += fmt.Sprintf(", straight at %+.1f mm: moving %+.1f mm", a.Zero, a.Shift)
		}
		if a.Note != "" {
			arc += " (" + a.Note + ")"
		}
	}
	if sw := steadyWords(f.Steady, run.Cur.Rot); sw != "" {
		arc += "; steadiness by position: " + sw
	}
	log.Printf("diagnosis: end of %q with %.1f×%.1f: %d clicks, %d aim misses (expected %.1f), jumps %+.1f%%, clicks %+.2f / %+.2f mm, lines curved %.1f%%%s; expected misses %.1f per 1000 with %.1f mm; perfection %.1f%% (certainty %.0f%%, clean %.0f%%, uncertainty %.1f%%); next area %.1f×%.1f (%.1f; %.1f)%s",
		last.Title, last.Area.W, last.Area.H, last.Clicks, last.AimMiss, last.Exp, 100*(math.Exp(g)-1), dx, dy, 100*last.Line, arc, f.MissBest, f.Target.W,
		f.Score.Pct, f.Score.Sure, f.Score.Clean, 100*f.Score.Spread, next.W, next.H, next.X, next.Y, map[bool]string{true: " — " + note, false: ""}[note != ""])
}

// placeTryWords says how far off the best place the next map is played.
func placeTryWords(d [2]float64, rot float64) string {
	d[0], d[1] = analysis.ScreenVec(d[0], d[1], rot)
	side := ""
	v := 0.0
	switch {
	case d[0] > 0.05:
		side, v = "right of", d[0]
	case d[0] < -0.05:
		side, v = "left of", -d[0]
	case d[1] > 0.05:
		side, v = "lower than", d[1]
	case d[1] < -0.05:
		side, v = "higher than", -d[1]
	default:
		return ""
	}
	return fmt.Sprintf("%s mm %s the best place, to see where you're steadiest", mmTxt(v), side)
}

// steadyWords says how the steadiness changes with the place, and what the
// area did for it.
func steadyWords(s analysis.AdaptSteady, rot float64) string {
	// as seen on the screen
	s.GX, s.GY = analysis.ScreenVec(s.GX, s.GY, rot)
	s.ShiftX, s.ShiftY = analysis.ScreenVec(s.ShiftX, s.ShiftY, rot)
	t := rot * math.Pi / 180
	c, sn := math.Abs(math.Cos(t)), math.Abs(math.Sin(t))
	s.GXSE, s.GYSE = math.Hypot(c*s.GXSE, sn*s.GYSE), math.Hypot(sn*s.GXSE, c*s.GYSE)
	if s.N < 8 {
		return ""
	}
	part := func(g, se, sh float64, plus, minus string) string {
		if se <= 0 || math.Abs(g) < se {
			return ""
		}
		// variance per mm → share of scatter per 5 mm
		pct := 100 * (math.Exp(g*5/2) - 1)
		t := fmt.Sprintf("with the area 5 mm further %s the clicks spread %.0f%% more", map[bool]string{true: plus, false: minus}[g > 0], math.Abs(pct))
		if math.Abs(sh) >= 0.1 {
			t += fmt.Sprintf(" (moved %s mm %s for this)", mmTxt(math.Abs(sh)), map[bool]string{true: minus, false: plus}[sh < 0])
		}
		return t
	}
	var ps []string
	if p := part(s.GX, s.GXSE, s.ShiftX, "right", "left"); p != "" {
		ps = append(ps, p)
	}
	if p := part(s.GY, s.GYSE, s.ShiftY, "down", "up"); p != "" {
		ps = append(ps, p)
	}
	return strings.Join(ps, "; ")
}

// blockedWords says why the next map is played with the best width itself
// instead of a different one.
func blockedWords(why string, lim, free float64) string {
	switch why {
	case "min", "max":
		t := fmt.Sprintf("you play the next one with the best area found: I don't try it %s because %s mm is the %s width you set",
			map[bool]string{true: "smaller", false: "bigger"}[why == "min"], mmTxt(lim), map[bool]string{true: "minimum", false: "maximum"}[why == "min"])
		if free > 0 {
			t += fmt.Sprintf(" (without that limit the best would be about %s mm)", mmTxt(free))
		}
		return t + "; the one after tries the other side again"
	case "reach":
		return "you play the next one with the best area found: I don't try it bigger, with your fastest maps you wouldn't reach the circles in time; the one after tries smaller"
	}
	return ""
}

// adaptLocalModel builds the misses expected by width from the maps of the
// diagnosis, when the replays of the profile cannot give them (a new grip
// or tablet). Callers hold ls.mu.
func (e *Engine) adaptLocalModel(ls *adaptState, c *store.CalibSession, extra []store.AdaptMap) {
	if e.missModel().OK() {
		return
	}
	all := append(append([]store.AdaptMap(nil), c.Maps...), extra...)
	n := 0
	for _, m := range all {
		if m.Windows > 0 {
			n++
		}
	}
	if n < 2 || n == ls.localN {
		return
	}
	env := e.Env()
	var plays []*analysis.PlayData
	for _, m := range all {
		if m.Windows == 0 || filterOn(m.Filter) {
			continue
		}
		hits, err := e.st.Hits(m.PlayID)
		if err != nil {
			continue
		}
		su := env.Setup
		su.Area = m.Area
		rate := 1.0
		if p, ok := e.st.Play(m.PlayID); ok && p.Rate > 0 {
			rate = p.Rate
		}
		plays = append(plays, &analysis.PlayData{ID: m.PlayID, Time: m.At, Rate: rate, Hits: hits, Setup: su, Epoch: 1})
	}
	obs := ls.run.Obs
	mm := analysis.DiagMissModel(plays, obs, e.st.State().Settings.Weights)
	ls.local.Store(mm)
	ls.localN = n
	if mm.OK() {
		log.Printf("diagnosis: expected misses by width from the %d diagnosis maps (no model from this profile's replays)", n)
	}
}

// adaptNoReach pauses the diagnosis when the areas it sets clearly do not
// reach the tablet (the cursor moves as before whatever the size): it is
// changing the profile of another tablet, or the tablet is not driven by
// OpenTabletDriver. The area from before is put back. Callers hold ls.mu.
func (e *Engine) adaptNoReach(ls *adaptState, c *store.CalibSession, run *analysis.AdaptRun) {
	env := e.Env()
	log.Printf("diagnosis paused: the areas set don't reach the tablet (profile %q, connected %v)", env.Setup.TabletName, env.Connected)
	if err := e.adaptSet(ls, c.Base); err == nil {
		run.Set(c.Base)
		e.adaptKeep(ls, c.Base)
	}
	now := time.Now()
	e.st.Update(func(s *store.State) {
		if s.Calib == nil {
			return
		}
		s.Calib.Active = false
		s.Calib.Cur = c.Base
		s.Calib.NoReachAt = now
		s.Calib.PauseNote = fmt.Sprintf("The areas I set don't reach the tablet: I change them in OpenTabletDriver (profile \"%s\") but your jumps stay long or short the same way whatever the size. Usually OpenTabletDriver has the profiles of several tablets and I'm changing the wrong one, or the tablet isn't driven by OpenTabletDriver. I put the previous area back and paused: pick the right tablet in Settings, then press Start over.", env.Setup.TabletName)
	})
}

// adaptResult keeps the area found as the program's recommendation.
func (e *Engine) adaptResult() {
	c := e.st.State().Calib
	ls := &e.ad
	if c == nil || ls.run == nil || ls.run.Fit.N < 8 {
		return
	}
	f := ls.run.Fit
	res := &store.CalibResult{Rect: f.Target, At: time.Now(), Mode: "adapt", Samples: f.N, Precise: f.Sure,
		Lo: math.Round((f.W.Value-2*f.W.SD)*10) / 10, Hi: math.Round((f.W.Value+2*f.W.SD)*10) / 10, Filter: c.Filter}
	e.st.Update(func(s *store.State) { s.CalibResult = res })
}

// adaptManualChange pauses the diagnosis when the area was changed and saved
// by hand in OpenTabletDriver.
func (e *Engine) adaptManualChange(c *store.CalibSession) bool {
	env := e.Env()
	ls := &e.ad
	ls.mu.Lock()
	wrote := ls.wrote
	ls.mu.Unlock()
	since := c.Resumed
	if wrote.After(since) {
		since = wrote
	}
	if !c.Active || !env.OTDModTime.After(since.Add(2*time.Second)) {
		return false
	}
	for _, p := range env.Profiles {
		if p.Tablet != env.Setup.TabletName || !p.Area.Valid() {
			continue
		}
		// ours: the area set now, or one of the last few (not older ones: the
		// window of OpenTabletDriver often saves again the area it showed
		// when it was opened, such as the one from before the diagnosis)
		ours := sameArea(p.Area, c.Cur)
		for i := len(c.Switches) - 1; i >= 0 && !ours && i >= len(c.Switches)-3; i-- {
			ours = sameArea(p.Area, c.Switches[i].Area)
		}
		if ours {
			continue
		}
		if staleArea(p.Area, c) && c.Cur.Valid() {
			// an area the diagnosis already had (or the one from before it),
			// saved again by the OpenTabletDriver window that still shows it:
			// the area of the diagnosis goes back, in the file too
			ls.mu.Lock()
			err := e.adaptSet(ls, c.Cur)
			if err == nil {
				e.adaptKeep(ls, c.Cur)
			}
			ls.mu.Unlock()
			if err == nil {
				log.Printf("diagnosis: the OpenTabletDriver window saved an old area (%.1f × %.1f): saved the diagnosis one again", p.Area.W, p.Area.H)
				e.adaptEvent(analysis.AdaptDecision{At: time.Now(), Kind: "otd", Note: fmt.Sprintf("the OpenTabletDriver window saved an old area (%s × %s mm): the diagnosis one is saved again; the filter you set stays", mmTxt(p.Area.W), mmTxt(p.Area.H))})
				e.emit("calib")
				return false
			}
		}
		e.st.Update(func(s *store.State) {
			s.Calib.Active = false
			s.Calib.PauseNote = "Another area was saved in the OpenTabletDriver file. I paused: press Resume to carry on with the area found."
		})
		_ = e.st.Save()
		e.emit("calib")
		log.Printf("diagnosis paused: area changed by hand in OpenTabletDriver")
		return true
	}
	return false
}

func playTitle(p *store.PlayMeta) string {
	t := p.Title
	if p.Artist != "" {
		t = p.Artist + " - " + t
	}
	if p.Version != "" {
		t += " [" + p.Version + "]"
	}
	if p.ModStr != "" {
		t += " +" + p.ModStr
	}
	return t
}

/* ---------- status ---------- */

// AdaptStatus is what the diagnosis page shows.
type AdaptStatus struct {
	Active      bool                     `json:"active"`
	Cur         otd.Rect                 `json:"cur"` // the area in OpenTabletDriver now
	Start       otd.Rect                 `json:"start"`
	Base        otd.Rect                 `json:"base"` // the area before the diagnosis
	Locked      bool                     `json:"locked"`
	Ratio       float64                  `json:"ratio"`
	Shape       string                   `json:"shape"`       // screen, fixed, free
	ScreenRatio float64                  `json:"screenRatio"` // the shape of the screen
	ShapeFrom   float64                  `json:"shapeFrom"`   // free: the shape it keeps to
	Fit         analysis.AdaptFit        `json:"fit"`
	Events      []analysis.AdaptDecision `json:"events"`
	Maps        []store.AdaptMap         `json:"maps"`
	N           int                      `json:"n"`     // stretches measured
	NMaps       int                      `json:"nMaps"` // maps measured
	Started     time.Time                `json:"started"`
	RangeLo     float64                  `json:"rangeLo"` // the widths the diagnosis may use
	RangeHi     float64                  `json:"rangeHi"`
	MinW        float64                  `json:"minW"` // the ones the player allows (0: no limit)
	MaxW        float64                  `json:"maxW"`
	PauseNote   string                   `json:"pauseNote"`
	OTDWindow   bool                     `json:"otdWindow"` // the OpenTabletDriver window is open
	PosFixed    bool                     `json:"posFixed"`  // the place is kept still
	PosX        float64                  `json:"posX"`
	PosY        float64                  `json:"posY"`
	HoldClean   bool                     `json:"holdClean"` // no change after a map with clean aim
	SizeFixed   bool                     `json:"sizeFixed"` // the size is kept, only the place adapts
	SizeW       float64                  `json:"sizeW"`
	SizeH       float64                  `json:"sizeH"`
	Try         float64                  `json:"try"`    // the area set now is this much wider (+) or narrower than the best (log)
	TryMin      float64                  `json:"tryMin"` // the smallest and largest such difference
	TryMax      float64                  `json:"tryMax"`
	Coach       *CoachStatus             `json:"coach"`      // the filter guided map by map
	LocalModel  bool                     `json:"localModel"` // the misses expected come from the diagnosis maps alone
	Filter      *store.AdaptFilter       `json:"filter"`     // the Radial Follow that goes with the area
	FilterBusy  bool                     `json:"filterBusy"`
	FilterHas   bool                     `json:"filterHas"`  // Radial Follow is among the filters of the profile
	FilterNow   *otd.RadialFollow        `json:"filterNow"`  // the one on now
	FilterNeed  int                      `json:"filterNeed"` // clicks it needs
	HasFamily   bool                     `json:"hasFamily"`  // the filter's kinds and strengths were simulated
}

func (e *Engine) adaptStatus(c *store.CalibSession) *AdaptStatus {
	env := e.Env()
	set := e.st.State().Settings
	o := e.adaptOpts(c, env.Setup)
	out := &AdaptStatus{Active: c.Active, Cur: c.Cur, Start: c.Start, Base: c.Base, Locked: c.Locked, Ratio: c.Ratio,
		Started: c.Started, PauseNote: c.PauseNote, OTDWindow: platform.IsRunning(platform.OTDWindowName())}
	out.Shape, out.ScreenRatio = shapeOf(c, env.Setup), screenRatio(env.Setup, c.Base)
	if !c.Locked {
		out.ShapeFrom = math.Round(analysis.ShapePrior(o)*10000) / 10000
	}
	out.RangeLo, out.RangeHi = analysis.AdaptRange(o)
	out.MinW, out.MaxW = set.DiagMinW, set.DiagMaxW
	ls := &e.ad
	ls.mu.Lock()
	if run := ls.run; run != nil && ls.runKey.Equal(c.Started) {
		o.Keep = run.Keep()
		out.Cur = run.Cur
	}
	out.LocalModel = !e.missModel().OK() && ls.local.Load().OK()
	ls.mu.Unlock()
	out.Fit = analysis.FitAdapt(c.Windows, o, true)
	if n := len(out.Fit.Trail); n > 400 {
		out.Fit.Trail = out.Fit.Trail[n-400:]
	}
	if !c.Active && env.SetupOK && env.Setup.Area.Valid() {
		// paused: what OpenTabletDriver really has (it may have been changed by hand)
		out.Cur = env.Setup.Area
	}
	out.N = len(c.Windows)
	for _, m := range c.Maps {
		if m.Windows > 0 {
			out.NMaps++
		}
	}
	ev := c.Events
	if len(ev) > 80 {
		ev = ev[len(ev)-80:]
	}
	out.Events = ev
	maps := c.Maps
	if len(maps) > 60 {
		maps = maps[len(maps)-60:]
	}
	out.Maps = maps
	out.PosFixed, out.PosX, out.PosY = c.PosFixed, c.PosX, c.PosY
	out.HoldClean = set.DiagHold
	out.SizeFixed, out.SizeW, out.SizeH = c.SizeFixed, c.SizeW, c.SizeH
	out.TryMin, out.TryMax = analysis.AdaptTryMin, analysis.AdaptTryMax
	for i := len(c.Maps) - 1; i >= 0; i-- {
		if m := c.Maps[i]; m.Windows > 0 {
			if sameArea(m.Next, out.Cur) {
				out.Try = m.Try
			}
			break
		}
	}
	out.Coach = e.coachStatus(c, out.Fit.Score.Miss, out.Fit.Target.W)
	out.Filter, out.FilterBusy, out.FilterNeed = c.Filter, e.adaptFilterBusy(), adaptFilterMinClicks
	out.FilterHas, out.FilterNow = radialPlugin(env.Profile)
	out.FilterHas = out.FilterHas || otd.RadialFollowInstalled(env.OTDPath)
	out.HasFamily = c.FFam.OK()
	return out
}

func mmTxt(v float64) string { return fmt.Sprintf("%.1f", v) }

// ReopenOTDWindow closes and reopens the OpenTabletDriver window, which
// otherwise keeps showing the values it read when it was opened.
func (e *Engine) ReopenOTDWindow() error {
	was, err := platform.RestartProcess(platform.OTDWindowName())
	if err != nil {
		return fmt.Errorf("can't reopen the OpenTabletDriver window: %w", err)
	}
	if !was {
		return errors.New("the OpenTabletDriver window isn't open: when you open it, it already shows the current area")
	}
	return nil
}
