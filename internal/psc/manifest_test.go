package psc

import (
	"strings"
	"testing"
)

func h(c byte) string { return strings.Repeat(string(c), 64) }

func TestParseManifest(t *testing.T) {
	text := "PSCLASSIC-MANIFEST 1\r\n" +
		"version 2026.11.4.3\r\n" +
		"created 2026-11-04T18:28:21Z\n" +
		"file " + h('a') + " 10 planetside.exe\n" +
		"config " + h('b') + " 20 fpscap.ini\n" +
		"optional hd-textures " + h('c') + " 30 patch6/x.fat\n" +
		"delete old/stale.dll\n" +
		"\n"
	m, err := ParseManifest(text)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "2026.11.4.3" || m.Created != "2026-11-04T18:28:21Z" {
		t.Fatalf("version/created = %q/%q", m.Version, m.Created)
	}
	want := []Entry{
		{KindFile, h('a'), 10, "planetside.exe", ""},
		{KindConfig, h('b'), 20, "fpscap.ini", ""},
		{KindOptional, h('c'), 30, "patch6/x.fat", "hd-textures"},
		{KindDelete, "", 0, "old/stale.dll", ""},
	}
	if len(m.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(m.Entries), len(want))
	}
	for i := range want {
		if m.Entries[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, m.Entries[i], want[i])
		}
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := map[string]string{
		"magic":       "PSCLASSIC-MANIFEST 2\n",
		"unknown":     "PSCLASSIC-MANIFEST 1\nfrobnicate x\n",
		"short hash":  "PSCLASSIC-MANIFEST 1\nfile abc 1 a\n",
		"upper hash":  "PSCLASSIC-MANIFEST 1\nfile " + h('A') + " 1 a\n",
		"no size":     "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " a\n",
		"bad size":    "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " -1 a\n",
		"absolute":    "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " 1 /etc/passwd\n",
		"dotdot":      "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " 1 a/../../b\n",
		"backslash":   "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " 1 a\\b\n",
		"drive":       "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " 1 c:x\n",
		"empty seg":   "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " 1 a//b\n",
		"delete up":   "PSCLASSIC-MANIFEST 1\ndelete ../x\n",
		"no group":    "PSCLASSIC-MANIFEST 1\noptional  " + h('a') + " 1 a\n",
		"control chr": "PSCLASSIC-MANIFEST 1\nfile " + h('a') + " 1 a\tb\n",
	}
	for name, text := range cases {
		if _, err := ParseManifest(text); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestEffective(t *testing.T) {
	m := &Manifest{Entries: []Entry{
		{Kind: KindFile, Hash: h('1'), Size: 1, Path: "base.dat"},
		{Kind: KindFile, Hash: h('2'), Size: 2, Path: "Tex/Shared.fat"},
		{Kind: KindOptional, Hash: h('3'), Size: 3, Path: "tex/shared.fat", Group: "hd-textures"}, // overrides, case differs
		{Kind: KindOptional, Hash: h('4'), Size: 4, Path: "voice/voice.exe", Group: "voice"},      // optional only
	}}

	off := m.Effective(map[string]bool{})
	wantOff := []Entry{
		{KindFile, h('1'), 1, "base.dat", ""},
		{KindFile, h('2'), 2, "Tex/Shared.fat", "hd-textures"}, // base file, tagged with the group
		{KindDelete, "", 0, "voice/voice.exe", "voice"},
	}
	assertEntries(t, "off", off, wantOff)

	on := m.Effective(map[string]bool{"hd-textures": true, "voice": true})
	wantOn := []Entry{
		{KindFile, h('1'), 1, "base.dat", ""},
		{KindFile, h('3'), 3, "tex/shared.fat", "hd-textures"},
		{KindFile, h('4'), 4, "voice/voice.exe", "voice"},
	}
	assertEntries(t, "on", on, wantOn)
}

func assertEntries(t *testing.T, name string, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d entries %+v, want %d", name, len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: entry %d = %+v, want %+v", name, i, got[i], want[i])
		}
	}
}
