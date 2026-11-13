package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

const maxVoiceBytes = 20 * 1024 * 1024 // Telegram's hosted Bot API download limit.

func (a *app) ensureBucket(ctx context.Context) error {
	exists, err := a.s3.BucketExists(ctx, a.cfg.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return a.s3.MakeBucket(ctx, a.cfg.bucket, minio.MakeBucketOptions{})
	}
	return nil
}

func newObjectKey(userID int64, pairID, kind string) string {
	return fmt.Sprintf("%d/%s/%s/%s.ogg", userID, pairID, kind, randomID())
}

// uploadVoice downloads the voice from Telegram and stores it under key. It returns the actual size.
func (a *app) uploadVoice(ctx context.Context, key, fileID, mimeType string) (int64, error) {
	var file struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := a.call(ctx, "getFile", map[string]any{"file_id": fileID}, &file); err != nil {
		return 0, err
	}
	if file.FilePath == "" || file.FileSize > maxVoiceBytes {
		return 0, errors.New("Telegram file unavailable or too large")
	}
	url := "https://api.telegram.org/file/bot" + a.cfg.token + "/" + file.FilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Telegram file request failed: %s", strings.ReplaceAll(err.Error(), a.cfg.token, "[redacted]"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Telegram file download status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVoiceBytes+1))
	if err != nil {
		return 0, err
	}
	if len(data) > maxVoiceBytes {
		return 0, errors.New("voice exceeds download limit")
	}
	if mimeType == "" {
		mimeType = "audio/ogg"
	}
	_, err = a.s3.PutObject(ctx, a.cfg.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: mimeType})
	return int64(len(data)), err
}

// uploadWorker drains samples whose audio has not been copied to object storage yet.
// The samples table is the queue, so pending uploads survive restarts.
func (a *app) uploadWorker(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		a.drainUploads(ctx)
		select {
		case <-ctx.Done():
			return
		case <-a.uploadKick:
		case <-tick.C:
		}
	}
}

func (a *app) kickUploads() {
	select {
	case a.uploadKick <- struct{}{}:
	default:
	}
}

func (a *app) drainUploads(ctx context.Context) {
	type job struct{ pairID, kind, key, fileID, mime string }
	for ctx.Err() == nil {
		rows, err := a.db.Query(ctx, `SELECT s.pair_id,s.kind,s.object_key,s.telegram_file_id,s.mime_type FROM samples s JOIN pairs p ON p.id=s.pair_id WHERE NOT s.stored AND p.status IN ('draft','complete') ORDER BY s.uploaded_at LIMIT 20`)
		if err != nil {
			log.Printf("upload queue: %v", err)
			return
		}
		var jobs []job
		for rows.Next() {
			var j job
			if err := rows.Scan(&j.pairID, &j.kind, &j.key, &j.fileID, &j.mime); err != nil {
				log.Printf("upload queue: %v", err)
				break
			}
			jobs = append(jobs, j)
		}
		rows.Close()
		if len(jobs) == 0 {
			return
		}
		done := 0
		for _, j := range jobs {
			uctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			size, err := a.uploadVoice(uctx, j.key, j.fileID, j.mime)
			cancel()
			if err != nil {
				log.Printf("upload %s/%s: %v", j.pairID, j.kind, err)
				continue
			}
			// Only mark stored if the sample still points at this key (it may have been replaced or deleted meanwhile).
			cmd, err := a.db.Exec(ctx, `UPDATE samples s SET stored=true,file_size_bytes=$4 FROM pairs p WHERE s.pair_id=p.id AND s.pair_id=$1 AND s.kind=$2 AND s.object_key=$3 AND p.status IN ('draft','complete')`, j.pairID, j.kind, j.key, size)
			if err != nil || cmd.RowsAffected() == 0 {
				_ = a.s3.RemoveObject(ctx, a.cfg.bucket, j.key, minio.RemoveObjectOptions{})
			}
			done++
		}
		if done == 0 {
			return // everything failed; retry on the next tick instead of spinning
		}
	}
}
