// Package store persists plays, aim data, area history and settings.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/filter"
	"areacoach/internal/otd"
)

// PlayMeta describes an imported replay.
type PlayMeta struct {
	ID         int                  `json:"id"`
	ReplayPath string               `json:"replayPath"`
	ReplayMD5  string               `json:"replayMd5"`
	BeatmapMD5 string               `json:"beatmapMd5"`
	Player     string               `json:"player"`
	Time       time.Time            `json:"time"`
	Mods       uint32               `json:"mods"`
	ModStr     string               `json:"modStr"`
	Title      string               `json:"title"`
	Artist     string               `json:"artist"`
	Version    string               `json:"version"`
	Creator    string               `json:"creator"`
	CS         float64              `json:"cs"`
	AR         float64              `json:"ar"`
	OD         float64              `json:"od"`
	BPM        float64              `json:"bpm"`
	LengthMs   float64              `json:"lengthMs"`
	Rate       float64              `json:"rate"`
	N300       int                  `json:"n300"`
	N100       int                  `json:"n100"`
	N50        int                  `json:"n50"`
	NMiss      int                  `json:"nMiss"`
	MaxCombo   int                  `json:"maxCombo"`
	Accuracy   float64              `json:"accuracy"`
	Summary    analysis.PlaySummary `json:"summary"`
	NormErr    float64              `json:"normErr"`
	Status     string               `json:"status"` // ok, missing_map, skipped, error
	StatusMsg  string               `json:"statusMsg"`
	Profile    int                  `json:"profile,omitempty"` // chosen by hand; 0 = by time
	Mixed      bool                 `json:"mixed,omitempty"`   // the area changed during the play (in-game calibration)
	FileTime   time.Time            `json:"fileTime"`          // when the replay file was written
}

