# Security

Report vulnerabilities privately (GitHub Security Advisories once published, or contact the maintainer). Don't open a public issue.

## Trust model

- **`config.yaml` is trusted.** The checker requests whatever URLs you configure, including localhost / metadata / private nets. Restrict who can edit config and `.env`.
- **`/api/stats` leaks target names and incident details.** Set `server.api_token` (`PINGBOT_API_TOKEN`) or lock it down at the proxy. `/healthz` can stay open.
- **No TLS in pingbot itself** — put a reverse proxy in front; don't expose `:8080`.
- Alert text may include hostnames / TLS errors; treat the Telegram chat as trusted.
- Use `sslmode=require` for Postgres in production. Never commit `.env`.
