package psc

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotSigned uses the launcher's own wording.
var ErrNotSigned = errors.New("the update list is not signed by Planetside Classic; nothing was changed")

// retryDelays are the launcher's waits between attempts after a temporary failure.
var retryDelays = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second}

// Updater brings a game folder in line with the signed manifest.
type Updater struct {
	Source    string // base URL, e.g. https://patch.blackarmoredreapers.com
	Key       *ecdsa.PublicKey
	Game      string          // game folder on disk
	Groups    map[string]bool // optional parts to install
	Jobs      int             // parallel downloads
	Client    *http.Client
	UserAgent string
	Beta      bool // use the beta branch (beta/manifest.txt) when the server has one
	Log       func(format string, args ...any)
	Verbose   bool

	// Progress, if set, is called while Plan checks files and while Apply
	// downloads, at most every ProgressEvery (default 200 ms) and once at the end
	// of each phase. It may be called from any goroutine.
	Progress      func(Progress)
	ProgressEvery time.Duration

	mu    sync.Mutex // guards st and lastProgress
	st    state
	lastP time.Time
}

// Progress describes how far Plan or Apply has got.
type Progress struct {
	Message    string // what is happening, in the launcher's words
	Done       int64  // bytes checked or downloaded so far
	Total      int64  // bytes to check or download
	Files      int    // objects finished (Apply only)
	FilesTotal int    // objects to download (Apply only; 0 while checking)
	Start      time.Time
}

// report passes p to the Progress callback, throttled unless force is set.
func (u *Updater) report(p Progress, force bool) {
	if u.Progress == nil {
		return
	}
	every := u.ProgressEvery
	if every == 0 {
		every = 200 * time.Millisecond
	}
	u.mu.Lock()
	now := time.Now()
	if !force && now.Sub(u.lastP) < every {
		u.mu.Unlock()
		return
	}
	u.lastP = now
	u.mu.Unlock()
	u.Progress(p)
}

func (u *Updater) logf(format string, args ...any) {
	if u.Log != nil {
		u.Log(format, args...)
	}
}

// FetchManifest downloads manifest.txt and manifest.sig and checks the signature
// before anything else looks at the list. With Beta set it first tries
// beta/manifest.txt and falls back to the normal release when the server has no
// beta branch, as the launcher does; the result's FromBeta says which one it got.
func (u *Updater) FetchManifest(ctx context.Context) (*Manifest, error) {
	prefix := ""
	var body []byte
	if u.Beta {
		b, err := u.tryReadAll(ctx, "beta/manifest.txt")
		if err != nil {
			return nil, fmt.Errorf("could not download the beta update list: %w", err)
		}
		if b != nil {
			body, prefix = b, "beta/"
		}
	}
	if body == nil {
		var err error
		body, err = u.readAll(ctx, "manifest.txt")
		if err != nil {
			return nil, fmt.Errorf("could not download the update list: %w", err)
		}
	}
	sigText, err := u.readAll(ctx, prefix+"manifest.sig")
	if err != nil {
		return nil, fmt.Errorf("could not download the update list's signature: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if err != nil || !VerifySignature(u.Key, body, sig) {
		return nil, ErrNotSigned
	}
	m, err := ParseManifest(string(body))
	if err != nil {
		return nil, err
	}
	m.FromBeta = prefix != ""
	return m, nil
}

// ReadFile fetches a small file from the update server (news.txt,
// game-version.txt, ...), with retries.
func (u *Updater) ReadFile(ctx context.Context, name string) ([]byte, error) {
	return u.readAll(ctx, name)
}

// TryReadFile is ReadFile, but a file the server does not have (404 or 403)
// gives nil and no error.
func (u *Updater) TryReadFile(ctx context.Context, name string) ([]byte, error) {
	return u.tryReadAll(ctx, name)
}

func (u *Updater) tryReadAll(ctx context.Context, name string) ([]byte, error) {
	b, err := u.readAll(ctx, name)
	var he *httpError
	if errors.As(err, &he) && (he.Code == http.StatusNotFound || he.Code == http.StatusForbidden) {
		return nil, nil
	}
	return b, err
}

// readAll fetches a small file with a cache-busting query, as the launcher does.
func (u *Updater) readAll(ctx context.Context, name string) ([]byte, error) {
	var out []byte
	err := u.withRetries(ctx, name, func() error {
		url := fmt.Sprintf("%s/%s?t=%d", u.Source, name, time.Now().UnixNano()/100)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", u.UserAgent)
		resp, err := u.Client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return &httpError{resp.StatusCode, url}
		}
		out, err = io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		return err
	})
	return out, err
}

