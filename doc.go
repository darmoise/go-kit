// Package gokit is a collection of Go libraries for HTTP services. The root package is empty;
// the functionality lives in subpackages:
//
//   - httpx — JSON responses and errors, middleware (RequestID, Recover, AccessLog), ClientIP, BearerToken;
//   - config — reading environment variables with error accumulation;
//   - httpserver — HTTP server with graceful shutdown and a healthz handler;
//   - servicetoken — Bearer token checks for internal HTTP APIs;
//   - webhook — signing and delivery of outgoing events;
//   - pgdb — Postgres connection pool and forward-only migrations;
//   - pgdb/pgtest — isolated test schema;
//   - jwtx — signing and verification of access JWTs, JWKS;
//   - sse — writing (Writer) and parsing (Reader) Server-Sent Events.
//
// The packages contain no application-specific logic.
package gokit
