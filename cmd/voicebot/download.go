package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"time"
	_ "time/tzdata" // the alpine runtime image has no zoneinfo

	"github.com/minio/minio-go/v7"
)

// exportTZ defines "today" and "this week" for /download. No timezone is configured elsewhere in the bot.
const exportTZ = "Asia/Tehran"

// zipPartLimit stays under Telegram's 50 MB bot upload limit, leaving room for zip overhead.
const zipPartLimit = 45 << 20

const downloadHelp = "Use /download to export your own recordings as WAV files in a zip."

var exportLoc = func() *time.Location {
	loc, err := time.LoadLocation(exportTZ)
	if err != nil {
		panic(err)
	}
	return loc
}()

func helpText(ctx context.Context) string {
	return translate(ctx, welcome) + "\n\n" + translate(ctx, downloadHelp)
}

// exportBounds returns the half-open completion-time range [from, to) for a callback range key.
// Week is the last 7 calendar days including today, not a Monday-start week.
func exportBounds(kind string, now time.Time) (from, to time.Time, ok bool) {
	n := now.In(exportLoc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, exportLoc)
	to = today.AddDate(0, 0, 1)
	switch kind {
	case "today":
		return today, to, true
	case "week":
		return today.AddDate(0, 0, -6), to, true
	case "all":
		return time.Unix(0, 0), to, true
	}
	return from, to, false
}

type exportItem struct {
	Key  string // S3 object key
	Path string // path inside the zip: YYYY-MM-DD/HHMMSS_<pair>_<kind>.wav
	At   time.Time
}

func exportPath(completed time.Time, pairID, kind string) string {
	if len(pairID) > 8 {
		pairID = pairID[:8]
	}
	return completed.In(exportLoc).Format("2006-01-02/150405") + "_" + pairID + "_" + kind + ".wav"
}

func exportFilename(kind string, now time.Time, part int, last bool) string {
	suffix := ""
	if part > 1 || !last {
		suffix = fmt.Sprintf("_part%d", part)
	}
	return fmt.Sprintf("voices_%s_%s%s.zip", kind, now.In(exportLoc).Format("2006-01-02"), suffix)
}

// buildZips streams items into zips of at most limit bytes (split on file boundaries, in order, so a day
// only splits when a part fills mid-day) and hands each finished zip to emit before building the next.
func buildZips(items []exportItem, limit int, convert func(key string) ([]byte, error), emit func(data []byte, part int, last bool) error) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	part, n := 1, 0
	flush := func(last bool) error {
		if err := zw.Close(); err != nil {
			return err
		}
		if err := emit(buf.Bytes(), part, last); err != nil {
			return err
		}
		buf.Reset()
		zw = zip.NewWriter(&buf)
		part, n = part+1, 0
		return nil
	}
	for _, it := range items {
		wav, err := convert(it.Key)
		if err != nil {
			return err
		}
		if err := zw.Flush(); err != nil {
			return err
		}
		if n > 0 && buf.Len()+len(wav) > limit {
			if err := flush(false); err != nil {
				return err
			}
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: it.Path, Method: zip.Deflate, Modified: it.At})
		if err != nil {
			return err
		}
		if _, err := w.Write(wav); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return nil
	}
	return flush(true)
}

// fixWAVSizes fills in the RIFF/data sizes ffmpeg leaves as 0xFFFFFFFF when writing to a pipe.
// It relies on -bitexact output having a plain 44-byte header.
func fixWAVSizes(b []byte) error {
	if len(b) < 44 || string(b[0:4]) != "RIFF" || string(b[36:40]) != "data" {
		return errors.New("unexpected WAV header from ffmpeg")
	}
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	return nil
}

// oggToWAV converts via stdin/stdout, keeping the source sample rate and channels (PCM 16-bit).
func oggToWAV(ctx context.Context, ogg []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", "pipe:0", "-c:a", "pcm_s16le", "-fflags", "+bitexact", "-flags:a", "+bitexact", "-f", "wav", "pipe:1")
	var out, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(ogg), &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	wav := out.Bytes()
	return wav, fixWAVSizes(wav)
}

func (a *app) fetchWAV(ctx context.Context, key string) ([]byte, error) {
	obj, err := a.s3.GetObject(ctx, a.cfg.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	ogg, err := io.ReadAll(io.LimitReader(obj, maxVoiceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return oggToWAV(ctx, ogg)
}

// exportItems always filters by the caller's own user_id; callback data never carries IDs.
func (a *app) exportItems(ctx context.Context, userID int64, from, to time.Time) ([]exportItem, error) {
	rows, err := a.db.Query(ctx, `SELECT p.completed_at,p.id,s.kind,s.object_key FROM pairs p JOIN samples s ON s.pair_id=p.id WHERE p.user_id=$1 AND p.status='complete' AND p.completed_at>=$2 AND p.completed_at<$3 ORDER BY p.completed_at,p.id,s.kind`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []exportItem
	for rows.Next() {
		var at time.Time
		var id, kind, key string
		if err := rows.Scan(&at, &id, &kind, &key); err != nil {
			return nil, err
		}
		items = append(items, exportItem{Key: key, Path: exportPath(at, id, kind), At: at})
	}
	return items, rows.Err()
}

// startExport runs in the background so the update loop keeps polling; one export per user at a time.
func (a *app) startExport(ctx context.Context, userID int64, kind string) {
	if _, busy := a.exporting.LoadOrStore(userID, true); busy {
		_ = a.send(ctx, userID, "Your previous export is still being prepared. Please wait for it.", nil)
		return
	}
	go func() {
		defer a.exporting.Delete(userID)
		ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		if err := a.export(ctx, userID, kind); err != nil {
			log.Printf("export for user %d (%s): %v", userID, kind, err)
			_ = a.send(ctx, userID, "I couldn't build your export. Please try again later, or choose a shorter range.", nil)
		}
	}()
}

func (a *app) export(ctx context.Context, userID int64, kind string) error {
	now := time.Now()
	from, to, ok := exportBounds(kind, now)
	if !ok {
		return fmt.Errorf("unknown range %q", kind)
	}
	items, err := a.exportItems(ctx, userID, from, to)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return a.send(ctx, userID, "You have no submitted recordings in that range.", nil)
	}
	if err := a.send(ctx, userID, "Preparing your file… this can take a minute.", nil); err != nil {
		return err
	}
	return buildZips(items, zipPartLimit, func(key string) ([]byte, error) { return a.fetchWAV(ctx, key) }, func(data []byte, part int, last bool) error {
		if part == 1 && !last {
			_ = a.send(ctx, userID, "Your export is large, so it will arrive as several zip files.", nil)
		}
		return a.sendDocument(ctx, userID, exportFilename(kind, now, part, last), data)
	})
}

func (a *app) sendDocument(ctx context.Context, chatID int64, filename string, data []byte) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", fmt.Sprint(chatID))
	fw, err := mw.CreateFormFile("document", filename)
	if err != nil {
		return err
	}
	fw.Write(data)
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL+"sendDocument", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// a.http has a 75s total timeout, too short for a 45 MB upload; ctx bounds this instead.
	client := &http.Client{Transport: a.http.Transport}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Telegram upload failed: %s", strings.ReplaceAll(err.Error(), a.cfg.token, "[redacted]"))
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return err
	}
	if !env.OK {
		return fmt.Errorf("telegram sendDocument: %s", env.Description)
	}
	return nil
}