// Plan lists the entries that need work: files that are missing or differ,
// config files that are missing, and files that should be deleted.
// With save set it also stores the refreshed hash cache, as the launcher does.
func (u *Updater) Plan(m *Manifest, save bool) ([]Entry, error) {
	st, err := loadState(u.Game)
	if err != nil {
		return nil, fmt.Errorf("reading %s state: %w", StateDirName, err)
	}
	u.st = st
	eff := m.Effective(u.Groups)
	var total, done int64
	for _, e := range eff {
		if e.Kind == KindFile {
			total += e.Size
		}
	}
	start := time.Now()
	u.mu.Lock()
	u.lastP = start // the first report comes after ProgressEvery, so quick checks stay quiet
	u.mu.Unlock()
	var todo []Entry
	for _, e := range eff {
		local, err := u.local(e.Path)
		if err != nil {
			return nil, err
		}
		switch e.Kind {
		case KindDelete:
			if exists(local) {
				todo = append(todo, e)
			}
		case KindConfig:
			if !exists(local) {
				todo = append(todo, e)
			}
		default:
			u.report(Progress{Message: "Checking game files... " + e.Path, Done: done, Total: total, Start: start}, false)
			cur, err := u.isCurrent(e, local)
			if err != nil {
				return nil, err
			}
			if !cur {
				todo = append(todo, e)
			}
			done += e.Size
		}
	}
	u.report(Progress{Message: "Checking game files...", Done: total, Total: total, Start: start}, true)

	// Files an earlier beta update installed that this list does not have are
	// removed (without a backup), so leaving the beta branch gives a clean release.
	listed := listedPaths(eff)
	for _, p := range loadBetaFiles(u.Game) {
		if listed[strings.ToLower(p)] {
			continue
		}
		if local, err := u.local(p); err == nil && exists(local) {
			todo = append(todo, Entry{Kind: KindDelete, Path: p, Group: betaGroup})
		}
	}
	if save && dirExists(u.Game) {
		if err := saveState(u.Game, u.st); err != nil {
			return nil, err
		}
	}
	return todo, nil
}

// betaGroup marks deletions of files left over from the beta branch, like the
// launcher's "beta/" group: they are removed without a backup.
const betaGroup = "beta/"

// listedPaths gives the lower-case paths of the entries that put a file in place.
func listedPaths(eff []Entry) map[string]bool {
	out := map[string]bool{}
	for _, e := range eff {
		if e.Kind != KindDelete {
			out[strings.ToLower(e.Path)] = true
		}
	}
	return out
}

// isCurrent reports whether local already matches e, hashing only files whose
// size or mtime changed since they were last hashed.
func (u *Updater) isCurrent(e Entry, local string) (bool, error) {
	fi, err := os.Stat(local)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if fi.Size() != e.Size {
		return false, nil
	}
	key := strings.ToLower(e.Path)
	ticks := dotnetTicks(fi.ModTime())
	k, ok := u.st[key]
	if !ok || k.Size != fi.Size() || k.Ticks != ticks {
		if u.Verbose {
			u.logf("Checking game files... %s", e.Path)
		}
		h, _, err := sha256File(local)
		if err != nil {
			return false, err
		}
		k = known{Hash: h, Size: fi.Size(), Ticks: ticks}
		u.st[key] = k
	}
	return k.Hash == e.Hash, nil
}

// Result sums up an Apply.
type Result struct {
	Downloaded      int
	DownloadedBytes int64
	Deleted         int
}

