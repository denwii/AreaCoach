package osu

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const lazerBeatmaps = "../../../ext/lazer/osu.Game.Rulesets.Osu.Tests/Resources/Testing/Beatmaps"

type convObj struct {
	StartTime float64
	EndTime   float64
	X, Y      float64
}
type convMapping struct {
	StartTime float64
	Objects   []convObj
}

// TestLazerConversions checks stacked positions, slider ends and end times
// against osu!lazer's reference conversions (which mirror stable).
func TestLazerConversions(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(lazerBeatmaps, "*-expected-conversion.json"))
	if len(files) == 0 {
		t.Skip("reference data not available")
	}
	for _, jf := range files {
		name := strings.TrimSuffix(filepath.Base(jf), "-expected-conversion.json")
		bm, err := ParseBeatmapFile(filepath.Join(lazerBeatmaps, name+".osu"))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var exp struct{ Mappings []convMapping }
		b, _ := os.ReadFile(jf)
		if err := json.Unmarshal(b, &exp); err != nil {
			t.Fatal(err)
		}
		objs, d := bm.Objects(0)
		if len(objs) != len(exp.Mappings) {
			t.Errorf("%s: %d objects, expected %d", name, len(objs), len(exp.Mappings))
			continue
		}
		bad := 0
		for i, m := range exp.Mappings {
			o := objs[i]
			if len(m.Objects) == 0 {
				continue
			}
			h := m.Objects[0]
			sp := o.StackedPos(d.StackOffset)
			if o.Kind == KindSpinner {
				continue
			}
			if math.Abs(sp.X-h.X) > 0.05 || math.Abs(sp.Y-h.Y) > 0.05 {
				if bad < 5 {
					t.Errorf("%s obj %d @%.0f: head (%.2f,%.2f) expected (%.2f,%.2f) stack=%d", name, i, o.Time, sp.X, sp.Y, h.X, h.Y, o.StackHeight)
				}
				bad++
				continue
			}
			if o.Kind == KindSlider && len(m.Objects) > 1 {
				tail := m.Objects[len(m.Objects)-1]
				ep := o.StackedEndPos(d.StackOffset)
				if math.Abs(ep.X-tail.X) > 1.0 || math.Abs(ep.Y-tail.Y) > 1.0 {
					if bad < 5 {
						t.Errorf("%s slider %d @%.0f: end (%.2f,%.2f) expected (%.2f,%.2f)", name, i, o.Time, ep.X, ep.Y, tail.X, tail.Y)
					}
					bad++
				}
				dur := o.EndTime - o.Time
				if dur > 80 {
					want := tail.StartTime + 36
					if math.Abs(o.EndTime-want) > 2 && math.Abs(o.EndTime-tail.StartTime) > 2 {
						if bad < 5 {
							t.Errorf("%s slider %d @%.0f: end time %.1f expected ~%.1f", name, i, o.Time, o.EndTime, want)
						}
						bad++
					}
				}
			}
		}
		t.Logf("%s: %d objects, %d mismatches", name, len(objs), bad)
	}
}
