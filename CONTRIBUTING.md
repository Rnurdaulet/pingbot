# Contributing

```bash
cp config.example.yaml config.yaml
cp .env.example .env
go run ./cmd/pingbot -config config.yaml
```

Keep PRs small. Business logic lives under `internal/`. Don't commit `.env`, `config.yaml`, or `data/`. `go build ./...` should pass.
