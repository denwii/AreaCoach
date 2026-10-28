package analysis

import (
	"math"
	"math/rand"
	"sort"
	"time"

	"areacoach/internal/otd"
)

// EpochInfo is a period during which one area was in use.
type EpochInfo struct {
	ID          int        `json:"id"`
	Setup       Setup      `json:"setup"`
	From        time.Time  `json:"from"`
	To          *time.Time `json:"to"`
	Source      string     `json:"source"`
	Adapted     bool       `json:"adapted"`     // user says they are used to it
	Established bool       `json:"established"` // in use before the coach started
	PrevAdvice  *otd.Rect  `json:"prevAdvice"`  // advice shown when the area was changed
}

// Input is everything Analyze needs.
type Input struct {
	Plays   []*PlayData
	Epochs  []EpochInfo // one per area: periods with the same area are joined
	Current *EpochInfo
	// Periods are the area changes in time order, as they happened (for the
	// history and the timelines); Epochs if empty
	Periods []EpochInfo
	Weights *[NumCats]float64
	Now     time.Time
	// PrevCompromise is the area recommended last time: it is kept while
	// the new estimate is within its uncertainty, and frozen while the player
	// is trying it
	PrevCompromise *otd.Rect
	Detailed       bool
}

func (in Input) periods() []EpochInfo {
	if len(in.Periods) > 0 {
		return in.Periods
	}
	return in.Epochs
}

// AreaOut is an area with its uncertainty.
type AreaOut struct {
	otd.Rect
	ErrW   float64 `json:"errW"`
	ErrH   float64 `json:"errH"`
	ErrX   float64 `json:"errX"`
	ErrY   float64 `json:"errY"`
	ErrRot float64 `json:"errRot"`
	Fits   bool    `json:"fits"`
}

// Evidence is one line of reasoning shown under "Perché?".
type Evidence struct {
	Text string `json:"text"`
	Tone string `json:"tone"` // good, neutral, warn
}

// CoachOut is the current coaching state.
type CoachOut struct {
	Phase      string     `json:"phase"`
	PhaseIndex int        `json:"phaseIndex"`
	Title      string     `json:"title"`
	Message    string     `json:"message"`
	Progress   float64    `json:"progress"`
	Next       *otd.Rect  `json:"next"`
	Change     *Change    `json:"change"`
	Evidence   []Evidence `json:"evidence"`
	History    []HistItem `json:"history"`
	Converged  []string   `json:"converged"`
}

// Change describes the suggested step.
type Change struct {
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
	DW   float64 `json:"dw"`
	DH   float64 `json:"dh"`
	DRot float64 `json:"drot"`
	Kind string  `json:"kind"`
}

// HistItem is one past decision.
type HistItem struct {
	Date time.Time `json:"date"`
	Text string    `json:"text"`
	Kind string    `json:"kind"`
}

// AreasOut groups the candidate areas.
type AreasOut struct {
	Current otd.Rect `json:"current"`
	// Final is the recommended area: the width with the fewest predicted
	// misses over every kind of pattern, from every replay and every area,
	// with the proportions of the current area. Free is the same width with
	// the hand's own proportions and rotation; Shifted keeps the current size
	// and only moves it.
	Final      *AreaOut      `json:"final"`
	Free       *AreaOut      `json:"free"`
	Shifted    *AreaOut      `json:"shifted"`
	Rec        *RecOut       `json:"rec"`
	TabletW    float64       `json:"tabletW"`
	TabletH    float64       `json:"tabletH"`
	TabletName string        `json:"tabletName"`
	Playfield  [4][2]float64 `json:"playfield"`
	Epochs     []EpochArea   `json:"epochs"`  // one per area
	Periods    []EpochArea   `json:"periods"` // every change, in time order
}

// EpochArea is a past area for the tablet drawing and comparisons.
type EpochArea struct {
	ID      int        `json:"id"`
	Area    otd.Rect   `json:"area"`
	From    time.Time  `json:"from"`
	To      *time.Time `json:"to"`
	Hits    int        `json:"hits"`
	Current bool       `json:"current"`
}

