# Voice sample bot

A private Telegram bot for collecting paired sleepy and awake voice notes. The bot is written in Go. PostgreSQL stores pairs, transcripts, Telegram metadata, and in-progress sessions; MinIO stores the audio in a private S3-compatible bucket. All three services run in one Docker Compose project.

## Start

1. Create a bot with Telegram's BotFather and copy its token.
2. Copy `.env.example` to `.env`. Set `TELEGRAM_BOT_TOKEN`, the admin's numeric `ADMIN_TELEGRAM_ID`, and two long, different passwords. `ALLOWED_USER_IDS` is optional for seeding initial users. Keep `.env` private.
3. Run `docker compose up --build -d`.
4. Run `docker compose logs -f bot` to confirm the bot started, then open a private chat with it and send `/start`.

The first build needs internet access to download Go modules and container images. The bot polls Telegram, so no inbound port or public domain is needed. Only allowlisted Telegram user IDs can use it, and it ignores group chats. The admin can change the allowlist through the bot; changes are saved in PostgreSQL and survive restarts. IDs in `ALLOWED_USER_IDS` seed the list at startup, while admin removals take precedence over that env value.

## Access requests

A user who is not on the allowlist is offered a **Request access** button. Pressing it sends the admins a message with the person's name, @username, a link to their account, their numeric ID and, when they have one, their profile photo, with **Accept** and **Deny** buttons. Admins are the numeric IDs in the optional `ADMIN_USER_IDS` (comma-separated); if it is unset, every ID in `ALLOWED_USER_IDS` is an admin. Admins must have opened a chat with the bot to receive requests, and only admins can press the buttons.

Accepting takes effect immediately and is saved in the `access_requests` table, so it survives restarts and works alongside `ALLOWED_USER_IDS`. A user with a pending request is told it is pending instead of sending another. A denied user cannot request again; to let them re-request, run `docker compose exec postgres psql -U voicebot -d voicebot -c "DELETE FROM access_requests WHERE user_id=<id>;"`. To revoke an approval, delete the same row. The first admin to answer decides; other admins' copies of the message are cleaned up when they press a button.

## Participant flow

- `/start` offers English or فارسی. The choice is saved per participant and can be changed by sending `/start` again; it changes the bot's prompts and buttons, not the recordings or transcripts.
- The welcome message explains Professor Zahedi's Statistical Machine Learning assignment: 20 sleepy/awake pairs of about five seconds each for a semester-end machine learning dataset, and credits @AliSohani2082. Telegram's bottom Menu lists the commands; inline buttons suggest the next action.
- `/new` starts a new pair. The bot asks for a sleepy Telegram voice note, then offers **Submit** or **Record a new one**.
- The bot repeats this for the awake voice and reminds participants to say the same sentence. After both recordings, it asks for the sentence once for the pair. Each later pair may use another sentence.
- Aim for five seconds. Telegram voice notes reporting more than ten seconds are rejected. The bot does not transcribe audio or compare the two typed sentences.
- `/my` lists submitted pairs and offers controls to replay or replace either recording, edit the pair transcript, or permanently delete a pair and both audio files.
- `/cancel` discards an unfinished pair. Starting `/new` while a pair is unfinished asks for confirmation before discarding it. In-progress work survives a bot restart.

## Admin commands

The `ADMIN_TELEGRAM_ID` user can use `/stats` for user, pair, recording, and storage totals; `/users` to browse users and open their submitted pairs; `/allow 123456789` and `/unallow 123456789` to change access; and `/broadcast` to be prompted for a message in the next chat message. Use `/cancel` to cancel the prompt. Adding an ID also puts it in `/users` and broadcast attempts, but the recipient must send `/start` before the bot can deliver messages. A user's record view also has an allowlist button. The admin can read transcripts and play recordings, but the admin view has no edit or delete controls. Broadcasts exclude the admin and report failed deliveries.

The bot stores the participant's Telegram numeric ID, Telegram file ID and unique file ID, reported duration, downloaded byte count, MIME type, transcript, timestamps, and bucket object key. The recordings remain private in MinIO. A startup and periodic cleanup job retries failed object deletions.

## Access the data

Open Adminer at `http://127.0.0.1:8080` to browse and edit PostgreSQL data. Select **PostgreSQL** and sign in with server `postgres`, user `voicebot`, database `voicebot`, and `POSTGRES_PASSWORD` from `.env`.

The MinIO console is available on the Docker host at `http://127.0.0.1:9001` with `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD` from `.env`. The S3 API is at `http://127.0.0.1:9000`; the bucket defaults to `voice-samples`. Object keys look like `<telegram-user-id>/<pair-id>/<sleepy-or-awake>/<random-id>.ogg`.

For metadata, run:

```sh
docker compose exec postgres psql -U voicebot -d voicebot -c "SELECT p.id, p.user_id, p.completed_at, s.kind, s.transcript, s.duration_seconds, s.file_size_bytes, s.mime_type, s.object_key FROM pairs p JOIN samples s ON s.pair_id=p.id WHERE p.status='complete' ORDER BY p.completed_at DESC, s.kind;"
```

Compose volumes `postgres_data` and `minio_data` persist across container restarts. Back up both volumes together if you need a recoverable copy of the collection. Do not use `docker compose down -v` unless you intend to erase all stored data.
# voice-sampler-bot
