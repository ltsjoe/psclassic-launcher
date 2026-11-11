package psc

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The launcher keeps its bookkeeping in <game>/.psclassic/. This tool reads and
// writes the same files in the same format, so the two can share a game folder:
//
//	version.txt  the manifest version last installed in full, plus "\n"
//	state.txt    "hash size ticks path" per file: a hash cache, so unchanged files
//	             (same size and modification time) are not hashed again
//	download/    <hash>.part files being downloaded (resumable)
//	backup/      base-game files that an update replaced or deleted
//	beta-files.txt  paths installed from the beta branch, one per line

const StateDirName = ".psclassic"

// known is a cached hash for a file, valid while its size and mtime stay the same.
type known struct {
	Hash  string
	Size  int64
	Ticks int64
}

// state maps a lower-case manifest path to its cached hash.
type state map[string]known

// ticksAtUnixEpoch is 1970-01-01 in .NET DateTime ticks (100 ns since 0001-01-01).
const ticksAtUnixEpoch = 621355968000000000

// dotnetTicks gives a file's mtime the way .NET under Wine reports
// FileInfo.LastWriteTimeUtc.Ticks, so cached entries stay valid in both programs.
func dotnetTicks(t time.Time) int64 {
	return t.Unix()*10_000_000 + int64(t.Nanosecond()/100) + ticksAtUnixEpoch
}

func loadState(game string) (state, error) {
	st := state{}
	f, err := os.Open(filepath.Join(game, StateDirName, "state.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.SplitN(strings.TrimRight(sc.Text(), "\r"), " ", 4)
		if len(parts) != 4 {
			continue
		}
		size, err1 := strconv.ParseInt(parts[1], 10, 64)
		ticks, err2 := strconv.ParseInt(parts[2], 10, 64)
		if err1 != nil || err2 != nil {
			continue // a damaged line only costs a re-hash
		}
		st[parts[3]] = known{Hash: parts[0], Size: size, Ticks: ticks}
	}
	return st, sc.Err()
}

func saveState(game string, st state) error {
	dir := filepath.Join(game, StateDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := st[k]
		fmt.Fprintf(&b, "%s %d %d %s\r\n", v.Hash, v.Size, v.Ticks, k)
	}
	return writeFileAtomic(filepath.Join(dir, "state.txt"), []byte(b.String()))
}

func InstalledVersion(game string) string {
	b, err := os.ReadFile(filepath.Join(game, StateDirName, "version.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func WriteVersion(game, version string) error {
	dir := filepath.Join(game, StateDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, "version.txt"), []byte(version+"\n"))
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sha256File returns the lower-case hex SHA-256 of a file and its length.
func sha256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// loadBetaFiles reads .psclassic/beta-files.txt. Lines that are not safe
// relative paths are skipped, so a damaged file can never delete anything else.
func loadBetaFiles(game string) []string {
	b, err := os.ReadFile(filepath.Join(game, StateDirName, "beta-files.txt"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if p, err := checkPath(line); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// saveBetaFiles writes the list sorted without regard to case, as the launcher
// does, and removes the file when the list is empty.
func saveBetaFiles(game string, paths []string) error {
	path := filepath.Join(game, StateDirName, "beta-files.txt")
	if len(paths) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	sorted := append([]string(nil), paths...)
	sort.SliceStable(sorted, func(i, j int) bool { return strings.ToLower(sorted[i]) < strings.ToLower(sorted[j]) })
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(strings.Join(sorted, "\r\n")+"\r\n"))
}
