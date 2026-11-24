package osu

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Index maps beatmap MD5 hashes (and window titles) to .osu files. It uses
// osu!.db when possible and falls back to reading the Songs folder (cached
// on disk), where the maps just downloaded are before osu!.db has them.
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
	Size  int64    `json:"s"`
	MTime int64    `json:"t"`
	MD5   string   `json:"h"`
	Meta  *DBEntry `json:"m,omitempty"`
}

// NewIndex prepares an index; call Refresh before use.
func NewIndex(osuDir, songsDir, cachePath string) *Index {
	ix := &Index{osuDir: osuDir, songsDir: songsDir, cachePath: cachePath,
		byMD5: map[string]string{}, meta: map[string]DBEntry{}, scanCache: map[string]scanEntry{}}
	if b, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(b, &ix.scanCache)
		for p, e := range ix.scanCache {
			ix.byMD5[e.MD5] = p
			if e.Meta != nil {
				ix.meta[e.MD5] = *e.Meta
			}
		}
	}
	return ix
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
		e := scanEntry{Size: info.Size(), MTime: info.ModTime().UnixNano(), MD5: h}
		if m, ok := ReadMeta(p); ok {
			m.MD5 = h
			e.Meta = &m
		}
		ix.mu.Lock()
		ix.scanCache[p] = e
		ix.byMD5[h] = p
		if _, ok := ix.meta[h]; !ok && e.Meta != nil {
			ix.meta[h] = *e.Meta
		}
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

// ScanIfOlder scans the Songs folder when the last scan is older than d.
func (ix *Index) ScanIfOlder(d time.Duration) int {
	ix.mu.RLock()
	last := ix.lastScan
	ix.mu.RUnlock()
	if time.Since(last) < d {
		return 0
	}
	return ix.Scan(true)
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
// ("Artist - Title [Difficulty]", without the "osu!  - " in front).
func (ix *Index) FindByTitle(t string) []string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return matchTitle(t, func(yield func(DBEntry, string)) {
		for md, e := range ix.meta {
			if p, ok := ix.byMD5[md]; ok {
				yield(e, p)
			}
		}
	})
}

// matchTitle picks, among the maps listed, those whose window title is t.
// When none matches exactly, it takes those whose title and difficulty end
// the window title (the part in front may be written differently).
func matchTitle(t string, each func(func(DBEntry, string))) []string {
	key := strings.ToLower(strings.TrimSpace(t))
	loose := normTitle(t)
	var out, near []string
	each(func(e DBEntry, p string) {
		if e.Mode != 0 {
			return
		}
		if TitleKey(e.Artist, e.Title, e.Version) == key || (e.ArtistU != "" && TitleKey(e.ArtistU, e.TitleU, e.Version) == key) {
			out = append(out, p)
			return
		}
		for _, ti := range []string{e.Title, e.TitleU} {
			if ti != "" && strings.HasSuffix(loose, normTitle(ti+" ["+e.Version+"]")) {
				near = append(near, p)
				return
			}
		}
	})
	if len(out) == 0 && len(near) <= 5 {
		return near
	}
	return out
}

// ReadMeta reads the title of a map from the top of its .osu file.
func ReadMeta(path string) (DBEntry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return DBEntry{}, false
	}
	defer f.Close()
	var e DBEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	section := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			section = line
			if section == "[Difficulty]" || section == "[Events]" || section == "[HitObjects]" {
				break
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch section + k {
		case "[General]Mode":
			if m, err := strconv.Atoi(v); err == nil {
				e.Mode = byte(m)
			}
		case "[Metadata]Artist":
			e.Artist = v
		case "[Metadata]ArtistUnicode":
			e.ArtistU = v
		case "[Metadata]Title":
			e.Title = v
		case "[Metadata]TitleUnicode":
			e.TitleU = v
		case "[Metadata]Version":
			e.Version = v
		case "[Metadata]Creator":
			e.Creator = v
		}
	}
	return e, e.Title != "" || e.Version != ""
}
