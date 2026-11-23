package osu

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// LazerFiles finds the replays and the maps in osu!lazer's files store. It
// keeps every file there under the SHA-256 of its content, with no
// extension, in folders named after the first one and two letters of it:
// files are told apart by how they begin. A file never changes (its name is
// its content), so each is looked at once; a folder is read again only when
// something was added to it or taken away. What was found is kept on disk.
type LazerFiles struct {
	mu        sync.Mutex
	root      string // <data>/files
	cachePath string
	c         lazerCache
	byRel     map[string]string // map file -> MD5
	pending   []string          // folders still to read
	leaves    int               // folders in the store
	dirty     bool
	savedAt   time.Time
}

type lazerFile struct {
	Size  int64 `json:"s"`
	MTime int64 `json:"t"`
}

type lazerCache struct {
	Root    string               `json:"root"`
	Dirs    map[string]int64     `json:"dirs"`    // folder -> its time when read
	Replays map[string]lazerFile `json:"replays"` // replay file -> size, time
	Maps    map[string]string    `json:"maps"`    // MD5 -> map file
	Titles  map[string]DBEntry   `json:"titles"`  // MD5 -> title of the map
	Other   map[string]bool      `json:"other"`   // the other files (first 20 letters of the name)
	Done    bool                 `json:"done"`    // the whole store was read once
}

// LazerReplay is a replay in the store.
type LazerReplay struct {
	Path  string
	Size  int64
	MTime time.Time
}

// NewLazerFiles prepares the store of the osu!lazer data folder dataDir;
// what was found before is read from cachePath.
func NewLazerFiles(dataDir, cachePath string) *LazerFiles {
	lf := &LazerFiles{root: filepath.Join(dataDir, "files"), cachePath: cachePath}
	if b, err := os.ReadFile(cachePath); err == nil {
		_ = json.Unmarshal(b, &lf.c)
	}
	if lf.c.Root != lf.root {
		lf.c = lazerCache{Root: lf.root}
	}
	if lf.c.Dirs == nil {
		lf.c.Dirs = map[string]int64{}
	}
	if lf.c.Replays == nil {
		lf.c.Replays = map[string]lazerFile{}
	}
	if lf.c.Maps == nil {
		lf.c.Maps = map[string]string{}
	}
	if lf.c.Other == nil {
		lf.c.Other = map[string]bool{}
	}
	if lf.c.Titles == nil {
		lf.c.Titles = map[string]DBEntry{}
	}
	lf.byRel = map[string]string{}
	for md, rel := range lf.c.Maps {
		lf.byRel[rel] = md
	}
	return lf
}

// Root is the files folder of the store.
func (lf *LazerFiles) Root() string { return lf.root }

func otherKey(name string) string {
	if len(name) > 20 {
		return name[:20]
	}
	return name
}

// Scan reads the folders that changed, for at most about budget; done
// reports whether the store is all read (otherwise call again).
func (lf *LazerFiles) Scan(budget time.Duration) (done bool) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	start := time.Now()
	if len(lf.pending) == 0 {
		lf.pending = lf.changed()
	}
	for len(lf.pending) > 0 && time.Since(start) < budget {
		leaf := lf.pending[0]
		lf.pending = lf.pending[1:]
		lf.readLeaf(leaf)
	}
	if len(lf.pending) == 0 && !lf.c.Done {
		lf.c.Done, lf.dirty = true, true
	}
	if lf.dirty && (len(lf.pending) == 0 || time.Since(lf.savedAt) > 20*time.Second) {
		lf.save()
	}
	return len(lf.pending) == 0
}

