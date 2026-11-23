package osu

import "testing"

func TestOsuDBRoundTrip(t *testing.T) {
	entries := []DBEntry{
		{MD5: "0123456789abcdef0123456789abcdef", Folder: "1 A - B", File: "a.osu", Artist: "A", Title: "B", Version: "Hard", Creator: "C"},
		{MD5: "fedcba9876543210fedcba9876543210", Folder: "2 C - D", File: "c.osu", Artist: "C", Title: "D", Version: "Insane", Creator: "E", Mode: 0},
	}
	for _, v := range []int32{20250107, 20240000, 20150203, 20120101} {
		b := EncodeOsuDB(v, "player", entries)
		got, err := parseOsuDB(b)
		if err != nil {
			t.Fatalf("version %d: %v", v, err)
		}
		if len(got) != 2 || got[entries[1].MD5].Folder != "2 C - D" || got[entries[0].MD5].File != "a.osu" {
			t.Fatalf("version %d: bad result %+v", v, got)
		}
	}
}

func TestReplayRoundTrip(t *testing.T) {
	frames := []Frame{{T: 100, X: 10, Y: 20, Keys: 0}, {T: 116, X: 11.5, Y: 21.25, Keys: 5}, {T: 132, X: 12, Y: 22, Keys: 0}}
	b, err := EncodeReplay(ReplayHeader{Mode: 0, Version: 20250101, BeatmapMD5: "0123456789abcdef0123456789abcdef", Player: "p", ReplayMD5: "x", N300: 3, Mods: ModHardRock}, frames)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseReplay(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Frames) != 3 || r.Frames[1].T != 116 || r.Frames[1].Keys != 5 || r.Mods != ModHardRock {
		t.Fatalf("bad replay: %+v", r.Frames)
	}
}

func TestRealReplays(t *testing.T) {
	for _, f := range []string{"replay.osr", "replay2.osr", "replay_old_replayid.osr", "lazer_standard_format.osr"} {
		r, err := ReadReplay("../../../ext/osrparse/tests/resources/" + f)
		if err != nil {
			t.Skipf("%s: %v", f, err)
		}
		if len(r.Frames) < 1000 || r.Mode != 0 {
			t.Errorf("%s: frames=%d mode=%d", f, len(r.Frames), r.Mode)
		}
		t.Logf("%s: player=%s mods=%s frames=%d first=%+v", f, r.Player, ModString(r.Mods), len(r.Frames), r.Frames[0])
	}
}