// AimOut holds the aim diagnostics of one area.
type AimOut struct {
	EpochID     int            `json:"epochId"`
	Area        otd.Rect       `json:"area"`
	From        time.Time      `json:"from"`
	To          *time.Time     `json:"to"`
	Current     bool           `json:"current"`
	Hits        int            `json:"hits"`
	OffsetX     float64        `json:"offsetX"`
	OffsetY     float64        `json:"offsetY"`
	OffsetErr   float64        `json:"offsetErr"`
	JumpGain    float64        `json:"jumpGain"` // %
	JumpGainErr float64        `json:"jumpGainErr"`
	PosGain     float64        `json:"posGain"` // %
	Rotation    float64        `json:"rotation"`
	RotationErr float64        `json:"rotationErr"`
	Scale       float64        `json:"scale"` // residual spread mm
	Field       []FieldCell    `json:"field"`
	Distance    []DistPoint    `json:"distance"`
	Directions  []DirPoint     `json:"directions"`
	Drift       []DriftPoint   `json:"drift"`
	Scatter     []ScatterPoint `json:"scatter"`
	NormErr     float64        `json:"normErr"`
	Inside      float64        `json:"inside"`
}

// CatOut is one category for the consistency page.
type CatOut struct {
	Key     string     `json:"key"`
	Name    string     `json:"name"`
	Share   float64    `json:"share"`
	Weight  float64    `json:"weight"`
	Current CatMetrics `json:"current"`
	Curve   []float64  `json:"curve"`
	Lo      []float64  `json:"lo"`
	Hi      []float64  `json:"hi"`
	BestS   float64    `json:"bestS"`
	BestW   float64    `json:"bestW"`
}

// EpochCompare holds observed metrics per area.
type EpochCompare struct {
	ID        int                     `json:"id"`
	Area      otd.Rect                `json:"area"`
	Unknown   bool                    `json:"unknown"`
	Hits      int                     `json:"hits"`
	Plays     int                     `json:"plays"`
	Sessions  int                     `json:"sessions"`
	From      time.Time               `json:"from"`
	To        *time.Time              `json:"to"`
	ByCat     [NumCats + 1]CatMetrics `json:"byCat"`
	Current   bool                    `json:"current"`
	HasBias   bool                    `json:"hasBias"`
	Lambda    float64                 `json:"lambda"`
	LambdaErr float64                 `json:"lambdaErr"`
	NaturalW  float64                 `json:"naturalW"`
	JumpGain  float64                 `json:"jumpGain"`
	Offset    float64                 `json:"offset"`
	SameMaps  *PairedCmp              `json:"sameMaps"`
}

// PairedCmp compares the current area with another one on the maps played
// with both: the fairest comparison, because map difficulty is the same.
type PairedCmp struct {
	Maps   int     `json:"maps"`
	Other  float64 `json:"other"`  // mean error with the other area
	Cur    float64 `json:"cur"`    // mean error with the current area
	Change float64 `json:"change"` // relative change current vs other
	Lo     float64 `json:"lo"`
	Hi     float64 `json:"hi"`
}

// ConsistencyOut is the consistency / compromise analysis.
type ConsistencyOut struct {
	Ready       bool           `json:"ready"`
	Steadiness  float64        `json:"steadiness"` // a (mm), along movement
	DistTerm    float64        `json:"distTerm"`   // b (% of distance)
	SpeedCoef   float64        `json:"speedCoef"`
	SpeedExp    float64        `json:"speedExp"`
	VMax        float64        `json:"vmax"`
	Scales      []float64      `json:"scales"`
	Widths      []float64      `json:"widths"`
	Combined    []float64      `json:"combined"`
	CombLo      []float64      `json:"combLo"`
	CombHi      []float64      `json:"combHi"`
	Best        float64        `json:"best"`
	BestLo      float64        `json:"bestLo"`
	BestHi      float64        `json:"bestHi"`
	TrustLo     float64        `json:"trustLo"`
	TrustHi     float64        `json:"trustHi"`
	Categories  []CatOut       `json:"categories"`
	Epochs      []EpochCompare `json:"epochs"`
	Calibration [][3]float64   `json:"calibration"` // predicted, observed, n
	Insights    []string       `json:"insights"`
	SigmaCurve  [][3]float64   `json:"sigmaCurve"` // speed, sigma par, sigma perp at typical distance
	NoiseHits   int            `json:"noiseHits"`
	NoiseAreas  int            `json:"noiseAreas"`
	LambdaKnown bool           `json:"lambdaKnown"`
	Cross       CrossArea      `json:"cross"`
	Size        *SizeDecision  `json:"size"`
	BestW       float64        `json:"bestW"`
	BestLoW     float64        `json:"bestLoW"`
	BestHiW     float64        `json:"bestHiW"`
	TrustLoW    float64        `json:"trustLoW"`
	TrustHiW    float64        `json:"trustHiW"`
	ByCS        []CSBest       `json:"byCS"`
	Stable      bool           `json:"stable"`
}

