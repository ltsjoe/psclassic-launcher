package psc

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kind is what a manifest line asks for. The numbers match the official launcher's.
type Kind int

const (
	KindFile     Kind = 0 // "file": always kept current
	KindConfig   Kind = 1 // "config": installed only if missing, so player edits survive
	KindDelete   Kind = 2 // "delete": removed if present
	KindOptional Kind = 3 // "optional <group>": a file that belongs to an optional part
)

// Entry is one line of the manifest.
type Entry struct {
	Kind  Kind
	Hash  string // lower-case hex SHA-256; empty for deletes
	Size  int64
	Path  string // relative, '/'-separated
	Group string // optional part this entry belongs to; "" for the base game
}

// Manifest is the signed list of game files (manifest.txt).
type Manifest struct {
	Version  string
	Created  string
	Entries  []Entry
	FromBeta bool // fetched from the beta branch (beta/manifest.txt)
}

// GroupSize is the download size of an optional part, as the launcher's
// Settings show it ("HD Player and Vehicle Pack (860 MB download)").
func (m *Manifest) GroupSize(group string) int64 {
	var n int64
	for _, e := range m.Entries {
		if e.Kind == KindOptional && e.Group == group {
			n += e.Size
		}
	}
	return n
}

const manifestMagic = "PSCLASSIC-MANIFEST 1"

// ParseManifest reads manifest.txt the way the official launcher does.
func ParseManifest(text string) (*Manifest, error) {
	lines := strings.Split(text, "\n")
	if strings.TrimRight(lines[0], "\r") != manifestMagic {
		return nil, errors.New("not a Planetside Classic manifest")
	}
	m := &Manifest{}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		n := i + 1
		badLine := fmt.Errorf("bad manifest line %d", n)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "version "):
			m.Version = line[len("version "):]
			continue
		case strings.HasPrefix(line, "created "):
			m.Created = line[len("created "):]
			continue
		case strings.HasPrefix(line, "delete "):
			p, err := checkPath(line[len("delete "):])
			if err != nil {
				return nil, err
			}
			m.Entries = append(m.Entries, Entry{Kind: KindDelete, Path: p})
			continue
		}

		var kind Kind
		var rest string
		switch {
		case strings.HasPrefix(line, "file "):
			kind, rest = KindFile, line[len("file "):]
		case strings.HasPrefix(line, "config "):
			kind, rest = KindConfig, line[len("config "):]
		case strings.HasPrefix(line, "optional "):
			kind, rest = KindOptional, line[len("optional "):]
		default:
			return nil, fmt.Errorf("unknown manifest line %d", n)
		}

		var group string
		if kind == KindOptional {
			sp := strings.IndexByte(rest, ' ')
			if sp <= 0 {
				return nil, badLine
			}
			group, rest = rest[:sp], rest[sp+1:]
		}

		// <64 hex chars> <size> <path>
		a := strings.IndexByte(rest, ' ')
		if a != 64 {
			return nil, badLine
		}
		b := strings.IndexByte(rest[a+1:], ' ')
		if b < 0 {
			return nil, badLine
		}
		b += a + 1
		hash := rest[:64]
		if !isLowerHex(hash) {
			return nil, fmt.Errorf("bad hash on line %d", n)
		}
		size, err := strconv.ParseInt(rest[a+1:b], 10, 64)
		if err != nil || size < 0 {
			return nil, badLine
		}
		p, err := checkPath(rest[b+1:])
		if err != nil {
			return nil, err
		}
		m.Entries = append(m.Entries, Entry{Kind: kind, Hash: hash, Size: size, Path: p, Group: group})
	}
	return m, nil
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkPath rejects anything that could land outside the game folder, like the launcher does.
func checkPath(p string) (string, error) {
	bad := strings.TrimSpace(p) == "" ||
		strings.HasPrefix(p, "/") ||
		strings.ContainsAny(p, "\\:\"<>|")
	if !bad {
		for _, seg := range strings.Split(p, "/") {
			if seg == "" || seg == "." || seg == ".." {
				bad = true
				break
			}
		}
	}
	if !bad {
		for _, c := range p {
			if c < 32 {
				bad = true
				break
			}
		}
	}
	if bad {
		return "", fmt.Errorf("unsafe path in manifest: %q", p)
	}
	return p, nil
}

// Effective works out what the game folder should hold for the chosen optional parts.
// It mirrors the launcher's Manifest.Effective:
//   - an optional file that is switched on replaces the base file at the same path;
//   - a base file that an optional part could replace is tagged with that part's group;
//   - an optional file with no base file is installed when its part is on and deleted when off.
//
// Paths are compared without regard to case, as on Windows.
func (m *Manifest) Effective(groups map[string]bool) []Entry {
	chosen := map[string]Entry{} // optional entries whose group is on
	anyOpt := map[string]Entry{} // every optional entry
	var optOrder []string
	for _, e := range m.Entries {
		if e.Kind != KindOptional {
			continue
		}
		k := strings.ToLower(e.Path)
		if groups[e.Group] {
			chosen[k] = e
		}
		if _, seen := anyOpt[k]; !seen {
			optOrder = append(optOrder, k)
		}
		anyOpt[k] = e
	}

	based := map[string]bool{}
	var out []Entry
	for _, e := range m.Entries {
		if e.Kind == KindOptional {
			continue
		}
		k := strings.ToLower(e.Path)
		based[k] = true
		if o, ok := chosen[k]; ok {
			out = append(out, Entry{Kind: KindFile, Hash: o.Hash, Size: o.Size, Path: o.Path, Group: o.Group})
		} else if o, ok := anyOpt[k]; ok {
			e.Group = o.Group
			out = append(out, e)
		} else {
			out = append(out, e)
		}
	}
	for _, k := range optOrder {
		if based[k] {
			continue
		}
		if c, ok := chosen[k]; ok {
			out = append(out, Entry{Kind: KindFile, Hash: c.Hash, Size: c.Size, Path: c.Path, Group: c.Group})
		} else {
			o := anyOpt[k]
			out = append(out, Entry{Kind: KindDelete, Path: o.Path, Group: o.Group})
		}
	}
	return out
}
