# Voice sample bot

A private Telegram bot for collecting paired sleepy and awake voice notes. The bot is written in Go. PostgreSQL stores pairs, transcripts, Telegram metadata, and in-progress sessions; MinIO stores the audio in a private S3-compatible bucket. All three services run in one Docker Compose project.

## Start

1. Create a bot with Telegram's BotFather and copy its token.
2. Copy `.env.example` to `.env`. Set `TELEGRAM_BOT_TOKEN`, the admin's numeric `ADMIN_TELEGRAM_ID` (several admins: comma-separated IDs), and two long, different passwords. `ALLOWED_USER_IDS` is optional for seeding initial users. Keep `.env` private.
3. Run `docker compose up --build -d`.
4. Run `docker compose logs -f bot` to confirm the bot started, then open a private chat with it and send `/start`.

The first build needs internet access to download Go modules and container images. The bot polls Telegram, so no inbound port or public domain is needed. Only allowlisted Telegram user IDs can use it, and it ignores group chats. Admins can change the allowlist through the bot; changes are saved in PostgreSQL and survive restarts. IDs in `ALLOWED_USER_IDS` seed the list at startup, while admin removals take precedence over that env value.

## Access requests

A user who is not on the allowlist is offered a **Request access** button. Pressing it sends the admins a message with the person's name, @username, a link to their account, their numeric ID and, when they have one, their profile photo, with **Accept** and **Deny** buttons. Admins are the numeric IDs in the optional `ADMIN_USER_IDS` (comma-separated); if it is unset, every ID in `ALLOWED_USER_IDS` is an admin. Users in `ADMIN_TELEGRAM_ID` can always answer requests. Admins in `ADMIN_USER_IDS` alone can only answer requests; they get none of the commands below. Admins must have opened a chat with the bot to receive requests, and only admins can press the buttons.

Accepting takes effect immediately and is saved in the `access_requests` table, so it survives restarts and works alongside `ALLOWED_USER_IDS`. A user with a pending request is told it is pending instead of sending another. A denied user cannot request again; to let them re-request, run `docker compose exec postgres psql -U voicebot -d voicebot -c "DELETE FROM access_requests WHERE user_id=<id>;"`. To revoke an approval, delete the same row. The first admin to answer decides; other admins' copies of the message are cleaned up when they press a button.

## Participant flow

- `/start` offers English or فارسی. The choice is saved per participant and can be changed by sending `/start` again; it changes the bot's prompts and buttons, not the recordings or transcripts.
- The welcome message explains Professor Zahedi's Statistical Machine Learning assignment: 20 sleepy/awake pairs of about five seconds each for a semester-end machine learning dataset, and credits @AliSohani2082. Telegram's bottom Menu lists the commands; inline buttons suggest the next action.
- `/onboard` collects the details the dataset needs: gender, age, first name, last name, and student number (شماره دانشجویی). New participants are asked right after choosing a language, one question at a time, and a half-finished questionnaire survives a bot restart. Persian and Arabic-Indic digits are accepted, so `۲۲` works for an age. A student number already registered to another Telegram account is refused; transferring one means deleting the old `profiles` row.
- `/profile` shows the stored details with a button per field, so a single typo can be fixed without answering everything again. Running `/onboard` once the profile is complete shows the same card, and running `/profile` before it exists starts the questionnaire, so either command does the sensible thing.
- `/new` requires a complete profile: without one it starts the questionnaire instead of a pair. If a pair is already unfinished, `/cancel` it first. Otherwise it asks for a sleepy Telegram voice note, then offers **Submit** or **Record a new one**.
- The bot repeats this for the awake voice and reminds participants to say the same sentence. After both recordings, it asks for the sentence once for the pair. Each later pair may use another sentence.
- Aim for five seconds. Telegram voice notes reporting more than ten seconds are rejected. The bot does not transcribe audio or compare the two typed sentences.
- `/my` lists submitted pairs and offers controls to replay or replace either recording, edit the pair transcript, or permanently delete a pair and both audio files.
- `/download` exports your own submitted pairs (all, this week, or today, by Asia/Tehran dates) as WAV files in a zip, one folder per day; large exports arrive as several zips.
- `/leader d` ranks today's top recorders by completed pairs and `/leader w` ranks the current week, which starts on Saturday in Asia/Tehran. Add `l` — `/leader w l` — for a longer board with recordings and audio minutes per person, plus your own rank. Leave the day or week out and the bot asks which you meant. Participants are shown by the name they entered in `/onboard`, so the board is visible to the whole class; admins are not ranked. A contributor from before onboarding existed appears under the first name Telegram reports, or anonymously if the bot has not seen them since — never as a numeric ID. Both fall away once they send a message or complete `/onboard`. Deleting a pair removes its leaderboard credit.
- `/cancel` discards an unfinished pair, or abandons a half-finished questionnaire or broadcast prompt. Starting `/new` while a pair is unfinished asks for confirmation before discarding it. In-progress work survives a bot restart.

