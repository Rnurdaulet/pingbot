# pingbot

[![CI](https://github.com/Rnurdaulet/pingbot/actions/workflows/ci.yml/badge.svg)](https://github.com/Rnurdaulet/pingbot/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Rnurdaulet/pingbot)](https://github.com/Rnurdaulet/pingbot/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
![Go](https://img.shields.io/badge/go-1.26-blue)

HTTP uptime checks → Telegram alerts. Single Go binary, YAML config, no UI. Sit it behind Caddy/nginx/Traefik.

Anti-flap thresholds, `up`/`down`/`degraded`, SQLite or Postgres history, optional healthchecks.io heartbeat so you notice if pingbot itself dies.

## Run

**Server without source** (prebuilt GHCR image only): copy [deploy/standalone/](deploy/standalone/) — compose, `config` / `.env` examples, Caddy snippet. See that folder's README.

**From this repo** (build locally):

```bash
cp config.example.yaml config.yaml
cp .env.example .env
# edit both, then:

docker network create proxy   # once
docker compose up -d --build
```

Or use the image: `image: ghcr.io/rnurdaulet/pingbot:latest` and drop `build: .`.

Without Docker: `go build -o pingbot ./cmd/pingbot && ./pingbot -config config.yaml`, or grab a binary from [Releases](https://github.com/Rnurdaulet/pingbot/releases).

`curl http://localhost:8080/healthz` → `ok` (uncomment `ports` in compose if you are not using a proxy).

No reverse proxy on the box? Use the Caddy overlay (TLS via Let's Encrypt):

```bash
DOMAIN=status.example.com docker compose -f docker-compose.yml -f docker-compose.caddy.yml up -d
```

## Config

See [config.example.yaml](config.example.yaml). `${VAR}` expands from the environment.

| Field | Default | Notes |
|---|---|---|
| `method` | `GET` | |
| `expect_status` | `[200]` | |
| `interval` / `timeout` | `30s` / `5s` | |
| `max_latency_ms` | off | slower → `degraded` |
| `fail_threshold` / `ok_threshold` | `3` / `2` | anti-flap |

### Telegram setup

1. Open [@BotFather](https://t.me/BotFather) → `/newbot` → copy the token into `TELEGRAM_BOT_TOKEN`.
2. Add the bot to your alerts group. For a private group also send any message to the group so the bot sees an update.
3. Get the group `chat_id`:

```bash
curl -s "https://api.telegram.org/bot<TOKEN>/getUpdates" | jq '.result[].message.chat.id'
```

Group ids are negative (`-100...` for supergroups) — copy the value including the minus into `TELEGRAM_CHAT_ID`. If the output is empty, send another message to the group and retry, or check that the bot was actually added.

4. Verify before the first run:

```bash
curl -s -X POST "https://api.telegram.org/bot<TOKEN>/sendMessage" \
  -d chat_id=<CHAT_ID> -d text="pingbot test"
```

The message should appear in the group. Note: by default bots in groups have "privacy mode" on — pingbot only sends messages, so this does not matter for alerts.

On start pingbot also sends a Telegram summary with each target and last-known status from the DB (`telegram.startup_notify`, default on). Set `require_on_start: true` to exit if that send fails.

In the alerts chat you can also ask the bot (no domain needed):

- `/status` — current status per target  
- `/stats` (or `/history`) — uptime for 24h / 7d / 30d  
- `/help` — command list  

Only messages from `TELEGRAM_CHAT_ID` are answered.

### Heartbeat

Put a [healthchecks.io](https://healthchecks.io) ping URL in `HEARTBEAT_URL`.

Deploy pack: [deploy/standalone/](deploy/standalone/). Proxy snippets: [deploy/](deploy/). Compose does not publish `8080` — only the `proxy` network.

## Storage

```yaml
storage:
  driver: sqlite          # or postgres
  path: data/pingbot.db   # sqlite
  # dsn: ${DATABASE_URL}  # postgres
```

## Stats

`GET /api/stats` — status + uptime for 24h / 7d / 30d. If `PINGBOT_API_TOKEN` is set:

```bash
curl -s -H "Authorization: Bearer $PINGBOT_API_TOKEN" http://localhost:8080/api/stats | jq
```

## Security

Config is trusted (the checker will hit any URL you give it, including internal ones). Protect `/api/*` with `PINGBOT_API_TOKEN` or proxy ACL. Details: [SECURITY.md](SECURITY.md).

## Publishing checklist (maintainer)

One-time steps after creating the GitHub repo:

1. `git remote add origin git@github.com:Rnurdaulet/pingbot.git && git push -u origin main`
2. Settings → Security: enable **Private vulnerability reporting**
3. Add topics: `go`, `monitoring`, `uptime`, `telegram`, `sqlite`
4. `git tag v0.1.0 && git push --tags` — Actions builds binaries (GoReleaser) and the multi-arch image to GHCR

## License

MIT — see [LICENSE](LICENSE). Contributions welcome; see [CONTRIBUTING.md](CONTRIBUTING.md).
