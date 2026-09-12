# Repository Guidelines

## Project Structure & Module Organization

- `api-go/cmd/server/` contains the Go entry point; business code lives under `api-go/internal/` (`handlers`, `middleware`, `config`, and `db`). Tests sit beside Go source files.
- `api-go/migrations/` contains sequential PostgreSQL migrations. Add a new numbered file instead of changing an applied migration.
- `frontend/legacy-gas/` and `frontend/_build/` are frontend sources. `frontend/index.html` and `frontend/publico.html` are generated artifacts; do not edit them directly.
- `migration-python/` imports source spreadsheets into PostgreSQL. Real `.xlsx` data is local-only.
- `docs/` holds setup notes, seed data, and project records.

Keep new code, comments, database names, and user-facing text in Spanish, matching the existing project.

## Build, Test, and Development Commands

Run commands from the repository root unless noted:

```bash
docker compose up --build                 # Build and run PostgreSQL plus the API
docker compose up -d postgres             # Start only the development database
cd api-go && go run ./cmd/server          # Run the API locally
cd api-go && go build ./... && go vet ./... && go test ./...
cd api-go && gofmt -w .                    # Format Go source
python3 frontend/_build/assemble.py        # Regenerate committed frontend outputs
```

For local API work, set `DATABASE_URL`, `PORT=8080`, and `STATIC_DIR=../frontend`. Verify the running service with `curl http://localhost:8080/api/health`.

## Coding Style & Naming Conventions

Use `gofmt` and idiomatic Go names. Keep handlers grouped by feature and name tests `feature_test.go`. Read environment variables only in `internal/config`. Preserve the API response shape `{"ok": true}` or `{"ok": false, "error": "..."}`. Route relative `/api/...` frontend calls through existing authentication helpers rather than raw `fetch()`.

## Testing Guidelines

Tests use Go's `testing` package and real PostgreSQL. Start Postgres and export `DATABASE_URL` before testing; database cases otherwise call `t.Skip`, making a green run incomplete. Run one case with:

```bash
cd api-go && go test ./internal/handlers -run TestLogin_PinIncorrecto
```

No coverage threshold is enforced. Test success, validation, authorization, and persistence paths. Use `api-go/requests.http` for manual smoke tests.

## Commit & Pull Request Guidelines

History uses short Spanish summaries such as `Fix de usuarios...`; Conventional Commits are not enforced. Keep commits focused on one outcome. Pull requests should summarize scope, list verification commands, link the issue or sprint item, and include screenshots for UI changes. Commit regenerated frontend outputs with their sources.

## Security & Configuration

Copy `.env.example` to `.env`; never commit secrets, PINs, or source spreadsheets. Keep authentication failures indistinguishable and never log PINs. Retain Go 1.22 unless the Docker builder is upgraded too.
