package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type config struct {
	token, bucket string
	allowed       map[int64]bool
	adminID       int64
	admins        map[int64]bool // may approve access requests; defaults to allowed
}

type app struct {
	cfg              config
	db               *pgxpool.Pool
	s3               *minio.Client
	http             *http.Client
	apiURL           string
	broadcastPending bool
	exporting        sync.Map // user IDs with a /download export running
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	a := &app{cfg: cfg, db: db, http: &http.Client{Timeout: 75 * time.Second}, apiURL: "https://api.telegram.org/bot" + cfg.token + "/"}
	if err := retry(ctx, func() error { return db.Ping(ctx) }); err != nil {
		log.Fatal(err)
	}
	for _, statement := range strings.Split(schema, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.Exec(ctx, statement); err != nil {
			log.Fatal(err)
		}
	}
	if err := a.loadAllowlist(ctx); err != nil {
		log.Fatal(err)
	}
	a.s3, err = minio.New(os.Getenv("S3_ENDPOINT"), &minio.Options{Creds: credentials.NewStaticV4(os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), ""), Secure: false})
	if err != nil {
		log.Fatal(err)
	}
	if err := retry(ctx, func() error { return a.ensureBucket(ctx) }); err != nil {
		log.Fatal(err)
	}
	if err := a.processDeletions(ctx); err != nil {
		log.Printf("deletion cleanup: %v", err)
	}
	// Polling needs no public web server or TLS certificate. A webhook would consume the same updates.
	if err := a.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": false}, nil); err != nil {
		log.Fatal(err)
	}
	if err := a.registerMenu(ctx); err != nil {
		log.Fatal(err)
	}
	log.Print("bot started")
	a.poll(ctx)
}

func loadConfig() (config, error) {
	c := config{token: os.Getenv("TELEGRAM_BOT_TOKEN"), bucket: os.Getenv("S3_BUCKET")}
	if c.token == "" || c.bucket == "" {
		return c, errors.New("TELEGRAM_BOT_TOKEN and S3_BUCKET are required")
	}
	var err error
	if c.allowed, err = parseIDs("ALLOWED_USER_IDS"); err != nil {
		return c, err
	}
	if raw := strings.TrimSpace(os.Getenv("ADMIN_TELEGRAM_ID")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return c, errors.New("ADMIN_TELEGRAM_ID must be a positive Telegram numeric user ID")
		}
		c.adminID = id
		c.allowed[c.adminID] = true
	}
	if len(c.allowed) == 0 {
		return c, errors.New("ALLOWED_USER_IDS or ADMIN_TELEGRAM_ID must contain a Telegram numeric user ID")
	}
	if c.admins, err = parseIDs("ADMIN_USER_IDS"); err != nil {
		return c, err
	}
	if len(c.admins) == 0 {
		c.admins = c.allowed
	}
	return c, nil
}

func parseIDs(env string) (map[int64]bool, error) {
	ids := map[int64]bool{}
	for _, raw := range strings.Split(os.Getenv(env), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid %s entry %q", env, raw)
		}
		ids[id] = true
	}
	return ids, nil
}

func retry(ctx context.Context, fn func() error) error {
	var err error
	for i := 0; i < 30; i++ {
		if err = fn(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return err
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
