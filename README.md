# go-kit

A small collection of Go libraries for HTTP services: HTTP helpers and middleware, env-based config, an HTTP server with graceful shutdown, service token checks, webhook signing and delivery, SSE streams, Postgres helpers and JWT verification.

Not affiliated with github.com/go-kit/kit.

## Packages

`httpx`, `config`, `httpserver`, `servicetoken`, `webhook` and `sse` use only the standard library; `pgdb` and `pgdb/pgtest` depend on `pgx/v5`, `jwtx` on `golang-jwt/jwt/v5`. The import path is `github.com/darmoise/go-kit`, the root package name is `gokit`.

- `httpx` — JSON responses and errors, middleware (RequestID, Recover, AccessLog), ClientIP, BearerToken.
- `config` — reading environment variables with error accumulation.
- `httpserver` — HTTP server with graceful shutdown and a healthz handler.
- `servicetoken` — Bearer token checks for internal HTTP APIs.
- `webhook` — signing and delivery of outgoing events.
- `pgdb` — Postgres connection pool with a per-application `search_path` and forward-only migrations (`Up`, `Pending`).
- `pgdb/pgtest` — isolated test schema based on `TEST_DATABASE_URL`.
- `jwtx` — signing and verification of access JWTs (EdDSA), JWKS, a verifier with a key cache.
- `sse` — writing (Writer) and parsing (Reader) Server-Sent Events.

## Installation

```
go get github.com/darmoise/go-kit@latest
```

## Development

```
go build ./... && go vet ./... && go test ./...
```

The `pgdb` integration tests read `TEST_DATABASE_URL` (for example `postgres://postgres@localhost:5432/postgres?sslmode=disable`) and are skipped without it; the restricted-role test requires privileges for `CREATE ROLE` and `CREATE SCHEMA`.

## License

MIT, see [LICENSE](LICENSE).