// SessionOut is one session on the progress page.
type SessionOut struct {
	ID        int       `json:"id"`
	Start     time.Time `json:"start"`
	Plays     int       `json:"plays"`
	Hits      int       `json:"hits"`
	NormErr   float64   `json:"normErr"`
	MissPer1k float64   `json:"missPer1k"`
	Inside    float64   `json:"inside"`
	OffsetX   float64   `json:"offsetX"`
	OffsetY   float64   `json:"offsetY"`
	Gain      float64   `json:"gain"`
	Epoch     int       `json:"epoch"`
	HasBias   bool      `json:"hasBias"`
}

// AdaptOut describes adaptation to the current area.
type AdaptOut struct {
	EpochID  int          `json:"epochId"`
	Since    time.Time    `json:"since"`
	Hits     int          `json:"hits"`
	Chunks   [][3]float64 `json:"chunks"` // hits so far, normErr, |offset|
	Stable   bool         `json:"stable"`
	Progress float64      `json:"progress"`
	Needed   int          `json:"needed"`
	Sessions int          `json:"sessions"`
	Days     int          `json:"days"`
}

// ProgressOut is the progress page.
type ProgressOut struct {
	Sessions   []SessionOut `json:"sessions"`
	Adaptation AdaptOut     `json:"adaptation"`
	Warmup     [][3]float64 `json:"warmup"` // minute, normErr, n
}

// Report is the full analysis.
type Report struct {
	missModel   *MissModel
	GeneratedAt time.Time      `json:"generatedAt"`
	Ready       bool           `json:"ready"`
	Totals      Totals         `json:"totals"`
	Coach       CoachOut       `json:"coach"`
	Areas       AreasOut       `json:"areas"`
	Aim         AimOut         `json:"aim"`
	AimEpochs   []AimOut       `json:"aimEpochs"`
	Consistency ConsistencyOut `json:"consistency"`
	Progress    ProgressOut    `json:"progress"`
}

// Totals are headline counts.
type Totals struct {
	Plays      int       `json:"plays"`
	Hits       int       `json:"hits"`
	AimHits    int       `json:"aimHits"`
	Sessions   int       `json:"sessions"`
	EpochHits  int       `json:"epochHits"`
	EpochPlays int       `json:"epochPlays"`
	First      time.Time `json:"first"`
	Last       time.Time `json:"last"`
	MissPer1k  float64   `json:"missPer1k"`
	NormErr    float64   `json:"normErr"`
}

const (
	minDiagHits  = 1500
	minAdaptHits = 2200
	adaptChunk   = 400
)

