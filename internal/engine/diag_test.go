package engine

import (
	"strings"
	"testing"
	"time"

	"areacoach/internal/analysis"
	"areacoach/internal/store"
)

// A failed play saved with F2: used up to where it stopped, ending when it
// was failed, not when the file was written.
func TestFailedPlay(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)
	hits := []analysis.Hit{{Time: 1000}, {Time: 30000}}
	p := &store.PlayMeta{LengthMs: 120000, FileTime: t0.Add(8 * time.Second), Time: t0}
	p.Summary.LastTime = 61000
	played, failed := playedMs(p, hits)
	if !failed || played != 60000 {
		t.Errorf("played %.0f failed %v, want 60000 true", played, failed)
	}
	if end := playEnd(p); !end.Equal(t0) {
		t.Errorf("ended %v, want %v", end, t0)
	}
	p.Summary.LastTime = 121500
	if played, failed := playedMs(p, hits); failed || played != 120000 {
		t.Errorf("a passed play: played %.0f failed %v", played, failed)
	}
	// a time in the replay far from the file's (another time zone): the file's
	p.Time = t0.Add(-2 * time.Hour)
	if end := playEnd(p); !end.Equal(p.FileTime) {
		t.Errorf("ended %v, want the file time", end)
	}
}

// The map of a replay saved with F2, from its file name.
func TestReplayTitle(t *testing.T) {
	e := &Engine{}
	for name, want := range map[string]string{
		`C:\osu!\Replays\Demo - 3OH!3 - My First Kiss (feat. Ke$ha) [Hard] (2026-10-28) Osu.osr`: "3OH!3 - My First Kiss (feat. Ke$ha) [Hard]",
		`C:\osu!\Replays\Demo - xi - Blue Zenith [Extra] (2026-10-28 02-00) Osu.osr`:             "xi - Blue Zenith [Extra]",
		`C:\osu!\Data\r\0d3621b949fc23fc13ed071782ec4016-639284611270589696.osr`:                 "",
	} {
		f := replayFile{path: strings.ReplaceAll(name, `\`, "/")}
		if got := e.replayTitle(f, ""); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
