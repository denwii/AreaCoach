package osu

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Index maps beatmap MD5 hashes to .osu files. It uses osu!.db when
// possible and falls back to hashing the Songs folder (cached on disk).
type Index struct {
	mu        sync.RWMutex
	osuDir    string
	songsDir  string
	cachePath string
	dbTime    time.Time
	byMD5     map[string]string
	meta      map[string]DBEntry
	scanCache map[string]scanEntry
	lastScan  time.Time
}

type scanEntry struct {
	Size  int64  `json:"s"`
	MTime int64  `json:"t"`
	MD5   string `json:"h"`
}

// NewIndex prepares an index; call Refresh before use.
func NewIndex(osuDir, songsDir, cachePath string) *Index {
	ix := &Index{osuDir: osuDir, songsDir: songsDir, cachePath: cachePath,
		byMD5: map[string]string{}, meta: map[string]DBEntry{}, scanCache: map[string]scanEntry{}}
	if b, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(b, &ix.scanCache)
		for p, e := range ix.scanCache {
			ix.byMD5[e.MD5] = p
		}
	}
	return ix
}

// Count returns how many maps are known.
func (ix *Index) Count() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.byMD5)
}

// Refresh reloads osu!.db if it changed. Returns an error only if the
// database exists but cannot be parsed.
func (ix *Index) Refresh() error {
	dbPath := filepath.Join(ix.osuDir, "osu!.db")
	st, err := os.Stat(dbPath)
	if err != nil {
		return nil
	}
	ix.mu.RLock()
	same := st.ModTime().Equal(ix.dbTime)
	ix.mu.RUnlock()
	if same {
		return nil
	}
	entries, err := ReadOsuDB(dbPath)
	if err != nil {
		return err
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.dbTime = st.ModTime()
	for md, e := range entries {
		ix.meta[md] = e
		if e.Folder != "" && e.File != "" {
			ix.byMD5[md] = filepath.Join(ix.songsDir, e.Folder, e.File)
		}
	}
	return nil
}

// Lookup returns the .osu path for a hash, verifying the file content.
func (ix *Index) Lookup(md string) (string, bool) {
	ix.mu.RLock()
	p, ok := ix.byMD5[md]
	ix.mu.RUnlock()
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	sum := md5.Sum(b)
	if hex.EncodeToString(sum[:]) != md {
		return "", false
	}
	return p, true
}

// Scan hashes every .osu file in the Songs folder (incremental, cached).
// It is rate-limited to once every few minutes unless force is set.
func (ix *Index) Scan(force bool) int {
	ix.mu.Lock()
	if !force && time.Since(ix.lastScan) < 3*time.Minute {
		ix.mu.Unlock()
		return 0
	}
	ix.lastScan = time.Now()
	ix.mu.Unlock()
	added := 0
	seen := map[string]bool{}
	_ = filepath.WalkDir(ix.songsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".osu") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[p] = true
		ix.mu.RLock()
		c, ok := ix.scanCache[p]
		ix.mu.RUnlock()
		if ok && c.Size == info.Size() && c.MTime == info.ModTime().UnixNano() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		sum := md5.Sum(b)
		h := hex.EncodeToString(sum[:])
		ix.mu.Lock()
		ix.scanCache[p] = scanEntry{Size: info.Size(), MTime: info.ModTime().UnixNano(), MD5: h}
		ix.byMD5[h] = p
		ix.mu.Unlock()
		added++
		return nil
	})
	ix.mu.Lock()
	for p := range ix.scanCache {
		if !seen[p] {
			delete(ix.scanCache, p)
		}
	}
	data, _ := json.Marshal(ix.scanCache)
	ix.mu.Unlock()
	if ix.cachePath != "" {
		_ = os.WriteFile(ix.cachePath, data, 0o644)
	}
	return added
}

// Meta is what osu!.db says of a map, if it has it.
func (ix *Index) Meta(md string) (DBEntry, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	e, ok := ix.meta[md]
	return e, ok
}

// TitleKey is how osu! writes a map in its window title, lower case:
// "artist - title [difficulty]".
func TitleKey(artist, title, version string) string {
	return strings.ToLower(strings.TrimSpace(artist) + " - " + strings.TrimSpace(title) + " [" + strings.TrimSpace(version) + "]")
}

// normTitle lowers a title and collapses its spaces, for loose matching.
func normTitle(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// FindByTitle returns the .osu files of the maps whose window title is t
// ("Artist - Title [Difficulty]", without the "osu!  - " in front). When no
// map matches exactly, it looks for one whose title and difficulty end the
// window title (the part in front may be written differently).
func (ix *Index) FindByTitle(t string) []string {
	key := strings.ToLower(strings.TrimSpace(t))
	loose := normTitle(t)
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var out, near []string
	for md, e := range ix.meta {
		if e.Mode != 0 {
			continue
		}
		p, ok := ix.byMD5[md]
		if !ok {
			continue
		}
		if TitleKey(e.Artist, e.Title, e.Version) == key || (e.ArtistU != "" && TitleKey(e.ArtistU, e.TitleU, e.Version) == key) {
			out = append(out, p)
			continue
		}
		for _, ti := range []string{e.Title, e.TitleU} {
			if ti != "" && strings.HasSuffix(loose, normTitle(ti+" ["+e.Version+"]")) {
				near = append(near, p)
				break
			}
		}
	}
	if len(out) == 0 && len(near) <= 5 {
		return near
	}
	return out
}