// Analyze runs every model and builds the report.
func Analyze(in Input) *Report {
	rep := &Report{GeneratedAt: in.Now}
	rng := rand.New(rand.NewSource(7))
	sessions, sessOf := BuildSessions(in.Plays)
	all := BuildSamples(in.Plays, sessOf)
	ptr := make([]*Sample, len(all))
	for i := range all {
		ptr[i] = &all[i]
	}
	rep.Totals.Plays = len(in.Plays)
	rep.Totals.Hits = len(all)
	rep.Totals.Sessions = len(sessions)
	if len(in.Plays) > 0 {
		first, last := in.Plays[0].Time, in.Plays[0].Time
		for _, p := range in.Plays {
			if p.Time.Before(first) {
				first = p.Time
			}
			if p.Time.After(last) {
				last = p.Time
			}
		}
		rep.Totals.First, rep.Totals.Last = first, last
	}
	allMetrics := catMetrics(ptr, nil)
	rep.Totals.AimHits = allMetrics[NumCats].Aimed
	rep.Totals.MissPer1k = allMetrics[NumCats].MissPer1k
	rep.Totals.NormErr = allMetrics[NumCats].NormErr

	cur := in.Current
	if cur == nil || !cur.Setup.Valid() {
		rep.Coach = CoachOut{Phase: "setup", PhaseIndex: -1, Title: "Setup missing",
			Message: "I can't find the OpenTabletDriver area or the osu! resolution. Check the Settings."}
		rep.Progress = progressOut(in, sessions, ptr, nil, nil)
		return rep
	}
	setup := cur.Setup
	mapper := setup.Mapper()
	rep.Areas = AreasOut{Current: setup.Area, TabletW: setup.TabletW, TabletH: setup.TabletH, TabletName: setup.TabletName,
		Playfield: mapper.PlayfieldCorners()}

	// samples of the current area, in time order
	var epochS []*Sample
	epochPlays := map[int]bool{}
	for _, s := range ptr {
		if s.Epoch == cur.ID && s.HasMM {
			epochS = append(epochS, s)
			epochPlays[s.Play] = true
		}
	}
	sort.SliceStable(epochS, func(i, j int) bool { return epochS[i].When < epochS[j].When })
	epochAim := 0
	for _, s := range epochS {
		if isAimHit(s) {
			epochAim++
		}
	}
	rep.Totals.EpochHits = epochAim
	rep.Totals.EpochPlays = len(epochPlays)

	// epochs for the drawing
	hitsByEpoch := map[int]int{}
	for _, s := range ptr {
		if isAimHit(s) {
			hitsByEpoch[s.Epoch]++
		}
	}
	for _, e := range in.Epochs {
		rep.Areas.Epochs = append(rep.Areas.Epochs, EpochArea{ID: e.ID, Area: e.Setup.Area, From: e.From, To: e.To,
			Hits: hitsByEpoch[e.ID], Current: e.ID == cur.ID})
	}
	for _, e := range in.periods() {
		rep.Areas.Periods = append(rep.Areas.Periods, EpochArea{ID: e.ID, Area: e.Setup.Area, From: e.From, To: e.To, Current: e.To == nil})
	}

	// per-area fits: each area has its own systematic error (the hand adapts
	// to it), while the scatter of the hand is pooled across all areas
	adapt := adaptation(cur, epochS)
	eds := buildEpochData(in, ptr, cur, adapt)
	ced := eds[cur.ID]
	if ced == nil {
		ced = &epochData{info: *cur}
	}
	bias := ced.bias
	boot := ced.boot
	set := ced.set
	fitS := ced.fitS
	// aim diagnostics, for every area with enough data (current first)
	order := make([]*epochData, 0, len(eds))
	for _, ed := range eds {
		order = append(order, ed)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].info.ID == cur.ID {
			return true
		}
		if order[j].info.ID == cur.ID {
			return false
		}
		return order[i].info.From.After(order[j].info.From)
	})
	for _, ed := range order {
		if !ed.bias.OK() || ed.aim < 300 {
			continue
		}
		max := 700
		if ed.info.ID == cur.ID {
			max = 1400
		}
		a := aimOut(ed.fitS, &ed.bias, ed.boot, ed.set, rng, max)
		a.EpochID, a.Area, a.From, a.To, a.Current = ed.info.ID, ed.info.Setup.Area, ed.info.From, ed.info.To, ed.info.ID == cur.ID
		rep.AimEpochs = append(rep.AimEpochs, a)
	}
	if len(rep.AimEpochs) > 0 {
		rep.Aim = rep.AimEpochs[0]
	} else {
		rep.Aim = aimOut(fitS, &bias, boot, set, rng, 0)
	}

	// consistency: scatter pooled over all areas
	biasFor := func(s *Sample) *BiasFit {
		if ed := eds[s.Epoch]; ed != nil {
			return &ed.bias
		}
		return nil
	}
	var pooled []*Sample
	areasUsed := 0
	for _, ed := range eds {
		if ed.bias.OK() {
			pooled = append(pooled, ed.fitS...)
			areasUsed++
		}
	}
	nf := FitNoise(pooled, biasFor, func(s *Sample) int { return s.Play }, 20, 13)
	var recent []*Sample
	cut := in.Now.Add(-120 * 24 * time.Hour).Unix()
	for _, s := range ptr {
		if s.When >= float64(cut) {
			recent = append(recent, s)
		}
	}
	if len(recent) < 2000 {
		recent = ptr
	}
	shares := catShares(recent)
	weights := shares
	if in.Weights != nil {
		weights = *in.Weights
	}
	// what all replays, with all their areas, say about the hand
	ca := crossArea(in, ptr, eds, cur.ID, setup.Area.W, rng)
	cons := ConsistencyOut{}
	var sweep SweepResult
	rec := &RecOut{Areas: ca.Areas, Plays: len(in.Plays), Hits: rep.Totals.AimHits}
	if nf.Model.OK() {
		sweep = recommend(rep, in, cur, eds, ca, nf, recent, weights, rec, epochAim, len(epochPlays))
		obsS := fitS
		if ced.aim < 500 {
			obsS = pooled
		}
		cons = consistencyOut(nf, sweep, shares, weights, setup, catMetrics(obsS, rng), rec.Ref)
		cons.Calibration = calibration(obsS, biasFor, nf.Model)
		cons.ByCS = rec.byCS
		cons.NoiseHits = nf.Model.N
		cons.NoiseAreas = areasUsed
		cons.LambdaKnown = ca.NaturalKnown
		cons.Size = rec.Decision
		cons.Stable = rec.Kept || rec.Testing
		rep.Areas.Rec = rec
	}
	cons.Cross = ca
	cons.Epochs = epochCompare(in, cur.ID, ptr, sessOf, eds, rng)
	cons.Insights = insights(cons, nf, rep.Areas, cur.Setup.Area)
	rep.Consistency = cons

	rep.Progress = progressOut(in, sessions, ptr, &adapt, cur)
	rep.Coach = coach(in, cur, epochAim, rep, cons)
	rep.Ready = bias.OK() || len(rep.AimEpochs) > 0 || rep.Areas.Final != nil
	return rep
}

