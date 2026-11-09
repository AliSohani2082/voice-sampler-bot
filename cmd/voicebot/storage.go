package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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

func (a *app) storeVoice(ctx context.Context, userID int64, pairID, kind string, v *voice) (string, int64, error) {
	var file struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := a.call(ctx, "getFile", map[string]any{"file_id": v.FileID}, &file); err != nil {
		return "", 0, err
	}
	if file.FilePath == "" || file.FileSize > maxVoiceBytes {
		return "", 0, errors.New("Telegram file unavailable or too large")
	}
	url := "https://api.telegram.org/file/bot" + a.cfg.token + "/" + file.FilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("Telegram file request failed: %s", strings.ReplaceAll(err.Error(), a.cfg.token, "[redacted]"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("Telegram file download status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxVoiceBytes+1))
	if err != nil {
		return "", 0, err
	}
	if len(data) > maxVoiceBytes {
		return "", 0, errors.New("voice exceeds download limit")
	}
	key := fmt.Sprintf("%d/%s/%s/%s.ogg", userID, pairID, kind, randomID())
	contentType := v.MimeType
	if contentType == "" {
		contentType = "audio/ogg"
	}
	_, err = a.s3.PutObject(ctx, a.cfg.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: contentType})
	return key, int64(len(data)), err
}