// Apply downloads, checks and installs the planned entries, then deletes what
// should go. version.txt is written only when everything succeeded.
func (u *Updater) Apply(ctx context.Context, m *Manifest, todo []Entry) (Result, error) {
	var res Result
	if u.st == nil {
		st, err := loadState(u.Game)
		if err != nil {
			return res, err
		}
		u.st = st
	}
	if err := os.MkdirAll(filepath.Join(u.Game, StateDirName, "download"), 0o755); err != nil {
		return res, err
	}

	// One download per distinct object: identical files at several paths share a hash.
	byHash := map[string][]Entry{}
	var order []string
	var total int64
	for _, e := range todo {
		if e.Kind == KindDelete {
			continue
		}
		if _, ok := byHash[e.Hash]; !ok {
			order = append(order, e.Hash)
			total += e.Size
		}
		byHash[e.Hash] = append(byHash[e.Hash], e)
	}

	var done atomic.Int64
	var filesDone atomic.Int64
	var current atomic.Value // path of the object most recently started
	current.Store("")
	start := time.Now()
	snapshot := func() Progress {
		msg := "Downloading " + current.Load().(string)
		return Progress{Message: msg, Done: done.Load(), Total: total, Files: int(filesDone.Load()), FilesTotal: len(order), Start: start}
	}
	stopProg := make(chan struct{})
	var progWG sync.WaitGroup
	if u.Progress != nil && len(order) > 0 {
		every := u.ProgressEvery
		if every == 0 {
			every = 200 * time.Millisecond
		}
		progWG.Add(1)
		go func() {
			defer progWG.Done()
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					u.Progress(snapshot())
				case <-stopProg:
					return
				}
			}
		}()
	}
	installed := map[string]bool{} // lower-case paths installed in this run
	jobs := make(chan string)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errs []error
	workers := max(1, min(u.Jobs, len(order)))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for hash := range jobs {
				entries := byHash[hash]
				current.Store(entries[0].Path)
				if err := u.installObject(ctx, entries, &done); err != nil {
					errMu.Lock()
					errs = append(errs, err)
					errMu.Unlock()
					u.logf("FAILED %s: %v", entries[0].Path, err)
					continue
				}
				filesDone.Add(1)
				errMu.Lock()
				res.Downloaded += len(entries)
				res.DownloadedBytes += entries[0].Size
				for _, e := range entries {
					installed[strings.ToLower(e.Path)] = true
				}
				errMu.Unlock()
				if u.Verbose {
					for _, e := range entries {
						u.logf("Installed %s", e.Path)
					}
				}
			}
		}()
	}
feed:
	for _, h := range order {
		select {
		case jobs <- h:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	close(stopProg)
	progWG.Wait()
	if u.Progress != nil && len(order) > 0 {
		u.Progress(snapshot())
	}

	if ctx.Err() == nil {
		for _, e := range todo {
			if e.Kind != KindDelete {
				continue
			}
			if err := u.deleteEntry(e); err != nil {
				errs = append(errs, err)
				continue
			}
			res.Deleted++
		}
	}

	if err := saveState(u.Game, u.st); err != nil {
		errs = append(errs, err)
	}
	if err := u.updateBetaFiles(m, todo, installed); err != nil {
		errs = append(errs, err)
	}
	if err := ctx.Err(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return res, fmt.Errorf("update stopped: %w", errors.Join(errs...))
	}
	return res, WriteVersion(u.Game, m.Version)
}

// updateBetaFiles records which files came from the beta branch
// (.psclassic/beta-files.txt), so a later release update can remove the ones the
// release does not have. It keeps every path that still needs that clean-up,
// even when this run failed part way.
func (u *Updater) updateBetaFiles(m *Manifest, todo []Entry, installed map[string]bool) error {
	listed := listedPaths(m.Effective(u.Groups))
	cands := loadBetaFiles(u.Game)
	if m.FromBeta {
		for _, e := range todo {
			if e.Kind != KindDelete && installed[strings.ToLower(e.Path)] {
				cands = append(cands, e.Path)
			}
		}
	}
	seen := map[string]bool{}
	var keep []string
	for _, p := range cands {
		k := strings.ToLower(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		if m.FromBeta && listed[k] {
			keep = append(keep, p)
			continue
		}
		if local, err := u.local(p); err == nil && !listed[k] && exists(local) {
			keep = append(keep, p) // not removed yet: try again next time
		}
	}
	return saveBetaFiles(u.Game, keep)
}

// installObject downloads one object, checks its hash and size, and installs it
// at every path that uses it.
func (u *Updater) installObject(ctx context.Context, entries []Entry, done *atomic.Int64) error {
	e := entries[0]
	part := filepath.Join(u.Game, StateDirName, "download", e.Hash+".part")
	if fileSize(part) > e.Size {
		os.Remove(part)
	}
	c := &counter{global: done}
	c.add(fileSize(part)) // a resumed download counts what it already has
	if fileSize(part) < e.Size {
		err := u.withRetries(ctx, e.Path, func() error {
			return u.downloadOnce(ctx, "objects/"+e.Hash, part, e.Size, c)
		})
		if err != nil {
			c.add(-c.mine.Load()) // take this object back out of the progress total
			return err
		}
	}
	c.add(e.Size - c.mine.Load()) // count exactly e.Size for this object

	h, n, err := sha256File(part)
	if err != nil {
		return err
	}
	if h != e.Hash || n != e.Size {
		os.Remove(part)
		return fmt.Errorf("a downloaded file was damaged (%s); nothing was replaced for it, please run again", e.Path)
	}

	for i, ent := range entries {
		local, err := u.local(ent.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return err
		}
		if exists(local) && ent.Group == "" {
			if err := u.backup(ent.Path, local, false); err != nil {
				return fmt.Errorf("backing up %s: %w", ent.Path, err)
			}
		}
		if i == len(entries)-1 {
			err = os.Rename(part, local)
		} else {
			err = copyFile(part, local)
		}
		if err != nil {
			return err
		}
		if err := u.remember(ent, local); err != nil {
			return err
		}
	}
	return nil
}

// downloadOnce fetches path into part, resuming from what part already holds.
// A download that ends short returns io.ErrUnexpectedEOF, so it is retried and resumed.
func (u *Updater) downloadOnce(ctx context.Context, path, part string, size int64, c *counter) error {
	have := fileSize(part)
	url := u.Source + "/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", u.UserAgent)
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := u.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	flags := os.O_WRONLY | os.O_CREATE
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC // server ignored the range: start over
		c.add(-have)
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part)
		c.add(-have)
		return &httpError{resp.StatusCode, url} // retried from scratch
	default:
		return &httpError{resp.StatusCode, url}
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, &countingReader{r: resp.Body, c: c})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && fileSize(part) < size {
		err = io.ErrUnexpectedEOF
	}
	return err
}