// TabletOverride lets the user set the physical tablet size by hand.
type TabletOverride struct {
	Name string  `json:"name"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

// Settings are user preferences.
type Settings struct {
	OsuDir          string                     `json:"osuDir"`
	OTDPath         string                     `json:"otdPath"`
	PreferredTablet string                     `json:"preferredTablet"`
	Tablet          *TabletOverride            `json:"tablet"`
	GameW           int                        `json:"gameW"`
	GameH           int                        `json:"gameH"`
	Weights         *[analysis.NumCats]float64 `json:"weights"`
	MaxImport       int                        `json:"maxImport"`
	PlayerName      string                     `json:"playerName"`
	Theme           string                     `json:"theme"`
	FilterRate      float64                    `json:"filterRate"` // tablet reports per second (0 = guess)
	DiagHold        bool                       `json:"diagHold"`   // keep the area after a map with no aim miss (since 2.1 off unless chosen)
	// the widths the diagnosis may use (0: no limit)
	DiagMinW float64 `json:"diagMinW,omitempty"`
	DiagMaxW float64 `json:"diagMaxW,omitempty"`
	// the keys that click (osu! names, such as "V"); empty: from the osu! config
	// the pen tip (mouse buttons) clicks: "on", "off", or "" (from the osu! config)
}

// CalibSession is the adaptive diagnosis: the area changes while the player
// plays maps of their choice, and settles on the one that fits them.
type CalibSession struct {
	Started   time.Time `json:"started"`
	Base      otd.Rect  `json:"base"`   // area before the diagnosis
	Active    bool      `json:"active"` // false = paused
	Mode      string    `json:"mode,omitempty"`
	Locked    bool      `json:"locked,omitempty"`    // forced proportions
	Resumed   time.Time `json:"resumed,omitempty"`   // last time it was started or resumed
	Ratio     float64   `json:"ratio,omitempty"`     // height / width when locked
	ShapeFrom float64   `json:"shapeFrom,omitempty"` // not locked: the shape kept unless the jumps ask for another (0: the start's)
	// maps played a little wider or narrower than the best width, in turn
	Dither int `json:"dither,omitempty"`
	// the areas set, and when
	Switches []analysis.LiveSwitch `json:"switches,omitempty"`
	Windows  []analysis.CalibPoint `json:"windows,omitempty"`
	// the area it started from, the one set now, the maps played
	Start  otd.Rect                 `json:"start,omitempty"`
	Cur    otd.Rect                 `json:"cur,omitempty"`
	Maps   []AdaptMap               `json:"maps,omitempty"`
	Events []analysis.AdaptDecision `json:"events,omitempty"`
	// why the diagnosis paused by itself, if it did
	PauseNote string `json:"pauseNote,omitempty"`
	// when it last paused because the areas set were not reaching the
	// tablet: the check starts again from there
	NoReachAt time.Time `json:"noReachAt,omitempty"`
	// the place of the area is kept still at PosX, PosY (only the size adapts)
	PosFixed bool    `json:"posFixed,omitempty"`
	PosX     float64 `json:"posX,omitempty"`
	PosY     float64 `json:"posY,omitempty"`
	// the size of the area is kept as it is (only the place adapts)
	SizeFixed bool    `json:"sizeFixed,omitempty"`
	SizeW     float64 `json:"sizeW,omitempty"`
	SizeH     float64 `json:"sizeH,omitempty"`
	// the Radial Follow that goes with the area found
	Filter *AdaptFilter `json:"filter,omitempty"`
	// the Radial Follow simulated on the movements recorded without a
	// filter: every kind at every strength
	FFam *filter.Family `json:"ffam,omitempty"`
	// the filter guided map by map: the values the player is told to put in
	// OpenTabletDriver
	FCoach *FilterCoach `json:"fcoach,omitempty"`
	// the Radial Follow OpenTabletDriver had, as read while the diagnosis
	// runs: when it changed
	FSeen []FilterSeen `json:"fseen,omitempty"`
}

// FilterSeen is the Radial Follow OpenTabletDriver had from At on.
type FilterSeen struct {
	At     time.Time     `json:"at"`
	On     bool          `json:"on"`
	Params filter.Params `json:"params"` // radii in tablet millimetres
	Tablet bool          `json:"tablet"` // the Tablet coordinates version (else screen pixels)
}

// FilterCoach is the filter guided map by map: the program says which
// values of Radial Follow to put in OpenTabletDriver for the next map; the
// player puts them; the replay says how they went; the next ones follow.
// Like the area: every map tries a filter a little stronger or lighter
// than the best one so far, and all of them say where the best one is.
type FilterCoach struct {
	Active  bool      `json:"active"`
	Started time.Time `json:"started"`
	// keep the best values, without trying others
	Hold bool `json:"hold,omitempty"`
	// as little latency as possible: only settings with a very small delay,
	// and among the ones about as good the quickest
	LowLag bool `json:"lowLag,omitempty"`
	Dither int  `json:"dither,omitempty"`
	// the values to put for the next map (AskOn false: Radial Follow off),
	// since when, and how much stronger (+) or lighter than the best (log)
	Ask    filter.Params `json:"ask"`
	AskOn  bool          `json:"askOn"`
	AskAt  time.Time     `json:"askAt"`
	AskTry float64       `json:"askTry,omitempty"`
	AskRef bool          `json:"askRef,omitempty"` // without a filter, to compare
	// when the player said the values are in (needed only when
	// OpenTabletDriver cannot be read)
	Confirmed time.Time `json:"confirmed,omitempty"`
	// the best so far
	Best   filter.Params `json:"best"`
	BestOn bool          `json:"bestOn"`
	// how close to a perfect filter after each map (0-100)
	Hist []FilterStep `json:"hist,omitempty"`
}

// FilterStep is the guided filter after one map.
type FilterStep struct {
	At  time.Time `json:"at"`
	Pct float64   `json:"pct"`
}

// AdaptFilter is the Radial Follow recommended with the area found by the
// diagnosis, tried on the pen movements recorded with areas close to it.
type AdaptFilter struct {
	At       time.Time       `json:"at"`
	Area     otd.Rect        `json:"area"` // the area it was worked out for
	Windows  int             `json:"windows"`
	Plays    int             `json:"plays"`
	Best     string          `json:"best"` // "none": no filter helps
	Params   filter.Params   `json:"params"`
	Gain     float64         `json:"gain"` // circles hit more per 1000 clicks
	GainLo   float64         `json:"gainLo"`
	GainHi   float64         `json:"gainHi"`
	Delay    float64         `json:"delay"` // ms of extra lag
	MissRaw  float64         `json:"missRaw"`
	Filtered bool            `json:"filtered"` // the movements were recorded with a smoothing filter on
	Options  []filter.Option `json:"options"`
	Tries    []filter.Option `json:"tries,omitempty"` // a filter for each latency, to try in game
	Current  *filter.Eval    `json:"current"`         // the Radial Follow in use, on the same data
	Rate     float64         `json:"rate"`
}

// AdaptMap is a map played during the diagnosis: its replay, the area it was
// played with, how the aim went, the area set after it.
type AdaptMap struct {
	Title   string    `json:"title"`
	PlayID  int       `json:"playId"`
	At      time.Time `json:"at"`   // when it ended
	Area    otd.Rect  `json:"area"` // the area it was played with
	Windows int       `json:"windows"`
	// circles clicked, aim misses, clicks at the edge
	Clicks  int `json:"clicks,omitempty"`
	AimMiss int `json:"aimMiss,omitempty"`
	Edge    int `json:"edge,omitempty"`
	// Held: no aim miss, the area was left as it was
	Held bool `json:"held,omitempty"`
	// the next map is played this much wider (+) or narrower than the best
	// width (log)
	Try float64 `json:"try,omitempty"`
	// the side the next map could not try, and why: "min" / "max" (the
	// widths the player allows: Limit mm), "reach" (too wide for the speed)
	Blocked string  `json:"blocked,omitempty"`
	Limit   float64 `json:"limit,omitempty"`
	// the next map is played this far off the best place (mm, right and
	// down), to see where the hand is steadier
	PTX  float64  `json:"ptx,omitempty"`
	PTY  float64  `json:"pty,omitempty"`
	Best otd.Rect `json:"best,omitempty"` // the best area found after it
	Next otd.Rect `json:"next,omitempty"` // the area set after it
	Note string   `json:"note,omitempty"` // why it was not used
	// what the map measured: jumps too long (+) or short, as a log ratio;
	// where the clicks landed against the circles (mm); how much the
	// movements came out turned (degrees, + clockwise)
	G   float64 `json:"g,omitempty"`
	DX  float64 `json:"dx,omitempty"`
	DY  float64 `json:"dy,omitempty"`
	Rot float64 `json:"rot,omitempty"`
	// how bent the paths between the circles were (share of the jump)
	Line float64 `json:"line,omitempty"`
	// how close to a perfect area the diagnosis was after it (0-100), how
	// sure and how clean
	Score float64 `json:"score,omitempty"`
	Sure  float64 `json:"sure,omitempty"`
	Clean float64 `json:"clean,omitempty"`
	// failed: the replay was saved on the game over screen (F2) and stops
	// where the player failed
	Failed bool `json:"failed,omitempty"`
	// the aim misses expected on its notes without a filter, and the filter
	// it was played with: "off", "on", "mixed" (changed during the map), ""
	// (not known); before 2.2 also a key of the filter test
	Exp    float64 `json:"exp,omitempty"`
	Filter string  `json:"filter,omitempty"`
	// the Radial Follow it was played with (radii in mm), and where that is
	// known from: "otd" (read from OpenTabletDriver), "ok" (the values asked,
	// the player said they were in)
	FParams *filter.Params `json:"fparams,omitempty"`
	FSrc    string         `json:"fsrc,omitempty"`
	// what the cursor did around the clicks, as recorded: wobble on the
	// circle before the press (share of the radius), presses made just as it
	// got in or outside, presses after going past the circle (shares)
	Trem float64 `json:"trem,omitempty"`
	Late float64 `json:"late,omitempty"`
	Over float64 `json:"over,omitempty"`
	CurN int     `json:"curN,omitempty"` // clicks those were measured on (0: not measured)
	// how close to a perfect filter the guided filter was after it (0-100;
	// nil: not guided then)
	FPct *float64 `json:"fpct,omitempty"`
}

// CalibResult is the outcome of the last finished calibration.
type CalibResult struct {
	Filter  *AdaptFilter `json:"filter,omitempty"` // the filter that goes with it (diagnosis)
	Rect    otd.Rect     `json:"rect"`
	At      time.Time    `json:"at"`
	Mode    string       `json:"mode"`
	Samples int          `json:"samples"` // segments (in game) or maps measured
	Precise bool         `json:"precise"`
	Lo      float64      `json:"lo"` // widths that do about as well
	Hi      float64      `json:"hi"`
}

// GripProfile is a way of holding the pen: plays of different profiles are
// never mixed in the analysis.
type GripProfile struct {
	ID      int       `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
}

