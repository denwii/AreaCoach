package analysis

import (
	"math"
	"math/rand"

	"areacoach/internal/otd"
)

// Rules of the recommendation.
const (
	// an area counts as tried when it has this much data
	triedHits  = 1000
	triedPlays = 3
	// the advice never goes further than this beyond the widths tried
	exploreStep = 0.06
	// a recommended area is tried for this long before it is judged again
	testPlays = 4
	testHits  = 1500
)

// RecCat is one kind of pattern under the recommended area.
type RecCat struct {
	Key       string  `json:"key"`
	Name      string  `json:"name"`
	Share     float64 `json:"share"`
	Weight    float64 `json:"weight"`
	BestW     float64 `json:"bestW"`
	CurMiss   float64 `json:"curMiss"` // predicted aim misses per 1000 notes (-1: unknown)
	FinalMiss float64 `json:"finalMiss"`
}

// RecOut explains how the recommended areas were obtained.
type RecOut struct {
	Decision    *SizeDecision `json:"decision"`
	Ref         float64       `json:"-"`
	byCS        []CSBest
	ExploredLo  float64   `json:"exploredLo"` // range of widths actually played enough
	ExploredHi  float64   `json:"exploredHi"`
	Tried       []float64 `json:"tried"`
	Edge        string    `json:"edge"` // "up"/"down": stopped where the widths tried end
	SpeedCapped bool      `json:"speedCapped"`
	Testing     bool      `json:"testing"` // the recommended area is being tried: frozen
	TestPlays   int       `json:"testPlays"`
	TestHits    int       `json:"testHits"`
	NeedPlays   int       `json:"needPlays"`
	NeedHits    int       `json:"needHits"`
	Kept        bool      `json:"kept"` // same as last time: the change is within the uncertainty
	Areas       int       `json:"areas"`
	Plays       int       `json:"plays"`
	Hits        int       `json:"hits"`
	CurMiss     float64   `json:"curMiss"`
	FinalMiss   float64   `json:"finalMiss"`
	ByCat       []RecCat  `json:"byCat"`
	FreeRot     bool      `json:"freeRot"`  // the free area is also rotated
	Centered    bool      `json:"centered"` // the current area is already in the right place
	SameAsFinal bool      `json:"sameAsFinal"`
}

