# pingbot — deploy without source

Copy this folder to the server, edit configs, run. Uses the prebuilt image
`ghcr.io/rnurdaulet/pingbot:latest` (no Go, no git clone of the whole repo).

## Files

| File | Purpose |
|---|---|
| `docker-compose.yml` | pulls GHCR image, mounts config + data |
| `config.example.yaml` | → copy to `config.yaml` |
| `.env.example` | → copy to `.env` |
| `Caddyfile.example` | snippet if you already run Caddy |

## Setup

```bash
# optional: download only this folder
# git clone --depth 1 --filter=blob:none --sparse https://github.com/Rnurdaulet/pingbot.git
# cd pingbot && git sparse-checkout set deploy/standalone
# cd deploy/standalone

cp config.example.yaml config.yaml
cp .env.example .env
# edit config.yaml (targets) and .env (Telegram tokens)

mkdir -p data
docker network create proxy   # once, if it does not exist

docker compose up -d
curl -s http://localhost:8080/healthz   # only if you published ports
```

Compose does **not** publish `8080` by default — put a reverse proxy on the
`proxy` network in front (see `Caddyfile.example`). For a local smoke test,
uncomment `ports` in `docker-compose.yml`.

### Already have Caddy?

Do **not** start a second Caddy. Join your Caddy container to `proxy` and add
the site block from `Caddyfile.example`.

### No reverse proxy yet?

Use the root-repo overlay instead:

```bash
# from a full clone:
DOMAIN=status.example.com docker compose \
  -f docker-compose.yml -f docker-compose.caddy.yml up -d
```

Or publish `8080` only on localhost and terminate TLS yourself.

## Telegram commands

In the alerts chat (same `TELEGRAM_CHAT_ID`):

- `/status` — current status  
- `/stats` — uptime 24h / 7d / 30d  
- `/help` — list commands  

No public domain required (long polling).

## Upgrade

```bash
docker compose pull
docker compose up -d
```


## Without Docker

Grab a binary from [Releases](https://github.com/Rnurdaulet/pingbot/releases),
keep `config.yaml` + `.env` next to it, run:

```bash
./pingbot -config config.yaml
```