// ProfileSpan says which profile was active from a moment on.
type ProfileSpan struct {
	Profile int       `json:"profile"`
	From    time.Time `json:"from"`
}

// ProfileSaved keeps the state of a profile that is not active.
type ProfileSaved struct {
	LastCompromise *otd.Rect                  `json:"lastCompromise"`
	LastAdvice     *otd.Rect                  `json:"lastAdvice"`
	Calib          *CalibSession              `json:"calib"`
	CalibResult    *CalibResult               `json:"calibResult"`
	Weights        *[analysis.NumCats]float64 `json:"weights"`
}

// State is everything stored in state.json.
type State struct {
	Version        int                   `json:"version"`
	FirstRun       time.Time             `json:"firstRun"`
	Settings       Settings              `json:"settings"`
	Epochs         []analysis.EpochInfo  `json:"epochs"`
	NextPlayID     int                   `json:"nextPlayId"`
	Seen           map[string]FileSig    `json:"seen"`
	LastAdvice     *otd.Rect             `json:"lastAdvice"`
	LastCompromise *otd.Rect             `json:"lastCompromise"`
	Calib          *CalibSession         `json:"calib"`
	CalibResult    *CalibResult          `json:"calibResult"`
	Profiles       []GripProfile         `json:"profiles"`
	ActiveProfile  int                   `json:"activeProfile"`
	ProfileSpans   []ProfileSpan         `json:"profileSpans"`
	ProfileData    map[int]*ProfileSaved `json:"profileData"`
	// replay timestamps written in local time: the shift found (minutes)
	ReplayTZMinutes int  `json:"replayTzMinutes"`
	ReplayTZChecked bool `json:"replayTzChecked"`
}

