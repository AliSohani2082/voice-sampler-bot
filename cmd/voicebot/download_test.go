package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestExportBounds(t *testing.T) {
	// 2026-11-10 21:30 UTC is 2026-11-11 01:00 in Tehran (+03:30), so "today" is the 11th there.
	now := time.Date(2026, 11, 10, 21, 30, 0, 0, time.UTC)
	for kind, want := range map[string]string{"today": "2026-11-11", "week": "2026-11-05"} {
		from, to, ok := exportBounds(kind, now)
		if !ok || from.In(exportLoc).Format("2006-01-02 15:04") != want+" 00:00" || to.In(exportLoc).Format("2006-01-02 15:04") != "2026-11-12 00:00" {
			t.Errorf("%s: from=%v to=%v ok=%v", kind, from, to, ok)
		}
	}
	if from, _, ok := exportBounds("all", now); !ok || from.After(now.AddDate(-20, 0, 0)) {
		t.Errorf("all: from=%v ok=%v", from, ok)
	}
	for _, bad := range []string{"", "123456", "today:1", "TODAY"} {
		if _, _, ok := exportBounds(bad, now); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	// Every range the /download prompt offers has to be one exportBounds accepts.
	for _, o := range lookupCommand("download").args[0].options {
		if _, _, ok := exportBounds(o.token, now); !ok {
			t.Errorf("download option %q has no export range", o.token)
		}
	}
}

func TestExportNames(t *testing.T) {
	for _, c := range []struct {
		kind string
		n    int
		want string
	}{{"sleepy", 24, "sleepy/40507844-24.wav"}, {"awake", 24, "fresh/40507844-24.wav"}, {"awake", 1, "fresh/40507844-1.wav"}} {
		if got := exportPath(c.kind, "40507844", c.n); got != c.want {
			t.Errorf("path(%s,%d) = %q, want %q", c.kind, c.n, got, c.want)
		}
	}
	for _, c := range []struct {
		first, last, kind string
		part              int
		isLast            bool
		want              string
	}{
		{"Ali", "Sohani", "week", 1, true, "Ali_Sohani_week.zip"},
		{"Ali", "Sohani", "all", 1, false, "Ali_Sohani_all_part1.zip"},
		{"Ali", "Sohani", "today", 2, true, "Ali_Sohani_today_part2.zip"},
		{"Mohammad Ali", " Sohani  Shahrood ", "week", 1, true, "Mohammad_Ali_Sohani_Shahrood_week.zip"},
		{"علی", "سهانی نژاد", "all", 1, true, "علی_سهانی_نژاد_all.zip"},
		{"A/b", "C:d", "all", 1, true, "Ab_Cd_all.zip"},
		{"", "", "all", 1, true, "voices_all.zip"},
	} {
		if got := exportFilename(c.first, c.last, c.kind, c.part, c.isLast); got != c.want {
			t.Errorf("filename(%q,%q,%s,%d,%v) = %q, want %q", c.first, c.last, c.kind, c.part, c.isLast, got, c.want)
		}
	}
}

func pairGroup(i int) []exportItem {
	return []exportItem{
		{Key: fmt.Sprintf("s%d", i), Path: exportPath("sleepy", "40507844", i)},
		{Key: fmt.Sprintf("f%d", i), Path: exportPath("awake", "40507844", i)},
	}
}

func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}

// Every part has only sleepy/ and fresh/ at the root, with the same names in both.
func checkMirrored(t *testing.T, names []string) {
	t.Helper()
	sleepy, fresh := map[string]bool{}, map[string]bool{}
	for _, n := range names {
		dir, file, ok := strings.Cut(n, "/")
		switch {
		case !ok || strings.Contains(file, "/"):
			t.Errorf("unexpected path %q", n)
		case dir == "sleepy":
			sleepy[file] = true
		case dir == "fresh":
			fresh[file] = true
		default:
			t.Errorf("unexpected folder in %q", n)
		}
	}
	if len(sleepy) != len(fresh) {
		t.Errorf("sleepy has %d files, fresh has %d", len(sleepy), len(fresh))
	}
	for f := range sleepy {
		if !fresh[f] {
			t.Errorf("%s in sleepy but not in fresh", f)
		}
	}
}

