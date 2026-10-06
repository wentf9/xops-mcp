# Development instructions

## Scope and status

This repository is the standalone XOps MCP server product. Read `docs/development/architecture.md` and `docs/development/roadmap.md` before implementation. M2 provides the SQLite-backed HTTP server, offline import/recovery commands, and persistent core adapters. M3 adds embedded Web management, administrator authentication, live API edits, and deployment examples. M4 adds PostgreSQL and encrypted offline database export/import/verification. Both backends remain single-instance with a local transfer journal. Administrator authentication now uses 15-minute stateless JWTs and JWE password requests over configurable HTTP/HTTPS; SQLite v6 and PostgreSQL v3 remove legacy session tables. See `docs/development/admin-auth.md`. MCP clients use database-managed token digests and separate client IDs (SQLite v7/PostgreSQL v4); see `docs/development/mcp-auth.md`.

## Dependency ownership

- Keep the dependency one-way: `xops-mcp` imports pinned public packages from `xops-cli`. Never introduce the reverse dependency.
- Shared SSH/SFTP/MCP behavior belongs to the upstream packages. Do not copy or fork their implementations here.
- Keep database models, Web APIs, migrations, and concrete server adapters in this repository.
- Do not import upstream command/TUI packages or directly import upstream `internal` packages.
- Do not commit local `replace` directives, `go.work`, or unversioned dependencies. Run release checks with `GOWORK=off`.
- Planned interfaces must be documented as planned until implemented and tested.

## Coding and lifecycle

- Go 1.26+; preserve Go initialisms such as ID, URL, SSH, CLI, SFTP, and TCP.
- Every connection, file, response body, and runtime has a deterministic close path. Use deferred cleanup and handle close errors.
- Every goroutine has a documented cancellation path and bounded completion. Pass context and enforce network/database deadlines.
- Wrap errors with context; do not swallow errors, panic, or append punctuation/newlines to error messages.
- Protect shared state with appropriate synchronization and avoid holding locks over network I/O.
- Keep credentials out of DTOs and logs. Use deployment-owned synthetic fixtures in tests, never personal configuration or credentials.

## Tests, documentation, and Git

- Add tests for every new capability, core behavior, or bug fix. Prefer contract and lifecycle behavior over tests that mirror implementation.
- Before every code push or PR, pass `go build ./...`, `go test ./...`, and `golangci-lint run ./...` locally.
- Use golangci-lint v2; run `golangci-lint config verify` after changing its configuration.
- Run relevant race/lifecycle tests. Distinguish protocol discovery from real SSH/SFTP validation.
- Run the same business tests with `XOPS_TEST_BACKEND=sqlite` and `postgres`; PostgreSQL tests require `XOPS_TEST_POSTGRES_DSN` pointing to a disposable server with CREATEDB permission. See `docs/development/build-testing.md`.
- Update corresponding Chinese and English documentation when changing described behavior.
- Use conventional commits: `feat:`, `fix:`, `chore:`, `ci:`, `docs:`, or `test:`.
- Do not infer authorization to modify or publish the upstream repository from work requested here.

## Documentation

- Keep operator instructions in `docs/user/` and `docs/en/user/`; use task-oriented language without milestones, source ownership, fixtures, test evidence, API internals, or implementation status.
- Keep build, test, API, architecture, and roadmap material in `docs/development/` and `docs/en/development/`.
- Validate documented commands using the same executable or image referenced by the instructions. `go build ./...` does not refresh `bin/xops-mcp`; an explicit `-o` build does. Container instructions must use the container image for key generation and administration.