// FileSig identifies a replay file version.
type FileSig struct {
	Size  int64 `json:"s"`
	MTime int64 `json:"t"`
}

// Store is the on-disk database.
type Store struct {
	mu    sync.RWMutex
	dir   string
	state State
	plays []*PlayMeta
	byID  map[int]*PlayMeta
	byMD5 map[string]*PlayMeta
	hits  map[int][]analysis.Hit
}

// DefaultDir returns the data folder.
func DefaultDir() string {
	if ad := os.Getenv("APPDATA"); ad != "" {
		return filepath.Join(ad, "AreaCoach")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".config", "AreaCoach")
	}
	return "AreaCoach-data"
}

// Open loads (or creates) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "hits"), 0o755); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, byID: map[int]*PlayMeta{}, byMD5: map[string]*PlayMeta{}, hits: map[int][]analysis.Hit{}}
	s.state = State{Version: 1, FirstRun: time.Now(), NextPlayID: 1, Seen: map[string]FileSig{},
		Settings: Settings{MaxImport: 1500}}
	if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err == nil {
		if err := json.Unmarshal(b, &s.state); err != nil {
			return nil, fmt.Errorf("damaged state.json: %w", err)
		}
	}
	if s.state.Seen == nil {
		s.state.Seen = map[string]FileSig{}
	}
	if s.state.Settings.MaxImport == 0 {
		s.state.Settings.MaxImport = 1500
	}
	if len(s.state.Profiles) == 0 {
		s.state.Profiles = []GripProfile{{ID: 1, Name: "Tripod standard", Created: time.Now()}}
		s.state.ActiveProfile = 1
		s.state.ProfileSpans = []ProfileSpan{{Profile: 1}}
	}
	if s.state.ProfileData == nil {
		s.state.ProfileData = map[int]*ProfileSaved{}
	}
	if b, err := os.ReadFile(filepath.Join(dir, "plays.json")); err == nil {
		if err := json.Unmarshal(b, &s.plays); err != nil {
			return nil, fmt.Errorf("damaged plays.json: %w", err)
		}
	}
	for _, p := range s.plays {
		s.byID[p.ID] = p
		if p.ReplayMD5 != "" {
			s.byMD5[p.ReplayMD5] = p
		}
	}
	return s, nil
}