func roundRect(r otd.Rect) otd.Rect {
	return otd.Rect{W: round(r.W, 2), H: round(r.H, 2), X: round(r.X, 2), Y: round(r.Y, 2), Rot: round(r.Rot, 2)}
}

func catShares(samples []*Sample) [NumCats]float64 {
	var n [NumCats]float64
	tot := 0.0
	for _, s := range samples {
		if s.Cat == CatOther {
			continue
		}
		n[s.Cat]++
		tot++
	}
	if tot == 0 {
		return [NumCats]float64{0.25, 0.25, 0.25, 0.25, 0}
	}
	for c := range n {
		n[c] /= tot
	}
	return n
}

func aimOut(samples []*Sample, bias *BiasFit, boot BiasBoot, set AreaSet, rng *rand.Rand, maxScatter int) AimOut {
	a := AimOut{}
	m := catMetrics(samples, nil)
	a.Hits = m[NumCats].Aimed
	a.NormErr = m[NumCats].NormErr
	a.Inside = m[NumCats].Inside
	if !bias.OK() {
		return a
	}
	a.OffsetX, a.OffsetY = round(bias.D[0], 3), round(bias.D[1], 3)
	a.OffsetErr = round(math.Hypot(boot.DX, boot.DY), 3)
	a.JumpGain = round(100*bias.JumpGain(), 2)
	a.JumpGainErr = round(100*boot.JumpGain, 2)
	a.PosGain = round(100*bias.PositionGain(), 2)
	a.Rotation = round(set.Rotation, 2)
	a.RotationErr = round(boot.Rotation, 2)
	a.Scale = round(bias.Scale, 3)
	a.Field = errorField(samples, 6)
	a.Distance = distanceCurve(samples, bias.D)
	a.Directions = directionGains(samples, bias.D)
	a.Drift = driftWithinPlay(samples)
	if maxScatter > 0 {
		a.Scatter = scatterPoints(samples, maxScatter, rng)
	}
	return a
}

func consistencyOut(nf NoiseFit, sw SweepResult, shares, weights [NumCats]float64, setup Setup, obs [NumCats + 1]CatMetrics, ref float64) ConsistencyOut {
	m := nf.Model
	c := ConsistencyOut{Ready: true, Steadiness: round(m.APar, 3), DistTerm: round(100*m.BPar, 2), SpeedCoef: round(m.CPar, 3),
		SpeedExp: round(m.P, 2), VMax: round(m.VMax, 3), Scales: sw.Scales, Best: sw.Best, BestLo: sw.BestLo, BestHi: sw.BestHi,
		TrustLo: sw.TrustLo, TrustHi: sw.TrustHi,
		BestW: round(sw.Best*ref, 2), BestLoW: round(sw.BestLo*ref, 2), BestHiW: round(sw.BestHi*ref, 2),
		TrustLoW: round(sw.TrustLo*ref, 2), TrustHiW: round(sw.TrustHi*ref, 2)}
	for _, s := range sw.Scales {
		c.Widths = append(c.Widths, round(ref*s, 2))
	}
	c.Combined = roundSlice(sw.Combined, 4)
	c.CombLo = roundSlice(sw.CombLo, 4)
	c.CombHi = roundSlice(sw.CombHi, 4)
	for k := 0; k < NumCats; k++ {
		if k == CatOther {
			continue
		}
		co := CatOut{Key: CatKeys[k], Name: CatNames[k], Share: round(shares[k], 3), Weight: round(weights[k], 3), Current: obs[k],
			Curve: roundSlice(sw.ByCat[k], 4), Lo: roundSlice(sw.Lo[k], 4), Hi: roundSlice(sw.Hi[k], 4)}
		co.BestS = BestScale(sw.Scales, sw.ByCat[k], sw.TrustLo, sw.TrustHi, sw.Anchor)
		co.BestW = round(co.BestS*ref, 2)
		if sw.Counts[k] < 50 {
			co.Curve, co.Lo, co.Hi = nil, nil, nil
			co.BestS, co.BestW = 0, 0
		}
		c.Categories = append(c.Categories, co)
	}
	// sigma vs speed at a typical distance (40 mm)
	vTop := math.Max(0.3, m.VMax*1.6)
	for v := 0.0; v <= vTop+1e-9; v += vTop / 30 {
		sp, sq := m.Sigma(40, v)
		c.SigmaCurve = append(c.SigmaCurve, [3]float64{round(v, 2), round(sp, 3), round(sq, 3)})
	}
	return c
}

