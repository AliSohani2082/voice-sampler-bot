package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
)

type session struct {
	Stage     string
	PairID    string
	Candidate *voice
	Nonce     string
}

type sample struct {
	Kind       string
	Transcript string
	ObjectKey  string
	FileID     string
	Duration   int
}

type pair struct {
	ID        string
	CreatedAt time.Time
	Sleepy    sample
	Awake     sample
}

func (a *app) getPair(ctx context.Context, userID int64, id string) (pair, error) {
	var p pair
	err := a.db.QueryRow(ctx, `SELECT id,created_at FROM pairs WHERE id=$1 AND user_id=$2 AND status='complete'`, id, userID).Scan(&p.ID, &p.CreatedAt)
	if err != nil {
		return p, err
	}
	rows, err := a.db.Query(ctx, `SELECT kind,transcript,object_key,telegram_file_id,duration_seconds FROM samples WHERE pair_id=$1`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var s sample
		if err := rows.Scan(&s.Kind, &s.Transcript, &s.ObjectKey, &s.FileID, &s.Duration); err != nil {
			return p, err
		}
		if s.Kind == "sleepy" {
			p.Sleepy = s
		} else {
			p.Awake = s
		}
	}
	return p, rows.Err()
}

const schema = `
CREATE TABLE IF NOT EXISTS pairs (
  id text PRIMARY KEY,
  user_id bigint NOT NULL,
  status text NOT NULL CHECK (status IN ('draft','complete','deleting')),
  created_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz
);
CREATE INDEX IF NOT EXISTS pairs_owner_idx ON pairs(user_id, status, created_at DESC);
CREATE TABLE IF NOT EXISTS samples (
  pair_id text NOT NULL REFERENCES pairs(id) ON DELETE CASCADE,
  kind text NOT NULL CHECK (kind IN ('sleepy','awake')),
  object_key text NOT NULL,
  telegram_file_id text NOT NULL,
  telegram_unique_id text NOT NULL,
  duration_seconds integer NOT NULL CHECK (duration_seconds BETWEEN 1 AND 10),
  file_size_bytes bigint NOT NULL,
  mime_type text NOT NULL,
  transcript text NOT NULL DEFAULT '',
  uploaded_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY(pair_id,kind)
);
CREATE TABLE IF NOT EXISTS sessions (
  user_id bigint PRIMARY KEY,
  stage text NOT NULL,
  pair_id text NOT NULL,
  candidate jsonb,
  nonce text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS user_languages (
  user_id bigint PRIMARY KEY,
  language text NOT NULL CHECK (language IN ('en','fa'))
);
CREATE TABLE IF NOT EXISTS object_deletions (
  object_key text PRIMARY KEY,
  pair_id text,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS bot_state (
  id integer PRIMARY KEY CHECK (id=1),
  offset_value bigint NOT NULL
);`

func (a *app) getLanguage(ctx context.Context, userID int64) (string, error) {
	var language string
	err := a.db.QueryRow(ctx, `SELECT language FROM user_languages WHERE user_id=$1`, userID).Scan(&language)
	if errors.Is(err, pgx.ErrNoRows) {
		return "en", nil
	}
	return language, err
}

func (a *app) setLanguage(ctx context.Context, userID int64, language string) error {
	_, err := a.db.Exec(ctx, `INSERT INTO user_languages(user_id,language) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET language=$2`, userID, language)
	return err
}

func (a *app) getSession(ctx context.Context, userID int64) (session, error) {
	var s session
	var candidate []byte
	err := a.db.QueryRow(ctx, `SELECT stage,pair_id,candidate,nonce FROM sessions WHERE user_id=$1`, userID).Scan(&s.Stage, &s.PairID, &candidate, &s.Nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return session{}, nil
	}
	if err != nil {
		return s, err
	}
	if len(candidate) > 0 {
		if err := json.Unmarshal(candidate, &s.Candidate); err != nil {
			return s, err
		}
	}
	return s, nil
}

