package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os/exec"
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
	at := time.Date(2026, 11, 10, 21, 30, 5, 0, time.UTC) // 01:00:05 on the 11th in Tehran
	if got := exportPath(at, "0123456789abcdef0123456789abcdef", "sleepy", "40123456"); got != "2026-11-11/010005_01234567/Sleepy-40123456.wav" {
		t.Errorf("path = %q", got)
	}
	if got := exportPath(at, "0123456789abcdef0123456789abcdef", "awake", "40123456"); got != "2026-11-11/010005_01234567/fresh-40123456.wav" {
		t.Errorf("path = %q", got)
	}
	for _, c := range []struct {
		part int
		last bool
		want string
	}{{1, true, "voices_week_2026-11-11.zip"}, {1, false, "voices_week_2026-11-11_part1.zip"}, {2, true, "voices_week_2026-11-11_part2.zip"}} {
		if got := exportFilename("week", at, c.part, c.last); got != c.want {
			t.Errorf("filename(%d,%v) = %q, want %q", c.part, c.last, got, c.want)
		}
	}
}

func TestBuildZipsSplits(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	var items []exportItem
	for i := 0; i < 5; i++ {
		items = append(items, exportItem{Key: fmt.Sprint(i), Path: fmt.Sprintf("2026-01-01/%06d_x_awake.wav", i)})
	}
	convert := func(string) ([]byte, error) { b := make([]byte, 1000); rnd.Read(b); return b, nil } // incompressible
	var parts [][]string
	var lasts []bool
	err := buildZips(items, 2500, convert, func(data []byte, part int, last bool) error {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		var names []string
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		parts = append(parts, names)
		lasts = append(lasts, last)
		if part != len(parts) || len(data) > 2500+1000 {
			t.Errorf("part %d size %d", part, len(data))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 2 || lasts[0] || !lasts[len(lasts)-1] {
		t.Fatalf("parts=%v lasts=%v", parts, lasts)
	}
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	if n != len(items) {
		t.Fatalf("%d files in zips, want %d", n, len(items))
	}
	// Nothing to export builds no zip.
	if err := buildZips(nil, 100, convert, func([]byte, int, bool) error { t.Error("emitted empty zip"); return nil }); err != nil {
		t.Fatal(err)
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