func roundSlice(xs []float64, d int) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		if finite(x) {
			out[i] = round(x, d)
		} else {
			out[i] = -1
		}
	}
	return out
}

// calibration compares predicted and observed in-circle rates (current area).
func calibration(samples []*Sample, biasFor func(*Sample) *BiasFit, m NoiseModel) [][3]float64 {
	type pr struct{ p, o float64 }
	var list []pr
	for _, s := range samples {
		if !s.HasMM || !isAimHit(s) || !s.HasOrigin() || !finite(s.DtReal) || s.DtReal <= 0 {
			continue
		}
		bias := biasFor(s)
		if bias == nil || !bias.OK() {
			continue
		}
		bx, by := bias.Predict(s)
		mx, my := s.Tx-s.Ox, s.Ty-s.Oy
		D := math.Hypot(mx, my)
		mu := 0.0
		if D >= 1.5 {
			mu = (bx*mx + by*my) / D
		}
		sp, sq := m.Sigma(D, D/s.DtReal)
		p := hitProbability(s.Rmm, sp, sq, mu)
		o := 0.0
		if math.Hypot(s.Cx-s.Tx, s.Cy-s.Ty) <= s.Rmm {
			o = 1
		}
		list = append(list, pr{p, o})
	}
	if len(list) < 200 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].p < list[j].p })
	bins := 8
	var out [][3]float64
	for b := 0; b < bins; b++ {
		lo := b * len(list) / bins
		hi := (b + 1) * len(list) / bins
		sp, so := 0.0, 0.0
		for _, x := range list[lo:hi] {
			sp += x.p
			so += x.o
		}
		n := float64(hi - lo)
		out = append(out, [3]float64{round(sp/n, 4), round(so/n, 4), n})
	}
	return out
}

func epochCompare(in Input, curID int, samples []*Sample, sessOf map[int]int, eds map[int]*epochData, rng *rand.Rand) []EpochCompare {
	by := map[int][]*Sample{}
	for _, s := range samples {
		by[s.Epoch] = append(by[s.Epoch], s)
	}
	mapOf := map[int]string{}
	for _, p := range in.Plays {
		mapOf[p.ID] = p.MapKey
	}
	// per-map error of every area
	type acc struct{ sum, n float64 }
	perMap := map[int]map[string]*acc{}
	for e, ss := range by {
		byPlay := map[int][]float64{}
		for _, s := range ss {
			if isAimHit(s) {
				byPlay[s.Play] = append(byPlay[s.Play], math.Hypot(s.ENx, s.ENy))
			}
		}
		m := map[string]*acc{}
		for pid, errs := range byPlay {
			key := mapOf[pid]
			if key == "" || len(errs) < 30 {
				continue
			}
			a := m[key]
			if a == nil {
				a = &acc{}
				m[key] = a
			}
			a.sum += robustRMS(errs) * float64(len(errs))
			a.n += float64(len(errs))
		}
		perMap[e] = m
	}
	paired := func(other int) *PairedCmp {
		a, b := perMap[other], perMap[curID]
		var xs, ys []float64
		for k, va := range a {
			if vb, ok := b[k]; ok {
				xs = append(xs, va.sum/va.n)
				ys = append(ys, vb.sum/vb.n)
			}
		}
		if len(xs) < 2 {
			return nil
		}
		mean := func(v []float64, idx []int) float64 {
			t := 0.0
			for _, i := range idx {
				t += v[i]
			}
			return t / float64(len(idx))
		}
		all := make([]int, len(xs))
		for i := range all {
			all[i] = i
		}
		mo, mc := mean(xs, all), mean(ys, all)
		pc := &PairedCmp{Maps: len(xs), Other: round(mo, 4), Cur: round(mc, 4), Change: round(mc/mo-1, 4)}
		var reps []float64
		idx := make([]int, len(xs))
		for r := 0; r < 300; r++ {
			for i := range idx {
				idx[i] = rng.Intn(len(xs))
			}
			reps = append(reps, mean(ys, idx)/mean(xs, idx)-1)
		}
		pc.Lo, pc.Hi = round(quantile(reps, 0.025), 4), round(quantile(reps, 0.975), 4)
		return pc
	}
	var out []EpochCompare
	add := func(id int, info *EpochInfo) {
		ss := by[id]
		m := catMetrics(ss, rng)
		if m[NumCats].Aimed < 150 {
			return
		}
		plays, sess := map[int]bool{}, map[int]bool{}
		for _, s := range ss {
			plays[s.Play] = true
			sess[sessOf[s.Play]] = true
		}
		ec := EpochCompare{ID: id, Hits: m[NumCats].Aimed, Plays: len(plays), Sessions: len(sess), ByCat: m, Current: id == curID}
		if info != nil {
			ec.Area, ec.From, ec.To = info.Setup.Area, info.From, info.To
		} else {
			ec.Unknown = true
			first := math.Inf(1)
			for _, s := range ss {
				first = math.Min(first, s.When)
			}
			ec.From = time.Unix(int64(first), 0)
		}
		if ed := eds[id]; ed != nil && ed.bias.OK() {
			ec.HasBias = true
			ec.Lambda = round(ed.set.Lambda, 4)
			ec.LambdaErr = round(ed.boot.Lambda, 4)
			ec.NaturalW = round(ed.set.Lambda*ed.info.Setup.Area.W, 1)
			ec.JumpGain = round(100*ed.bias.JumpGain(), 2)
			ec.Offset = round(math.Hypot(ed.bias.D[0], ed.bias.D[1]), 2)
		}
		if id != curID {
			ec.SameMaps = paired(id)
		}
		out = append(out, ec)
	}
	add(0, nil)
	for i := range in.Epochs {
		add(in.Epochs[i].ID, &in.Epochs[i])
	}
	return out
}