## Commands with arguments

Commands are described in one place, `cmd/voicebot/registry.go`, as a name plus the arguments it takes. An argument is either one of a fixed set of values (`d` or `w` for `/leader`, `all`/`week`/`today` for `/download`), a flag that is simply present or absent (`l`), or free text (a student number, a broadcast body). Each one is mandatory or optional.

Leaving a mandatory argument out is not an error: the bot asks for it. A fixed set of values becomes a row of buttons, and free text is answered with the next message. So `/leader` and `/leader w` both work, and so do `/download today` and `/start fa`. Answering by button is identical to having typed the argument, because each button carries the whole command line it replays. The same description generates Telegram's command menu, so there is one list to keep up to date.

## Admin commands

Each user in `ADMIN_TELEGRAM_ID` (one ID or several, comma-separated) can use `/stats` for user, pair, recording, and storage totals; `/users` to browse users and open their submitted pairs; `/allow 123456789` and `/unallow 123456789` to change access; and `/broadcast` to be prompted for a message in the next chat message. Use `/cancel` to cancel the prompt. Adding an ID also puts it in `/users` and broadcast attempts, but the recipient must send `/start` before the bot can deliver messages. A user's record view also has an allowlist button. Admins can read transcripts and play recordings, but the admin view has no edit or delete controls. Each admin gets the replies to their own commands. Broadcasts exclude all admins and report failed deliveries.

A user's record view in `/users` also shows their onboarding details, since that is the metadata the dataset is for.

The bot stores the participant's Telegram numeric ID, the first name and username Telegram reports, the gender, age, first name, last name and student number entered in `/onboard`, and per recording the Telegram file ID and unique file ID, reported duration, downloaded byte count, MIME type, transcript, timestamps, and bucket object key. The recordings remain private in MinIO. Note that `/leader` shows participants' entered names to each other; nothing else about a profile is visible to other participants. A startup and periodic cleanup job retries failed object deletions.

## Tests

`go test ./...` needs no services. The tests that can only be shown against a real database — which commands leave a half-finished question behind, whether the rank a participant is told matches the board they see, and that the schema upgrades a database created before profiles existed — are skipped unless `VOICEBOT_TEST_DSN` points at a scratch PostgreSQL:

```sh
VOICEBOT_TEST_DSN='postgres://voicebot:PASSWORD@127.0.0.1:5432/scratch' go test ./cmd/voicebot
```

That database is written to, so never point it at collected data.

## Access the data

Open Adminer at `http://127.0.0.1:8080` to browse and edit PostgreSQL data. Select **PostgreSQL** and sign in with server `postgres`, user `voicebot`, database `voicebot`, and `POSTGRES_PASSWORD` from `.env`.

The MinIO console is available on the Docker host at `http://127.0.0.1:9001` with `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD` from `.env`. The S3 API is at `http://127.0.0.1:9000`; the bucket defaults to `voice-samples`. Object keys look like `<telegram-user-id>/<pair-id>/<sleepy-or-awake>/<random-id>.ogg`.

For metadata, run:

```sh
docker compose exec postgres psql -U voicebot -d voicebot -c "SELECT p.id, p.user_id, p.completed_at, s.kind, s.transcript, s.duration_seconds, s.file_size_bytes, s.mime_type, s.object_key FROM pairs p JOIN samples s ON s.pair_id=p.id WHERE p.status='complete' ORDER BY p.completed_at DESC, s.kind;"
```

Compose volumes `postgres_data` and `minio_data` persist across container restarts. Back up both volumes together if you need a recoverable copy of the collection. Do not use `docker compose down -v` unless you intend to erase all stored data.
# voice-sampler-bot
