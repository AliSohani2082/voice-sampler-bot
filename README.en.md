# Voice sample bot

A private Telegram bot for collecting paired sleepy and awake voice notes. The bot is written in Go. PostgreSQL stores pairs, transcripts, Telegram metadata, and in-progress sessions; MinIO stores the audio in a private S3-compatible bucket. All three services run in one Docker Compose project.

## Start

1. Create a bot with Telegram's BotFather and copy its token.
2. Copy `.env.example` to `.env`. Set `TELEGRAM_BOT_TOKEN`, comma-separated numeric `ALLOWED_USER_IDS`, and two long, different passwords. Keep `.env` private.
3. Run `docker compose up --build -d`.
4. Run `docker compose logs -f bot` to confirm the bot started, then open a private chat with it and send `/start`.

The first build needs internet access to download Go modules and container images. The bot polls Telegram, so no inbound port or public domain is needed. Only allowlisted Telegram user IDs can use it, and it ignores group chats. To change the allowlist, edit `.env` and run `docker compose up -d --force-recreate bot`.

## Participant flow

- `/start` offers English or فارسی. The choice is saved per participant and can be changed by sending `/start` again; it changes the bot's prompts and buttons, not the recordings or transcripts.
- The welcome message explains Professor Zahedi's Statistical Machine Learning assignment: 20 sleepy/awake pairs of about five seconds each for a semester-end machine learning dataset, and credits @ali2082. Telegram's bottom Menu lists the commands; inline buttons suggest the next action.
- `/new` starts a new pair. The bot asks for a sleepy Telegram voice note, then offers **Submit** or **Record a new one**. It asks the participant to type what they said.
- The bot repeats this for the awake voice and reminds them to say the same sentence. Each later pair may use another sentence.
- Aim for five seconds. Telegram voice notes reporting more than ten seconds are rejected. The bot does not transcribe audio or compare the two typed sentences.
- `/my` lists submitted pairs and offers controls to replay either recording, replace either recording, edit either transcript, or permanently delete a pair and both audio files.
- `/cancel` discards an unfinished pair. Starting `/new` while a pair is unfinished asks for confirmation before discarding it. In-progress work survives a bot restart.

The bot stores the participant's Telegram numeric ID, Telegram file ID and unique file ID, reported duration, downloaded byte count, MIME type, transcript, timestamps, and bucket object key. The recordings remain private in MinIO. A startup and periodic cleanup job retries failed object deletions.

## Access the data

The MinIO console is available on the Docker host at `http://127.0.0.1:9001` with `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD` from `.env`. The S3 API is at `http://127.0.0.1:9000`; the bucket defaults to `voice-samples`. Object keys look like `<telegram-user-id>/<pair-id>/<sleepy-or-awake>/<random-id>.ogg`.

For metadata, run:

```sh
docker compose exec postgres psql -U voicebot -d voicebot -c "SELECT p.id, p.user_id, p.completed_at, s.kind, s.transcript, s.duration_seconds, s.file_size_bytes, s.mime_type, s.object_key FROM pairs p JOIN samples s ON s.pair_id=p.id WHERE p.status='complete' ORDER BY p.completed_at DESC, s.kind;"
```

Compose volumes `postgres_data` and `minio_data` persist across container restarts. Back up both volumes together if you need a recoverable copy of the collection. Do not use `docker compose down -v` unless you intend to erase all stored data.
# voice-sampler-bot