// epochData holds the per-area fits.
type epochData struct {
	info    EpochInfo
	samples []*Sample
	fitS    []*Sample
	bias    BiasFit
	boot    BiasBoot
	set     AreaSet
	aim     int
}

func buildEpochData(in Input, samples []*Sample, cur *EpochInfo, adapt AdaptOut) map[int]*epochData {
	eds := map[int]*epochData{}
	infos := map[int]EpochInfo{}
	for _, e := range in.Epochs {
		infos[e.ID] = e
	}
	for _, s := range samples {
		if s.Epoch <= 0 || !s.HasMM {
			continue
		}
		ed := eds[s.Epoch]
		if ed == nil {
			info, ok := infos[s.Epoch]
			if !ok {
				continue
			}
			ed = &epochData{info: info}
			eds[s.Epoch] = ed
		}
		ed.samples = append(ed.samples, s)
	}
	for id, ed := range eds {
		sort.SliceStable(ed.samples, func(i, j int) bool { return ed.samples[i].When < ed.samples[j].When })
		for _, s := range ed.samples {
			if isAimHit(s) {
				ed.aim++
			}
		}
		ed.fitS = ed.samples
		isCur := cur != nil && id == cur.ID
		if !ed.info.Established && !ed.info.Adapted && len(ed.samples) > 0 {
			// skip the early part of an adaptation, when the hand was still
			// getting used to the area
			if (isCur && adapt.Stable) || (!isCur && ed.aim >= minAdaptHits) {
				ed.fitS = ed.samples[len(ed.samples)*35/100:]
			}
		}
		sessions := map[int]bool{}
		latest := 0.0
		for _, s := range ed.fitS {
			sessions[s.Session] = true
			latest = math.Max(latest, s.When)
		}
		clusterBy := func(s *Sample) int { return s.Play }
		if len(sessions) >= 6 {
			clusterBy = func(s *Sample) int { return s.Session }
		}
		var weight func(*Sample) float64
		if isCur {
			weight = func(s *Sample) float64 { return math.Exp(-(latest - s.When) / 86400 / 45) }
		}
		ed.bias = FitBias(ed.fitS, weight, clusterBy)
		if ed.bias.OK() {
			ed.set = ed.bias.Derive(ed.info.Setup.Area)
			reps := 60
			if isCur {
				reps = 150
			}
			ed.boot = ed.bias.Bootstrap(ed.info.Setup.Area, reps, int64(11+id))
		}
	}
	return eds
}

