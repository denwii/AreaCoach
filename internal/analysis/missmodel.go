package analysis

import (
	"math"
	"sync"
)

// MissModel predicts the aim misses with any width of the area: how the pen
// scatters (fitted on all the replays, after taking out the systematic
// over/undershoot) applied to the maps the player plays, passage type by
// passage type, plus the part of the difference from the size the hand is
// used to that survives adaptation. A small area needs steadier hands (more
// misses where precision counts), a large one faster ones (more misses on
// big jumps): the best width is where the sum over all types is smallest.
type MissModel struct {
	nf      NoiseFit
	pts     []predPoint
	weights [NumCats]float64
	ref     float64
	maxW    float64 // beyond it the maps would need speeds never produced
	home    float64 // the width of the replays the model comes from

	mu    sync.Mutex
	cache map[[2]int32]missBest
	plans map[planKey]MissPlan
}

type missBest struct {
	w, miss, missNat float64 // w: the best width over natW, in the cache
	ok               bool
}

const missModelPts = 600

func newMissModel(nf NoiseFit, pts []predPoint, weights [NumCats]float64, ref, home float64) *MissModel {
	if !nf.Model.OK() || len(pts) < 200 {
		return nil
	}
	if len(pts) > missModelPts {
		pts = pts[:missModelPts] // already shuffled
	}
	m := &MissModel{nf: nf, pts: pts, weights: weights, ref: ref, home: home, cache: map[[2]int32]missBest{}}
	m.maxW = math.Inf(1)
	if vmax := nf.Model.VMax; vmax > 0 {
		for w := 20.0; w <= 300; w += 0.5 {
			over := 0
			for _, p := range pts {
				if p.V*w/ref > vmax*1.05 {
					over++
				}
			}
			if float64(over)/float64(len(pts)) > 0.06 {
				m.maxW = w - 0.5
				break
			}
		}
	}
	return m
}

// OK reports whether the model can be used.
func (m *MissModel) OK() bool { return m != nil && len(m.pts) > 0 }

// Miss is the predicted aim misses per 1000 notes with width w, for a hand
// used to natW that keeps the share persist of the difference.
func (m *MissModel) Miss(w, natW, persist float64) float64 {
	var v [NumCats]float64
	predictCat(m.nf.Model, m.pts, w/m.ref, natW/m.ref, persist, &v, nil)
	t, wt := 0.0, 0.0
	for c := 0; c < NumCats; c++ {
		if m.weights[c] > 0 && finite(v[c]) {
			t += m.weights[c] * v[c]
			wt += m.weights[c]
		}
	}
	if wt == 0 {
		return math.NaN()
	}
	return (1 - t/wt) * 1000
}

// Best returns the width between lo and hi with the fewest predicted aim
// misses (among practically equal ones, the one nearest to natW), and the
// predicted misses there and at natW.
func (m *MissModel) Best(natW, persist, lo, hi float64) (w, miss, missNat float64, ok bool) {
	if !m.OK() || !(natW > 0) {
		return natW, 0, 0, false
	}
	persist = clamp(persist, 0.05, 1)
	// worked out every half millimetre: in between, the same ratio to natW
	key := [2]int32{int32(math.Round(natW * 2)), int32(math.Round(persist * 20))}
	m.mu.Lock()
	if b, hit := m.cache[key]; hit {
		m.mu.Unlock()
		if !b.ok {
			return natW, 0, 0, false
		}
		return natW * b.w, b.miss, b.missNat, true
	}
	m.mu.Unlock()
	w0 := natW
	natW = float64(key[0]) / 2
	persist = float64(key[1]) / 20
	// near the size the hand is used to: further away the model would be
	// guessing, and the hand would need to change a lot
	lo = math.Max(lo, natW*0.8)
	hi = math.Min(math.Min(hi, natW*1.25), math.Max(m.maxW, natW))
	var scales, vals []float64
	for x := math.Ceil(lo*2) / 2; x <= hi+1e-9; x += 0.5 {
		v := m.Miss(x, natW, persist)
		if finite(v) {
			scales = append(scales, x)
			vals = append(vals, 1-v/1000)
		}
	}
	b := missBest{w: natW}
	if len(scales) >= 3 {
		b.w = BestScale(scales, vals, lo, hi, clamp(natW, lo, hi))
		b.miss = round(m.Miss(b.w, natW, persist), 2)
		b.missNat = round(m.Miss(natW, natW, persist), 2)
		b.ok = finite(b.miss)
	}
	k := b.w / natW // kept as a ratio
	m.mu.Lock()
	if len(m.cache) > 4000 {
		m.cache = map[[2]int32]missBest{}
	}
	m.cache[key] = missBest{w: k, miss: b.miss, missNat: b.missNat, ok: b.ok}
	m.mu.Unlock()
	if !b.ok {
		return w0, 0, 0, false
	}
	return w0 * k, b.miss, b.missNat, true
}

// MissModel returns the model of the aim misses by width (nil until the
// replays allow it).
func (r *Report) MissModel() *MissModel { return r.missModel }