// recommend computes the three recommended areas. Every width is absolute:
// the prediction points are measured at the current width only as a unit,
// so setting another area in the driver does not change the advice; only
// new replays do.
func recommend(rep *Report, in Input, cur *EpochInfo, eds map[int]*epochData, ca CrossArea, nf NoiseFit,
	recent []*Sample, weights [NumCats]float64, rec *RecOut, epochAim, epochPlays int) SweepResult {
	setup := cur.Setup
	// a fixed reference width: the prediction points are measured in units
	// that do not depend on the area set in the driver
	const ref = 100.0
	rec.Ref = ref

	// widths actually played enough, and the one used most
	lo, hi := math.Inf(1), math.Inf(-1)
	alo, ahi := math.Inf(1), math.Inf(-1)
	for _, ed := range eds {
		plays := map[int]bool{}
		for _, s := range ed.samples {
			plays[s.Play] = true
		}
		w := ed.info.Setup.Area.W
		if ed.aim >= 300 {
			alo, ahi = math.Min(alo, w), math.Max(ahi, w)
		}
		if ed.aim >= triedHits && len(plays) >= triedPlays {
			lo, hi = math.Min(lo, w), math.Max(hi, w)
			rec.Tried = append(rec.Tried, round(w, 2))
		}
	}
	if math.IsInf(lo, 1) {
		lo, hi = alo, ahi
	}
	if math.IsInf(lo, 1) {
		lo, hi = ref, ref
	}
	rec.ExploredLo, rec.ExploredHi = round(lo, 2), round(hi, 2)

	// the grid shown in the charts, and the range the advice may use
	gLo := math.Max(lo*0.8, 20)
	gHi := math.Min(hi*1.2, setup.TabletW)
	if gHi <= gLo+1 {
		gHi = gLo + 1
	}
	// a grid of absolute widths every half millimetre, the same whatever
	// area is set in the driver
	var scales []float64
	for w := math.Floor(gLo*2) / 2; w <= gHi+1e-9; w += 0.5 {
		scales = append(scales, w/ref)
	}
	tLo := math.Max(lo*(1-exploreStep), gLo) / ref
	tHi := math.Min(hi*(1+exploreStep), gHi) / ref

	// where the hand goes, from every area
	lambda, persist := 1.0, 0.0
	var lams []float64
	if ca.NaturalKnown {
		lambda, persist = ca.NaturalW/ref, ca.Persist
		sd := ca.NaturalWErr / 1.96 / ref
		lr := rand.New(rand.NewSource(17))
		for i := 0; i < 60; i++ {
			lams = append(lams, lambda+lr.NormFloat64()*sd)
		}
	}
	// among practically equal widths: the previous advice if it is one of
	// them, otherwise the middle of the plateau
	anchor := -1.0
	ratio := setup.Area.H / setup.Area.W
	prev := in.PrevCompromise
	prevOK := prev != nil && prev.Valid() && math.Abs(prev.H/prev.W-ratio) < 0.005 && math.Abs(prev.Rot-setup.Area.Rot) < 0.01
	if prevOK && prev.W >= tLo*ref && prev.W <= tHi*ref {
		anchor = prev.W
	}
	shares := catShares(recent)
	toMM := round(setup.Mapper().MMPerPx()/setup.Area.W*ref, 9)
	pts := predPointsFrom(recent, toMM, 2500, 3)
	rep.missModel = newMissModel(nf, pts, weights, ref, setup.Area.W)
	sw := Sweep(nf, pts, SweepOpts{Scales: scales, Lambda: lambda, Persist: persist, LambdaReps: lams, Weights: weights,
		TrustLo: tLo, TrustHi: tHi, Anchor: anchorScale(anchor, ref)})
	rec.SpeedCapped = sw.SpeedCapped
	rec.byCS = sweepByCS(nf, pts, lambda, persist, weights, shares, sw)
	for i := range rec.byCS {
		rec.byCS[i].BestW = round(rec.byCS[i].BestS*ref, 2)
	}

	// model optimum, checked against the same maps played with different widths
	modelW := round(sw.Best*ref, 2)
	modelErr := round((sw.BestHi-sw.BestLo)/2*ref, 2)
	dec := decideSize(modelW, modelErr, ca, sw.TrustLo*ref, sw.TrustHi*ref)
	finalW := dec.FinalW
	if dec.Source != "evidence" {
		finalW = clamp(finalW, round(sw.TrustLo*ref, 2), round(sw.TrustHi*ref, 2))
	}
	dec.FinalW = round(finalW, 2)
	rec.Decision = &dec

	cx, cy := setup.Area.X, setup.Area.Y
	ex, ey := 0.5, 0.5
	if ca.NaturalKnown {
		cx, cy, ex, ey = ca.NaturalX, ca.NaturalY, ca.NaturalXErr, ca.NaturalYErr
	}
	final := ClampToTablet(otd.Rect{W: finalW, H: finalW * ratio, X: cx, Y: cy, Rot: setup.Area.Rot}, setup.TabletW, setup.TabletH)
	final = roundRect(final)

	// stability: while the recommended area is being tried it stays frozen;
	// afterwards it moves only when the new estimate leaves its uncertainty
	same := func(a, b otd.Rect) bool {
		return math.Abs(a.W-b.W) < 0.3 && math.Abs(a.H-b.H) < 0.3 && math.Hypot(a.X-b.X, a.Y-b.Y) < 0.5 && math.Abs(a.Rot-b.Rot) < 0.2
	}
	// the trial counts every play made with this exact area, also in other
	// periods and with other filters
	{
		plays := map[int]bool{}
		aim := 0
		for _, ed := range eds {
			if !ed.info.Setup.Area.Same(setup.Area) {
				continue
			}
			aim += ed.aim
			for _, s := range ed.samples {
				plays[s.Play] = true
			}
		}
		epochPlays, epochAim = max(epochPlays, len(plays)), max(epochAim, aim)
	}
	rec.TestPlays, rec.TestHits, rec.NeedPlays, rec.NeedHits = epochPlays, epochAim, testPlays, testHits
	if prevOK {
		// no waiting period: the advice is recomputed with every replay and
		// only kept when the new estimate is within its uncertainty
		switch {
		case math.Abs(prev.W-final.W) < math.Max(1, dec.FinalErr) &&
			math.Hypot(prev.X-final.X, prev.Y-final.Y) < math.Max(0.6, 1.5*math.Hypot(ex, ey)):
			final = *prev
			rec.Kept = true
		}
	}
	switch {
	case final.W >= hi*(1+exploreStep*0.8):
		rec.Edge = "up"
	case final.W <= lo*(1-exploreStep*0.8):
		rec.Edge = "down"
	}
	rep.Areas.Final = &AreaOut{Rect: final, ErrW: dec.FinalErr, ErrH: round(dec.FinalErr*ratio, 2), ErrX: ex, ErrY: ey,
		Fits: FitsTablet(final, setup.TabletW, setup.TabletH)}
	rec.SameAsFinal = same(setup.Area, final)

	// the same width with the hand's own proportions and rotation
	if ca.HandRatio > 0 {
		free := otd.Rect{W: final.W, H: final.W * ca.HandRatio, X: final.X, Y: final.Y, Rot: setup.Area.Rot}
		if math.Abs(ca.HandRot-setup.Area.Rot) >= math.Max(1, ca.HandRotErr) {
			free.Rot = ca.HandRot
			rec.FreeRot = true
		}
		free = roundRect(ClampToTablet(free, setup.TabletW, setup.TabletH))
		rep.Areas.Free = &AreaOut{Rect: free, ErrW: dec.FinalErr, ErrH: round(dec.FinalErr*ca.HandRatio, 2), ErrX: ex, ErrY: ey,
			ErrRot: ca.HandRotErr, Fits: FitsTablet(free, setup.TabletW, setup.TabletH)}
	}
	// the current size, only moved
	shifted := setup.Area
	if math.Hypot(setup.Area.X-final.X, setup.Area.Y-final.Y) >= math.Max(0.3, math.Hypot(ex, ey)) {
		shifted.X, shifted.Y = final.X, final.Y
		shifted = roundRect(ClampToTablet(shifted, setup.TabletW, setup.TabletH))
	} else {
		rec.Centered = true
	}
	rep.Areas.Shifted = &AreaOut{Rect: shifted, ErrX: ex, ErrY: ey, Fits: FitsTablet(shifted, setup.TabletW, setup.TabletH)}

	// predicted misses now and with the recommended width
	missAt := func(vals []float64, w float64) float64 {
		v := interp(sw.Scales, vals, w/ref)
		if !finite(v) || v < 0 {
			return -1
		}
		return round((1-v)*1000, 2)
	}
	rec.CurMiss = missAt(sw.Combined, setup.Area.W)
	rec.FinalMiss = missAt(sw.Combined, final.W)
	for k := 0; k < NumCats; k++ {
		if k == CatOther || sw.Counts[k] < 50 {
			continue
		}
		rec.ByCat = append(rec.ByCat, RecCat{Key: CatKeys[k], Name: CatNames[k], Share: round(shares[k], 3), Weight: round(weights[k], 3),
			BestW:   round(BestScale(sw.Scales, sw.ByCat[k], sw.TrustLo, sw.TrustHi, sw.Anchor)*ref, 2),
			CurMiss: missAt(sw.ByCat[k], setup.Area.W), FinalMiss: missAt(sw.ByCat[k], final.W)})
	}
	return sw
}

// interp reads a curve sampled at xs (increasing) at x; NaN outside.
func interp(xs, ys []float64, x float64) float64 {
	n := len(xs)
	if n == 0 || x < xs[0]-1e-9 || x > xs[n-1]+1e-9 {
		return math.NaN()
	}
	for i := 1; i < n; i++ {
		if x <= xs[i] {
			t := (x - xs[i-1]) / (xs[i] - xs[i-1])
			return ys[i-1] + t*(ys[i]-ys[i-1])
		}
	}
	return ys[n-1]
}

func anchorScale(w, ref float64) float64 {
	if w < 0 {
		return -1
	}
	return w / ref
}