func adaptation(cur *EpochInfo, epochS []*Sample) AdaptOut {
	a := AdaptOut{EpochID: cur.ID, Since: cur.From, Needed: minAdaptHits}
	var aim []*Sample
	for _, s := range epochS {
		if isAimHit(s) {
			aim = append(aim, s)
		}
	}
	a.Hits = len(aim)
	for k := 0; k+adaptChunk <= len(aim); k += adaptChunk {
		var errs, ex, ey []float64
		for _, s := range aim[k : k+adaptChunk] {
			errs = append(errs, math.Hypot(s.ENx, s.ENy))
			ex = append(ex, s.Cx-s.Tx)
			ey = append(ey, s.Cy-s.Ty)
		}
		a.Chunks = append(a.Chunks, [3]float64{float64(k + adaptChunk), round(robustRMS(errs), 4), round(math.Hypot(trimmedMean(ex), trimmedMean(ey)), 3)})
	}
	if cur.Established || cur.Adapted {
		a.Stable = true
		a.Progress = 1
		return a
	}
	// getting used to an area takes more than a few maps: it needs several
	// sessions on at least two different days (sleep consolidates it)
	sessions, days := map[int]bool{}, map[string]bool{}
	for _, s := range aim {
		sessions[s.Session] = true
		days[time.Unix(int64(s.When), 0).Format("2006-01-02")] = true
	}
	a.Sessions, a.Days = len(sessions), len(days)
	n := len(a.Chunks)
	if a.Hits >= minAdaptHits && n >= 4 && a.Sessions >= 3 && a.Days >= 2 {
		recent := (a.Chunks[n-1][1] + a.Chunks[n-2][1]) / 2
		before := (a.Chunks[n-3][1] + a.Chunks[n-4][1]) / 2
		if before > 0 && (before-recent)/before < 0.04 {
			a.Stable = true
		}
	}
	if a.Stable {
		a.Progress = 1
	} else {
		hp := math.Min(1, float64(a.Hits)/float64(minAdaptHits+adaptChunk*2))
		sp := math.Min(1, float64(a.Sessions)/3)
		dp := math.Min(1, float64(a.Days)/2)
		a.Progress = math.Min(0.95, (hp+sp+dp)/3)
	}
	return a
}

func progressOut(in Input, sessions []Session, samples []*Sample, adapt *AdaptOut, cur *EpochInfo) ProgressOut {
	var p ProgressOut
	bySess := map[int][]*Sample{}
	for _, s := range samples {
		bySess[s.Session] = append(bySess[s.Session], s)
	}
	for _, se := range sessions {
		ss := bySess[se.ID]
		m := catMetrics(ss, nil)
		if m[NumCats].Aimed < 80 {
			continue
		}
		so := SessionOut{ID: se.ID, Start: se.Start, Plays: len(se.Plays), Hits: m[NumCats].Aimed, NormErr: round(m[NumCats].NormErr, 4),
			MissPer1k: round(m[NumCats].MissPer1k, 2), Inside: round(m[NumCats].Inside, 4)}
		epochCount := map[int]int{}
		for _, s := range ss {
			epochCount[s.Epoch]++
		}
		best, bn := 0, 0
		for e, n := range epochCount {
			if n > bn {
				best, bn = e, n
			}
		}
		so.Epoch = best
		var mm []*Sample
		for _, s := range ss {
			if s.HasMM && s.Epoch == best {
				mm = append(mm, s)
			}
		}
		if len(mm) >= 150 {
			bf := FitBias(mm, nil, func(s *Sample) int { return s.Play })
			if bf.OK() {
				so.HasBias = true
				so.OffsetX, so.OffsetY = round(bf.D[0], 3), round(bf.D[1], 3)
				L := bf.L()
				so.Gain = round(100*((L[0][0]+L[1][1])/2-1), 2)
			}
		}
		p.Sessions = append(p.Sessions, so)
	}
	if adapt != nil {
		p.Adaptation = *adapt
	}
	// warm-up: error by minute since session start
	var buckets [12][]float64
	for _, se := range sessions {
		start := float64(se.Start.Unix())
		for _, s := range bySess[se.ID] {
			if !isAimHit(s) {
				continue
			}
			k := int((s.When - start) / 300) // 5-minute buckets
			if k >= 0 && k < len(buckets) {
				buckets[k] = append(buckets[k], math.Hypot(s.ENx, s.ENy))
			}
		}
	}
	for k := range buckets {
		if len(buckets[k]) >= 200 {
			p.Warmup = append(p.Warmup, [3]float64{float64(k * 5), round(robustRMS(buckets[k]), 4), float64(len(buckets[k]))})
		}
	}
	return p
}