// Dir returns the data folder.
func (s *Store) Dir() string { return s.dir }

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Save writes state and play index.
func (s *Store) Save() error {
	s.mu.RLock()
	st, err := json.MarshalIndent(s.state, "", " ")
	if err != nil {
		s.mu.RUnlock()
		return err
	}
	pl, err := json.Marshal(s.plays)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(s.dir, "state.json"), st); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, "plays.json"), pl)
}

// SaveState writes only state.json (cheap, for frequent small changes).
func (s *Store) SaveState() error {
	s.mu.RLock()
	st, err := json.MarshalIndent(s.state, "", " ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, "state.json"), st)
}

// SetPlayMixed marks a play made while the area kept changing.
func (s *Store) SetPlayMixed(playID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.byID[playID]; ok {
		p.Mixed = true
	}
}

// State returns a copy of the state.
func (s *Store) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.state
	st.Epochs = append([]analysis.EpochInfo(nil), s.state.Epochs...)
	return st
}

// Update modifies the state under lock.
func (s *Store) Update(f func(st *State)) {
	s.mu.Lock()
	f(&s.state)
	s.mu.Unlock()
}

// Seen reports whether a replay file was already processed.
func (s *Store) Seen(path string, sig FileSig) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	old, ok := s.state.Seen[path]
	return ok && old == sig
}

// MarkSeen records a processed replay file.
func (s *Store) MarkSeen(path string, sig FileSig) {
	s.mu.Lock()
	s.state.Seen[path] = sig
	s.mu.Unlock()
}

// ForgetSeen makes the replay files in paths be looked at again, unless a
// play was imported from them. It returns how many.
func (s *Store) ForgetSeen(paths []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	used := map[string]bool{}
	for _, p := range s.plays {
		used[p.ReplayPath] = true
	}
	n := 0
	for _, p := range paths {
		if _, ok := s.state.Seen[p]; ok && !used[p] {
			delete(s.state.Seen, p)
			n++
		}
	}
	return n
}

// HasReplay reports whether a replay (by its MD5) was imported already.
func (s *Store) HasReplay(md string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byMD5[md]
	return ok
}

// AddPlay stores a play and its hits.
func (s *Store) AddPlay(p *PlayMeta, hits []analysis.Hit) error {
	s.mu.Lock()
	if old, ok := s.byMD5[p.ReplayMD5]; ok && p.ReplayMD5 != "" {
		p.ID = old.ID
		*old = *p
	} else {
		p.ID = s.state.NextPlayID
		s.state.NextPlayID++
		s.plays = append(s.plays, p)
		s.byID[p.ID] = p
		if p.ReplayMD5 != "" {
			s.byMD5[p.ReplayMD5] = p
		}
	}
	id := p.ID
	if hits != nil {
		s.hits[id] = hits
	}
	s.mu.Unlock()
	if hits != nil {
		return writeAtomic(filepath.Join(s.dir, "hits", fmt.Sprintf("%d.bin", id)), analysis.EncodeHits(hits))
	}
	return nil
}

// Plays returns the play list sorted by time (newest first).
func (s *Store) Plays() []*PlayMeta {
	s.mu.RLock()
	out := append([]*PlayMeta(nil), s.plays...)
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// Play returns one play.
func (s *Store) Play(id int) (*PlayMeta, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	return p, ok
}

// Hits returns the hits of a play (cached).
func (s *Store) Hits(id int) ([]analysis.Hit, error) {
	s.mu.RLock()
	h, ok := s.hits[id]
	s.mu.RUnlock()
	if ok {
		return h, nil
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "hits", fmt.Sprintf("%d.bin", id)))
	if err != nil {
		return nil, err
	}
	h, err = analysis.DecodeHits(b)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.hits[id] = h
	s.mu.Unlock()
	return h, nil
}

// CurrentEpoch returns the open epoch, if any.
func (s *Store) CurrentEpoch() (analysis.EpochInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.state.Epochs) - 1; i >= 0; i-- {
		if s.state.Epochs[i].To == nil {
			return s.state.Epochs[i], true
		}
	}
	return analysis.EpochInfo{}, false
}

// EpochAt returns the epoch in use at time t (0 if unknown).
func EpochAt(epochs []analysis.EpochInfo, t time.Time) int {
	for i := len(epochs) - 1; i >= 0; i-- {
		e := epochs[i]
		if !t.Before(e.From) && (e.To == nil || t.Before(*e.To)) {
			return e.ID
		}
	}
	return 0
}

