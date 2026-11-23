package osu

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func lazerReplay(t *testing.T, mods []LazerMod, legacy uint32) []byte {
	t.Helper()
	frames := []Frame{{T: 0, X: 100, Y: 100}, {T: 16, X: 110, Y: 105}, {T: 32, X: 120, Y: 110, Keys: KeyK1}}
	b, err := EncodeReplay(ReplayHeader{Mode: 0, Version: 30000016, BeatmapMD5: "0123456789abcdef0123456789abcdef", Player: "me",
		ReplayMD5: "abc", N300: 3, Mods: legacy, Timestamp: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), LazerMods: mods}, frames)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLazerReplay(t *testing.T) {
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	b := lazerReplay(t, []LazerMod{{Acronym: "DT", Settings: map[string]json.RawMessage{"speed_change": raw(1.3)}},
		{Acronym: "DA", Settings: map[string]json.RawMessage{"circle_size": raw(6.5)}}, {Acronym: "CL"}}, ModDoubleTime)
	rep, err := ParseReplay(b)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Lazer || len(rep.LazerMods) != 3 || len(rep.Frames) != 3 {
		t.Fatalf("lazer %v, mods %v, frames %d", rep.Lazer, rep.LazerMods, len(rep.Frames))
	}
	a, bad := rep.LazerAdjust()
	if bad != "" || math.Abs(a.Rate-1.3) > 1e-9 || !a.HasCS || a.CS != 6.5 || a.HasAR {
		t.Fatalf("adjust %+v %q", a, bad)
	}
	if s := rep.LazerModString(); s != "DT 1.3× DA" {
		t.Fatalf("mods %q", s)
	}
	bm := &Beatmap{CS: 4, AR: 9, OD: 8}
	d := bm.DifficultyAdj(rep.Mods, a)
	if d.CS != 6.5 || math.Abs(d.Rate-1.3) > 1e-6 {
		t.Fatalf("difficulty %+v", d)
	}
	// the default speed, and a mod that moves the circles
	rep2, _ := ParseReplay(lazerReplay(t, []LazerMod{{Acronym: "HT"}}, ModHalfTime))
	if a, _ := rep2.LazerAdjust(); a.Rate != 0.75 {
		t.Fatalf("HT rate %v", a.Rate)
	}
	rep3, _ := ParseReplay(lazerReplay(t, []LazerMod{{Acronym: "MR"}}, 0))
	if _, bad := rep3.LazerAdjust(); bad != "Mirror" {
		t.Fatalf("mirror: %q", bad)
	}
	// a replay of osu! stable has none of it
	st, _ := EncodeReplay(ReplayHeader{Mode: 0, Version: 20250101, BeatmapMD5: "0123456789abcdef0123456789abcdef", Player: "p", ReplayMD5: "x"}, []Frame{{T: 0, X: 1, Y: 1}})
	if r, err := ParseReplay(st); err != nil || r.Lazer {
		t.Fatalf("stable: %v %v", err, r.Lazer)
	}
}

func TestLazerFiles(t *testing.T) {
	dir := t.TempDir()
	put := func(name string, b []byte) string {
		p := filepath.Join(dir, "files", name[:1], name[:2], name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	osu := []byte("\xef\xbb\xbfosu file format v14\n\n[General]\nMode: 0\n")
	put("aa11", osu)
	rp := put("ab22", lazerReplay(t, nil, 0))
	put("ff33", []byte("RIFF....WAVEfmt "))
	cache := filepath.Join(dir, "cache.json")
	lf := NewLazerFiles(dir, cache)
	for i := 0; i < 10 && !lf.Scan(time.Second); i++ {
	}
	maps, reps, prog := lf.Counts()
	if maps != 1 || reps != 1 || prog != 1 || !lf.Done() {
		t.Fatalf("maps %d replays %d progress %v", maps, reps, prog)
	}
	if r := lf.Replays(); len(r) != 1 || r[0].Path != rp {
		t.Fatalf("replays %+v", r)
	}
	if _, ok := lf.Lookup(fileMD5(filepath.Join(dir, "files", "a", "aa", "aa11"))); !ok {
		t.Fatal("map not found")
	}
	// from the cache, and a replay removed
	_ = os.Remove(rp)
	later := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Dir(rp), later, later)
	lf2 := NewLazerFiles(dir, cache)
	if _, reps, _ := lf2.Counts(); reps != 1 {
		t.Fatalf("cached replays %d", reps)
	}
	lf2.Scan(time.Second)
	if _, reps, _ := lf2.Counts(); reps != 0 {
		t.Fatalf("replays after removal %d", reps)
	}
}

func TestLazerConfig(t *testing.T) {
	def := t.TempDir()
	data := t.TempDir()
	_ = os.MkdirAll(filepath.Join(data, "files"), 0o755)
	_ = os.WriteFile(filepath.Join(def, "storage.ini"), []byte("FullPath = "+data+"\n"), 0o644)
	if got := LazerDataDir(def); got != data {
		t.Fatalf("data dir %q", got)
	}
	_ = os.WriteFile(filepath.Join(def, "framework.ini"), []byte("WindowMode = Windowed\r\nWindowedSize = 1600x900\r\nWindowedPositionX = 0.25\r\nSizeFullscreen = 9999x9999\r\n"), 0o644)
	_ = os.WriteFile(filepath.Join(data, "game.ini"), []byte("Username = me\nScaling = Gameplay\nScalingSizeX = 0.5\nScalingSizeY = 0.5\nScalingPositionX = 1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(def, "input.json"), []byte(`{"InputHandlers":[{"$type":"osu.Framework.Input.Handlers.Tablet.OpenTabletDriverHandler, osu.Framework","Enabled":false}]}`), 0o644)
	c, err := ReadLazerConfig(def, data)
	if err != nil {
		t.Fatal(err)
	}
	if c.WinW != 1600 || c.WinH != 900 || c.PosX != 0.25 || c.PosY != 0.5 || c.FullW != 0 || c.Username != "me" || c.TabletInput {
		t.Fatalf("%+v", c)
	}
	w, h, x, y, ok := c.Scaled()
	if !ok || w != 0.5 || h != 0.5 || x != 0.5 || y != 0.25 {
		t.Fatalf("scaled %v %v %v %v %v", w, h, x, y, ok)
	}
}