// changed lists the folders added to or taken from since they were read.
func (lf *LazerFiles) changed() []string {
	var out []string
	tops, err := os.ReadDir(lf.root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	n := 0
	for _, t := range tops {
		if !t.IsDir() || len(t.Name()) != 1 {
			continue
		}
		leaves, err := os.ReadDir(filepath.Join(lf.root, t.Name()))
		if err != nil {
			continue
		}
		for _, l := range leaves {
			if !l.IsDir() {
				continue
			}
			rel := t.Name() + "/" + l.Name()
			n++
			seen[rel] = true
			info, err := l.Info()
			if err != nil {
				continue
			}
			if mt, ok := lf.c.Dirs[rel]; !ok || mt != info.ModTime().UnixNano() {
				out = append(out, rel)
			}
		}
	}
	lf.leaves = n
	for rel := range lf.c.Dirs {
		if !seen[rel] {
			lf.forgetUnder(rel, nil)
			delete(lf.c.Dirs, rel)
			lf.dirty = true
		}
	}
	sort.Strings(out)
	return out
}

// forgetUnder forgets the replays and maps of a folder that are not among
// names (nil: all of them).
func (lf *LazerFiles) forgetUnder(leaf string, names map[string]bool) {
	pre := leaf + "/"
	for rel := range lf.c.Replays {
		if strings.HasPrefix(rel, pre) && !names[rel[len(pre):]] {
			delete(lf.c.Replays, rel)
			lf.dirty = true
		}
	}
	for rel, md := range lf.byRel {
		if strings.HasPrefix(rel, pre) && !names[rel[len(pre):]] {
			delete(lf.byRel, rel)
			if lf.c.Maps[md] == rel {
				delete(lf.c.Maps, md)
				delete(lf.c.Titles, md)
			}
			lf.dirty = true
		}
	}
}

func (lf *LazerFiles) readLeaf(leaf string) {
	dir := filepath.Join(lf.root, filepath.FromSlash(leaf))
	st, err := os.Stat(dir)
	if err != nil {
		return
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := map[string]bool{}
	for _, de := range ents {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		names[name] = true
		rel := leaf + "/" + name
		if _, ok := lf.c.Replays[rel]; ok {
			continue
		}
		if _, ok := lf.byRel[rel]; ok {
			continue
		}
		if lf.c.Other[otherKey(name)] {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(dir, name)
		switch kindOf(p) {
		case 'r':
			lf.c.Replays[rel] = lazerFile{Size: info.Size(), MTime: info.ModTime().UnixNano()}
		case 'o':
			if md := fileMD5(p); md != "" {
				lf.c.Maps[md] = rel
				lf.byRel[rel] = md
				if e, ok := ReadMeta(p); ok {
					lf.c.Titles[md] = DBEntry{Artist: e.Artist, ArtistU: e.ArtistU, Title: e.Title, TitleU: e.TitleU, Version: e.Version, Mode: e.Mode}
				}
			}
		case 'x':
			lf.c.Other[otherKey(name)] = true
		default:
			continue // not readable now: the next time
		}
		lf.dirty = true
	}
	lf.forgetUnder(leaf, names)
	lf.c.Dirs[leaf] = st.ModTime().UnixNano()
	lf.dirty = true
}

// kindOf tells a replay ('r') from a map ('o') or anything else ('x') by
// how the file begins; 0 if it cannot be read.
func kindOf(p string) byte {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	var b [64]byte
	n, err := io.ReadFull(f, b[:])
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return 0
	}
	h := b[:n]
	if looksLikeReplay(h) {
		return 'r'
	}
	h = bytes.TrimPrefix(h, []byte{0xEF, 0xBB, 0xBF})
	h = bytes.TrimLeft(h, " \t\r\n")
	if bytes.HasPrefix(h, []byte("osu file format v")) {
		return 'o'
	}
	return 'x'
}

// looksLikeReplay: the mode, a version, then the MD5 of the map as text.
func looksLikeReplay(h []byte) bool {
	if len(h) < 39 || h[0] > 3 {
		return false
	}
	v := int32(binary.LittleEndian.Uint32(h[1:5]))
	if v < 20000000 || v > 40000000 || h[5] != 0x0b || h[6] != 32 {
		return false
	}
	for _, c := range h[7:39] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func fileMD5(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (lf *LazerFiles) save() {
	b, err := json.Marshal(lf.c)
	if err != nil {
		return
	}
	tmp := lf.cachePath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil && os.Rename(tmp, lf.cachePath) == nil {
		lf.dirty = false
		lf.savedAt = time.Now()
	}
}

// Replays lists the replays found so far, the newest first.
func (lf *LazerFiles) Replays() []LazerReplay {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	out := make([]LazerReplay, 0, len(lf.c.Replays))
	for rel, f := range lf.c.Replays {
		out = append(out, LazerReplay{Path: filepath.Join(lf.root, filepath.FromSlash(rel)), Size: f.Size, MTime: time.Unix(0, f.MTime)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MTime.After(out[j].MTime) })
	return out
}

// Lookup is the file of the map with that MD5, checked.
func (lf *LazerFiles) Lookup(md string) (string, bool) {
	lf.mu.Lock()
	rel, ok := lf.c.Maps[md]
	lf.mu.Unlock()
	if !ok {
		return "", false
	}
	p := filepath.Join(lf.root, filepath.FromSlash(rel))
	if fileMD5(p) != md {
		return "", false
	}
	return p, true
}

// FindByTitle returns the files of the maps whose window title is t
// ("Artist - Title [Difficulty]").
func (lf *LazerFiles) FindByTitle(t string) []string {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return matchTitle(t, func(yield func(DBEntry, string)) {
		for md, e := range lf.c.Titles {
			if rel, ok := lf.c.Maps[md]; ok {
				yield(e, filepath.Join(lf.root, filepath.FromSlash(rel)))
			}
		}
	})
}

// Counts is how many maps and replays were found, and how far the first
// reading of the store got (0-1).
func (lf *LazerFiles) Counts() (maps, replays int, progress float64) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	progress = 1
	if !lf.c.Done && lf.leaves > 0 {
		progress = float64(lf.leaves-len(lf.pending)) / float64(lf.leaves)
	}
	return len(lf.c.Maps), len(lf.c.Replays), progress
}

// Done reports whether the whole store was read once.
func (lf *LazerFiles) Done() bool {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return lf.c.Done
}
