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

// pendingFetchTimeout bounds each Telegram download of a recording the upload worker has not stored yet.
// Telegram's getFile can stall for minutes while sendMessage still works; one slow file must not hold up the export.
const pendingFetchTimeout = 20 * time.Second

// errSkipItem tells buildZips to leave a recording out of the zip instead of failing the export.
var errSkipItem = errors.New("skip export item")

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
	Key    string // S3 object key
	FileID string // Telegram file_id, used while the background upload is still pending
	Stored bool   // false until the upload worker has copied the audio to object storage
	Path   string // path inside the zip, see exportPath
	At     time.Time
}

// exportPath places a recording at <sleepy|fresh>/<student number>-<n>.wav. n is the pair's running number among
// all of the student's completed pairs, so the sleepy and fresh files of one pair share a name in the two
// folders and a pair keeps its name in every export (all, week or day).
func exportPath(kind, studentNumber string, n int) string {
	folder := "fresh"
	if kind == "sleepy" {
		folder = "sleepy"
	}
	return fmt.Sprintf("%s/%s-%d.wav", folder, studentNumber, n)
}

// exportBase turns the profile name into the zip's base name: first and last name joined by "_", with spaces
// inside either name also becoming "_". Non-Latin names are kept as written.
func exportBase(first, last string) string {
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
				return -1
			}
			return r
		}, s)
		return strings.Join(strings.Fields(s), "_")
	}
	var parts []string
	for _, p := range []string{clean(first), clean(last)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "voices"
	}
	return strings.Join(parts, "_")
}

// exportFilename is <first>_<last>_<range>.zip, with _partN added when the export is split.
func exportFilename(first, last, kind string, part int, isLast bool) string {
	suffix := ""
	if part > 1 || !isLast {
		suffix = fmt.Sprintf("_part%d", part)
	}
	return fmt.Sprintf("%s_%s%s.zip", exportBase(first, last), kind, suffix)
}

// buildZips streams groups into zips of at most limit bytes and hands each finished zip to emit before building
// the next. A group (the sleepy and fresh files of one pair) is never split across zips, and if any file of a
// group cannot be fetched the whole group is left out, so the sleepy and fresh folders always match. It returns
// the number of groups left out.
func buildZips(groups [][]exportItem, limit int, convert func(it exportItem) ([]byte, error), emit func(data []byte, part int, last bool) error) (int, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	part, n, skipped := 1, 0, 0
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
groups:
	for _, g := range groups {
		wavs := make([][]byte, len(g))
		size := 0
		for i, it := range g {
			wav, err := convert(it)
			if errors.Is(err, errSkipItem) {
				skipped++
				continue groups
			}
			if err != nil {
				return skipped, err
			}
			wavs[i] = wav
			size += len(wav)
		}
		if err := zw.Flush(); err != nil {
			return skipped, err
		}
		if n > 0 && buf.Len()+size > limit {
			if err := flush(false); err != nil {
				return skipped, err
			}
		}
		for i, it := range g {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: it.Path, Method: zip.Deflate, Modified: it.At})
			if err != nil {
				return skipped, err
			}
			if _, err := w.Write(wavs[i]); err != nil {
				return skipped, err
			}
		}
		n++
	}
	if n == 0 {
		return skipped, nil
	}
	return skipped, flush(true)
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

func (a *app) fetchWAV(ctx context.Context, it exportItem) ([]byte, error) {
	if !it.Stored {
		// Not in object storage yet: take it straight from Telegram. If Telegram is slow too, leave this one
		// out (the caller reports it) rather than failing every other recording in the export.
		fctx, cancel := context.WithTimeout(ctx, pendingFetchTimeout)
		ogg, err := a.downloadTelegramFile(fctx, it.FileID)
		cancel()
		if err != nil {
			log.Printf("export: pending %s: %v", it.Key, err)
			return nil, errSkipItem
		}
		return oggToWAV(ctx, ogg)
	}
	obj, err := a.s3.GetObject(ctx, a.cfg.bucket, it.Key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	ogg, err := io.ReadAll(io.LimitReader(obj, maxVoiceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", it.Key, err)
	}
	return oggToWAV(ctx, ogg)
}

// exportData is one user's export: their profile name for the zip name, and the recordings grouped by pair.
type exportData struct {
	First, Last string
	Groups      [][]exportItem
}

// exportItems always filters by the caller's own user_id; callback data never carries IDs. The running number is
// counted over all of the user's completed pairs before the range filter, so it does not depend on the range.
// A pair missing either recording is left out so the two folders stay equal.
func (a *app) exportItems(ctx context.Context, userID int64, from, to time.Time) (exportData, error) {
	var d exportData
	rows, err := a.db.Query(ctx, `SELECT p.n,p.completed_at,p.id,s.kind,s.object_key,s.telegram_file_id,s.stored,pr.student_number,pr.first_name,pr.last_name FROM (SELECT id,completed_at,row_number() OVER (ORDER BY completed_at,id) AS n FROM pairs WHERE user_id=$1 AND status='complete') p JOIN samples s ON s.pair_id=p.id JOIN profiles pr ON pr.user_id=$1 WHERE p.completed_at>=$2 AND p.completed_at<$3 ORDER BY p.n,s.kind`, userID, from, to)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	byPair := map[string][]exportItem{}
	var order []string
	for rows.Next() {
		var n int
		var at time.Time
		var id, kind, key, fileID, student string
		var stored bool
		if err := rows.Scan(&n, &at, &id, &kind, &key, &fileID, &stored, &student, &d.First, &d.Last); err != nil {
			return d, err
		}
		if _, ok := byPair[id]; !ok {
			order = append(order, id)
		}
		byPair[id] = append(byPair[id], exportItem{Key: key, FileID: fileID, Stored: stored, Path: exportPath(kind, student, n), At: at})
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	for _, id := range order {
		if g := byPair[id]; len(g) == 2 {
			d.Groups = append(d.Groups, g)
		}
	}
	return d, nil
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
	data, err := a.exportItems(ctx, userID, from, to)
	if err != nil {
		return err
	}
	if len(data.Groups) == 0 {
		return a.send(ctx, userID, "You have no submitted recordings in that range.", nil)
	}
	if err := a.send(ctx, userID, "Preparing your file… this can take a minute.", nil); err != nil {
		return err
	}
	convert := func(it exportItem) ([]byte, error) { return a.fetchWAV(ctx, it) }
	skipped, err := buildZips(data.Groups, zipPartLimit, convert, func(zipData []byte, part int, last bool) error {
		if part == 1 && !last {
			_ = a.send(ctx, userID, "Your export is large, so it will arrive as several zip files.", nil)
		}
		return a.sendDocument(ctx, userID, exportFilename(data.First, data.Last, kind, part, last), zipData)
	})
	if err != nil || skipped == 0 {
		return err
	}
	// Each skipped pair leaves out two recordings.
	return a.send(ctx, userID, fmt.Sprintf(translate(ctx, skippedNote), skipped*2), nil)
}

const skippedNote = "%d recording(s) are still being saved and were left out. Run /download again in a few minutes to include them."

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