// ProfileAt returns the profile active at time t.
func ProfileAt(spans []ProfileSpan, t time.Time) int {
	id := 1
	for _, sp := range spans {
		if !t.Before(sp.From) {
			id = sp.Profile
		}
	}
	return id
}

// PlayProfile returns the profile of a play.
func (st *State) PlayProfile(p *PlayMeta) int {
	if p.Profile > 0 {
		return p.Profile
	}
	return ProfileAt(st.ProfileSpans, p.Time)
}

// CreateProfile adds a profile and returns its id.
func (s *Store) CreateProfile(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := 1
	for _, p := range s.state.Profiles {
		if p.ID >= id {
			id = p.ID + 1
		}
	}
	s.state.Profiles = append(s.state.Profiles, GripProfile{ID: id, Name: name, Created: time.Now()})
	return id
}

// RenameProfile changes the name of a profile.
func (s *Store) RenameProfile(id int, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Profiles {
		if s.state.Profiles[i].ID == id {
			s.state.Profiles[i].Name = name
			return nil
		}
	}
	return errors.New("profile not found")
}

// SwitchProfile makes another profile active from now on. What belongs to
// a profile (last advice, calibration, priorities) is kept apart.
func (s *Store) SwitchProfile(id int, from time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, p := range s.state.Profiles {
		if p.ID == id {
			found = true
		}
	}
	if !found {
		return errors.New("profile not found")
	}
	if id == s.state.ActiveProfile {
		return nil
	}
	st := &s.state
	st.ProfileData[st.ActiveProfile] = &ProfileSaved{LastCompromise: st.LastCompromise, LastAdvice: st.LastAdvice, Calib: st.Calib,
		CalibResult: st.CalibResult, Weights: st.Settings.Weights}
	next := st.ProfileData[id]
	if next == nil {
		next = &ProfileSaved{}
	}
	st.LastCompromise, st.LastAdvice, st.Calib, st.Settings.Weights = next.LastCompromise, next.LastAdvice, next.Calib, next.Weights
	st.CalibResult = next.CalibResult
	delete(st.ProfileData, id)
	st.ActiveProfile = id
	// a switch replaces any later one (e.g. a switch made by mistake)
	var spans []ProfileSpan
	for _, sp := range st.ProfileSpans {
		if sp.From.Before(from) {
			spans = append(spans, sp)
		}
	}
	st.ProfileSpans = append(spans, ProfileSpan{Profile: id, From: from})
	return nil
}

// SetPlayProfile assigns one play to a profile by hand (0 = by time).
func (s *Store) SetPlayProfile(playID, profile int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byID[playID]
	if !ok {
		return errors.New("play not found")
	}
	p.Profile = profile
	return nil
}

// ShiftPlayTimes moves the time of the plays for which f returns a shift.
func (s *Store) ShiftPlayTimes(f func(p *PlayMeta) time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.plays {
		if d := f(p); d != 0 {
			p.Time = p.Time.Add(d)
			n++
		}
	}
	return n
}

// sameMapping reports whether two setups map the pen to the screen in the
// same way (area, display mapping, filters): game window details aside.
func sameMapping(a, b analysis.Setup) bool {
	return a.Area.Same(b.Area) && a.Display.Same(b.Display) &&
		(!a.SmoothingSet || !b.SmoothingSet || a.Smoothing == b.Smoothing)
}

// MergeRepeatedEpochs joins consecutive periods with the same area, display
// and filters: older versions could open a new period when only the game
// window changed, which split the plays of one area in two.
func (s *Store) MergeRepeatedEpochs() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := 0
	out := s.state.Epochs[:0]
	for _, e := range s.state.Epochs {
		if n := len(out); n > 0 && sameMapping(out[n-1].Setup, e.Setup) {
			prev := &out[n-1]
			prev.To = e.To
			prev.Established = prev.Established || e.Established
			prev.Adapted = prev.Adapted || e.Adapted
			set := prev.Setup.SmoothingSet || e.Setup.SmoothingSet
			sm := prev.Setup.Smoothing
			if e.Setup.SmoothingSet {
				sm = e.Setup.Smoothing
			}
			prev.Setup = e.Setup
			prev.Setup.Smoothing, prev.Setup.SmoothingSet = sm, set
			merged++
			continue
		}
		out = append(out, e)
	}
	s.state.Epochs = out
	return merged
}

