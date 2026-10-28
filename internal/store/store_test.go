package store

import (
	"testing"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/otd"
)

func TestMergeRepeatedEpochs(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a := analysis.Setup{Area: otd.Rect{W: 80, H: 45, X: 76, Y: 47.5}, Display: otd.Rect{W: 1920, H: 1080, X: 960, Y: 540}, GameW: 1920, GameH: 1080}
	b := a
	b.GameW, b.GameH = 1600, 900 // only the game window changed
	c := a
	c.Area.W = 90
	s.StartEpoch(a, t0, "otd")
	s.StartEpoch(b, t0.Add(time.Hour), "osu")
	s.StartEpoch(c, t0.Add(2*time.Hour), "otd")
	s.StartEpoch(a, t0.Add(3*time.Hour), "otd") // back to the first area: a real change
	if n := s.MergeRepeatedEpochs(); n != 1 {
		t.Fatalf("merged %d, want 1", n)
	}
	eps := s.State().Epochs
	if len(eps) != 3 || eps[0].To == nil || !eps[0].To.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("unexpected epochs: %+v", eps)
	}
	if EpochAt(eps, t0.Add(90*time.Minute)) != eps[0].ID {
		t.Fatalf("a play of the merged period must belong to it")
	}
}

func TestProfiles(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if len(st.Profiles) != 1 || st.ActiveProfile != 1 || st.Profiles[0].Name != "Tripod standard" {
		t.Fatalf("default profile: %+v active %d", st.Profiles, st.ActiveProfile)
	}
	adv := otd.Rect{W: 91.4, H: 51.41, X: 93.08, Y: 31.89}
	s.Update(func(st *State) {
		st.LastCompromise = &adv
		st.Calib = &CalibSession{Active: false, Base: adv}
	})
	t0 := time.Now()
	id := s.CreateProfile("Palm grip")
	if err := s.SwitchProfile(id, t0); err != nil {
		t.Fatal(err)
	}
	st = s.State()
	if st.ActiveProfile != id || st.LastCompromise != nil || st.Calib != nil {
		t.Fatalf("new profile should start empty: %+v", st)
	}
	old := &PlayMeta{Time: t0.Add(-time.Hour)}
	nw := &PlayMeta{Time: t0.Add(time.Hour)}
	if st.PlayProfile(old) != 1 || st.PlayProfile(nw) != id {
		t.Fatalf("play profiles %d %d", st.PlayProfile(old), st.PlayProfile(nw))
	}
	old.Profile = id
	if st.PlayProfile(old) != id {
		t.Fatal("manual profile ignored")
	}
	if err := s.SwitchProfile(1, t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st = s.State()
	if st.LastCompromise == nil || st.LastCompromise.W != 91.4 || st.Calib == nil {
		t.Fatalf("first profile not restored: %+v", st)
	}
	later := &PlayMeta{Time: t0.Add(3 * time.Hour)}
	if st.PlayProfile(later) != 1 || st.PlayProfile(nw) != id {
		t.Fatalf("spans after switching back: %d %d", st.PlayProfile(later), st.PlayProfile(nw))
	}
}

// Only the replays saved since the first start are kept.
func TestDropPlaysBefore(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	from := s.UseFrom()
	if from.IsZero() || time.Since(from) > time.Minute {
		t.Fatalf("first start %v, want now", from)
	}
	hits := []analysis.Hit{{}}
	_ = s.AddPlay(&PlayMeta{ReplayMD5: "old", FileTime: from.Add(-48 * time.Hour)}, hits)
	_ = s.AddPlay(&PlayMeta{ReplayMD5: "older", Time: from.Add(-72 * time.Hour)}, hits)
	_ = s.AddPlay(&PlayMeta{ReplayMD5: "new", FileTime: from.Add(time.Minute)}, hits)
	if n := s.DropPlaysBefore(from); n != 2 {
		t.Fatalf("dropped %d, want 2", n)
	}
	ps := s.Plays()
	if len(ps) != 1 || ps[0].ReplayMD5 != "new" || s.HasReplay("old") {
		t.Fatalf("left %+v", ps)
	}
	if _, err := s.Hits(ps[0].ID); err != nil {
		t.Fatalf("hits of the play kept: %v", err)
	}
	// the first start is kept across restarts
	_ = s.Save()
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.UseFrom().Equal(from) {
		t.Fatalf("first start %v after a restart, want %v", s2.UseFrom(), from)
	}
}