func (u *Updater) deleteEntry(e Entry) error {
	local, err := u.local(e.Path)
	if err != nil {
		return err
	}
	if exists(local) {
		if e.Group == "" {
			err = u.backup(e.Path, local, true)
		} else {
			err = os.Remove(local)
		}
		if err != nil {
			return fmt.Errorf("removing %s: %w", e.Path, err)
		}
	}
	u.mu.Lock()
	delete(u.st, strings.ToLower(e.Path))
	u.mu.Unlock()
	if u.Verbose {
		u.logf("Removed %s", e.Path)
	}
	return nil
}

// backup keeps the previous copy of a base-game file in .psclassic/backup/.
func (u *Updater) backup(path, local string, move bool) error {
	dst := filepath.Join(u.Game, StateDirName, "backup", filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	os.Remove(dst)
	if move {
		return os.Rename(local, dst)
	}
	return copyFile(local, dst)
}

func (u *Updater) remember(e Entry, local string) error {
	fi, err := os.Stat(local)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.st[strings.ToLower(e.Path)] = known{Hash: e.Hash, Size: fi.Size(), Ticks: dotnetTicks(fi.ModTime())}
	u.mu.Unlock()
	return nil
}

// local maps a manifest path into the game folder and refuses anything outside it.
func (u *Updater) local(p string) (string, error) {
	full := filepath.Join(u.Game, filepath.FromSlash(p))
	rel, err := filepath.Rel(u.Game, full)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", fmt.Errorf("unsafe path %q", p)
	}
	return full, nil
}

// withRetries runs fn, retrying temporary failures with the launcher's delays.
func (u *Updater) withRetries(ctx context.Context, what string, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil || ctx.Err() != nil || !temporary(err) || attempt >= len(retryDelays) {
			return err
		}
		u.logf("Connection problem (%v); trying again (%d of %d)... %s", err, attempt+1, len(retryDelays), what)
		select {
		case <-time.After(retryDelays[attempt]):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type httpError struct {
	Code int
	URL  string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d from %s", e.Code, e.URL) }

// temporary matches the launcher's idea of a failure worth retrying: network
// trouble, a timeout, rate limiting, or a server error.
func temporary(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var he *httpError
	if errors.As(err, &he) {
		return he.Code == 408 || he.Code == 416 || he.Code == 429 || he.Code >= 500
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		strings.Contains(err.Error(), "connection reset") || strings.Contains(err.Error(), "broken pipe")
}

// counter tracks one object's bytes and feeds the shared progress total.
type counter struct {
	global *atomic.Int64
	mine   atomic.Int64
}

func (c *counter) add(n int64) {
	c.mine.Add(n)
	c.global.Add(n)
}

type countingReader struct {
	r io.Reader
	c *counter
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	cr.c.add(int64(n))
	return n, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".psclassic-tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func exists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