func TestBuildZipsSplits(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	var groups [][]exportItem
	for i := 1; i <= 5; i++ {
		groups = append(groups, pairGroup(i))
	}
	convert := func(exportItem) ([]byte, error) { b := make([]byte, 1000); rnd.Read(b); return b, nil } // incompressible
	var parts [][]string
	var lasts []bool
	skipped, err := buildZips(groups, 4500, convert, func(data []byte, part int, last bool) error {
		names := zipNames(t, data)
		checkMirrored(t, names)
		parts = append(parts, names)
		lasts = append(lasts, last)
		if part != len(parts) || len(data) > 4500+2000 {
			t.Errorf("part %d size %d", part, len(data))
		}
		return nil
	})
	if err != nil || skipped != 0 {
		t.Fatalf("skipped=%d err=%v", skipped, err)
	}
	if len(parts) < 2 || lasts[0] || !lasts[len(lasts)-1] {
		t.Fatalf("parts=%v lasts=%v", parts, lasts)
	}
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	if n != 10 {
		t.Fatalf("%d files in zips, want 10", n)
	}
	// Nothing to export builds no zip.
	if _, err := buildZips(nil, 100, convert, func([]byte, int, bool) error { t.Error("emitted empty zip"); return nil }); err != nil {
		t.Fatal(err)
	}
}

// A pair with an unfetchable recording is left out whole; the rest still export, and all skipped emits nothing.
func TestBuildZipsSkips(t *testing.T) {
	groups := [][]exportItem{pairGroup(1), pairGroup(2), pairGroup(3)}
	convert := func(it exportItem) ([]byte, error) {
		if it.Key == "f2" {
			return nil, errSkipItem
		}
		return []byte(it.Key), nil
	}
	var names []string
	skipped, err := buildZips(groups, 1<<20, convert, func(data []byte, part int, last bool) error {
		names = append(names, zipNames(t, data)...)
		return nil
	})
	if err != nil || skipped != 1 {
		t.Fatalf("skipped=%d err=%v", skipped, err)
	}
	checkMirrored(t, names)
	want := "[sleepy/40507844-1.wav fresh/40507844-1.wav sleepy/40507844-3.wav fresh/40507844-3.wav]"
	if fmt.Sprint(names) != want {
		t.Fatalf("names=%v", names)
	}
	skipAll := func(exportItem) ([]byte, error) { return nil, errSkipItem }
	skipped, err = buildZips(groups, 1<<20, skipAll, func([]byte, int, bool) error { t.Error("emitted empty zip"); return nil })
	if err != nil || skipped != 3 {
		t.Fatalf("skipped=%d err=%v", skipped, err)
	}
}

func TestOggToWAV(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	ogg, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "libopus", "-ar", "48000", "-ac", "1", "-f", "ogg", "pipe:1").Output()
	if err != nil {
		t.Skipf("cannot generate opus sample: %v", err)
	}
	wav, err := oggToWAV(context.Background(), ogg)
	if err != nil {
		t.Fatal(err)
	}
	if string(wav[8:12]) != "WAVE" || binary.LittleEndian.Uint32(wav[4:]) != uint32(len(wav)-8) || binary.LittleEndian.Uint32(wav[40:]) != uint32(len(wav)-44) {
		t.Fatal("bad WAV header")
	}
	if binary.LittleEndian.Uint16(wav[20:]) != 1 || binary.LittleEndian.Uint16(wav[22:]) != 1 || binary.LittleEndian.Uint32(wav[24:]) != 48000 || binary.LittleEndian.Uint16(wav[34:]) != 16 {
		t.Fatalf("want PCM mono 48k 16-bit, header % x", wav[:44])
	}
	if _, err := oggToWAV(context.Background(), []byte("not audio")); err == nil {
		t.Fatal("garbage input converted")
	}
}
