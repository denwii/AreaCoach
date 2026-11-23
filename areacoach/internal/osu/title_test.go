package osu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMetaAndMatchTitle(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.osu")
	body := "osu file format v14\n\n[General]\nMode: 0\n\n[Metadata]\nTitle:Kimi no Uta\nTitleUnicode:君の歌\nArtist:Some Band\nArtistUnicode:バンド\nCreator:x\nVersion:Insane\n\n[Difficulty]\nCircleSize:4\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	e, ok := ReadMeta(p)
	if !ok || e.Title != "Kimi no Uta" || e.ArtistU != "バンド" || e.Version != "Insane" || e.Mode != 0 {
		t.Fatalf("meta %+v", e)
	}
	list := func(yield func(DBEntry, string)) { yield(e, p) }
	for _, title := range []string{"Some Band - Kimi no Uta [Insane]", "バンド - 君の歌 [Insane]", "Some  Band (TV) - Kimi no Uta [Insane]"} {
		if got := matchTitle(title, list); len(got) != 1 {
			t.Fatalf("%q: %v", title, got)
		}
	}
	if got := matchTitle("Some Band - Kimi no Uta [Hard]", list); len(got) != 0 {
		t.Fatalf("wrong difficulty matched: %v", got)
	}
}