func (a *app) putSession(ctx context.Context, userID int64, s session) error {
	var candidate any
	if s.Candidate != nil {
		data, err := json.Marshal(s.Candidate)
		if err != nil {
			return err
		}
		candidate = data
	}
	_, err := a.db.Exec(ctx, `INSERT INTO sessions(user_id,stage,pair_id,candidate,nonce) VALUES($1,$2,$3,$4,$5) ON CONFLICT(user_id) DO UPDATE SET stage=$2,pair_id=$3,candidate=$4,nonce=$5`, userID, s.Stage, s.PairID, candidate, s.Nonce)
	return err
}

func (a *app) clearSession(ctx context.Context, userID int64) error {
	_, err := a.db.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
	return err
}

func (a *app) saveVoice(ctx context.Context, userID int64, pairID, kind, key string, v *voice) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var old string
	err = tx.QueryRow(ctx, `SELECT s.object_key FROM samples s JOIN pairs p ON p.id=s.pair_id WHERE s.pair_id=$1 AND s.kind=$2 AND p.user_id=$3 AND p.status IN ('draft','complete')`, pairID, kind, userID).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pairs WHERE id=$1 AND user_id=$2 AND status IN ('draft','complete'))`, pairID, userID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("pair unavailable")
	}
	_, err = tx.Exec(ctx, `INSERT INTO samples(pair_id,kind,object_key,telegram_file_id,telegram_unique_id,duration_seconds,file_size_bytes,mime_type) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(pair_id,kind) DO UPDATE SET object_key=$3,telegram_file_id=$4,telegram_unique_id=$5,duration_seconds=$6,file_size_bytes=$7,mime_type=$8,uploaded_at=now()`, pairID, kind, key, v.FileID, v.FileUniqueID, v.Duration, v.FileSize, v.MimeType)
	if err != nil {
		return err
	}
	if old != "" && old != key {
		if _, err := tx.Exec(ctx, `INSERT INTO object_deletions(object_key) VALUES($1) ON CONFLICT DO NOTHING`, old); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (a *app) saveTranscript(ctx context.Context, userID int64, pairID, kind, transcript string) error {
	cmd, err := a.db.Exec(ctx, `UPDATE samples s SET transcript=$1 FROM pairs p WHERE s.pair_id=p.id AND p.id=$2 AND p.user_id=$3 AND p.status IN ('draft','complete') AND s.kind=$4`, transcript, pairID, userID, kind)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("sample unavailable")
	}
	return nil
}

func (a *app) completePair(ctx context.Context, userID int64, pairID string) error {
	cmd, err := a.db.Exec(ctx, `UPDATE pairs SET status='complete',completed_at=now() WHERE id=$1 AND user_id=$2 AND status='draft' AND (SELECT count(*) FROM samples WHERE pair_id=$1 AND transcript<>'')=2`, pairID, userID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() != 1 {
		return errors.New("pair is incomplete")
	}
	return nil
}

func (a *app) markDeleting(ctx context.Context, userID int64, pairID string) error {
	tx, err := a.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cmd, err := tx.Exec(ctx, `UPDATE pairs SET status='deleting' WHERE id=$1 AND user_id=$2 AND status IN ('draft','complete','deleting')`, pairID, userID)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return errors.New("pair unavailable")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO object_deletions(object_key,pair_id) SELECT object_key,pair_id FROM samples WHERE pair_id=$1 ON CONFLICT(object_key) DO UPDATE SET pair_id=EXCLUDED.pair_id`, pairID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (a *app) processDeletions(ctx context.Context) error {
	rows, err := a.db.Query(ctx, `SELECT object_key FROM object_deletions ORDER BY created_at LIMIT 500`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, key := range keys {
		if err := a.s3.RemoveObject(ctx, a.cfg.bucket, key, minio.RemoveObjectOptions{}); err != nil {
			return err
		}
		if _, err := a.db.Exec(ctx, `DELETE FROM object_deletions WHERE object_key=$1`, key); err != nil {
			return err
		}
	}
	_, err = a.db.Exec(ctx, `DELETE FROM pairs p WHERE status='deleting' AND NOT EXISTS(SELECT 1 FROM object_deletions d WHERE d.pair_id=p.id)`)
	return err
}