// StartEpoch closes the current epoch at `at` and opens a new one.
func (s *Store) StartEpoch(setup analysis.Setup, at time.Time, source string) analysis.EpochInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.state.Epochs)
	if n > 0 && s.state.Epochs[n-1].To == nil {
		last := &s.state.Epochs[n-1]
		if !at.After(last.From) {
			at = time.Now()
		}
		t := at
		last.To = &t
	}
	id := 1
	for _, e := range s.state.Epochs {
		if e.ID >= id {
			id = e.ID + 1
		}
	}
	ep := analysis.EpochInfo{ID: id, Setup: setup, From: at, Source: source, PrevAdvice: s.state.LastAdvice}
	s.state.Epochs = append(s.state.Epochs, ep)
	return ep
}

// UpdateCurrentSetup changes non-area parts of the current setup in place
// (e.g. tablet size correction) without starting a new epoch.
func (s *Store) UpdateCurrentSetup(f func(*analysis.Setup)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.state.Epochs) - 1; i >= 0; i-- {
		if s.state.Epochs[i].To == nil {
			f(&s.state.Epochs[i].Setup)
			return
		}
	}
}

// SetEpochStart moves the start of an epoch (and the end of the previous).
func (s *Store) SetEpochStart(id int, from time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Epochs {
		if s.state.Epochs[i].ID != id {
			continue
		}
		if i > 0 {
			prev := &s.state.Epochs[i-1]
			if !from.After(prev.From) {
				return errors.New("the date must be after the start of the previous area")
			}
			t := from
			prev.To = &t
		}
		if s.state.Epochs[i].To != nil && !from.Before(*s.state.Epochs[i].To) {
			return errors.New("the date must be before the end of this area")
		}
		s.state.Epochs[i].From = from
		return nil
	}
	return errors.New("area not found")
}

// SetEpochFlag sets the "already adapted" flag.
func (s *Store) SetEpochFlag(id int, adapted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Epochs {
		if s.state.Epochs[i].ID == id {
			s.state.Epochs[i].Adapted = adapted
		}
	}
}

// AddPastEpoch inserts an area used before the first known one.
func (s *Store) AddPastEpoch(area otd.Rect, from time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.Epochs) == 0 {
		return errors.New("no current area")
	}
	first := s.state.Epochs[0]
	if !from.Before(first.From) {
		return errors.New("the date must be before the oldest area recorded")
	}
	id := 1
	for _, e := range s.state.Epochs {
		if e.ID >= id {
			id = e.ID + 1
		}
	}
	setup := first.Setup
	setup.Area = area
	to := first.From
	ep := analysis.EpochInfo{ID: id, Setup: setup, From: from, To: &to, Source: "manuale", Established: true}
	s.state.Epochs = append([]analysis.EpochInfo{ep}, s.state.Epochs...)
	return nil
}

// RemoveEpoch deletes a manually added past area.
func (s *Store) RemoveEpoch(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.state.Epochs {
		if e.ID == id {
			if e.To == nil {
				return errors.New("you can't delete the current area")
			}
			if i+1 < len(s.state.Epochs) && i > 0 {
				prev := &s.state.Epochs[i-1]
				t := s.state.Epochs[i+1].From
				prev.To = &t
			}
			s.state.Epochs = append(s.state.Epochs[:i], s.state.Epochs[i+1:]...)
			return nil
		}
	}
	return errors.New("area not found")
}

// ResetPlays forgets every imported play (used for a full re-import).
func (s *Store) ResetPlays() {
	s.mu.Lock()
	s.plays = nil
	s.byID = map[int]*PlayMeta{}
	s.byMD5 = map[string]*PlayMeta{}
	s.hits = map[int][]analysis.Hit{}
	s.state.Seen = map[string]FileSig{}
	s.mu.Unlock()
	_ = os.RemoveAll(filepath.Join(s.dir, "hits"))
	_ = os.MkdirAll(filepath.Join(s.dir, "hits"), 0o755)
}
