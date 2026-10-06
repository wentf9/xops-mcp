# Builds and validation

[简体中文](../../development/build-testing.md) · [Development guide](README.md)

Run from the repository root. Use Go 1.26+ and golangci-lint v2; browser checks also require Node.js 22 and Playwright. Release checks set `GOWORK=off` and do not rely on adjacent checkouts or local replacements.

## Build the actual executable

```sh
mkdir -p bin
GOWORK=off go build -o bin/xops-mcp ./cmd/xops-mcp
```

`-o bin/xops-mcp` sets the executable's output path; `go build ./...` checks that all packages build.

For an isolated local deployment:

```sh
mkdir -m 0700 -p .local
cp examples/server.yaml .local/server.yaml
bin/xops-mcp keygen --out .local/master.key
bin/xops-mcp keygen --out .local/admin.jwt.key
bin/xops-mcp keygen --type rsa --out .local/admin.encryption.key
bin/xops-mcp keygen --out .local/admin.setup
bin/xops-mcp migrate --config .local/server.yaml
bin/xops-mcp serve --config .local/server.yaml
```

Do not blindly regenerate keys in an existing directory. Use a new directory rather than deleting existing user data.

Build the Docker image:

```sh
docker build -t xops-mcp:local .
```

## Go and PostgreSQL

```sh
GOWORK=off go build ./...
GOWORK=off go test ./...
GOWORK=off golangci-lint run ./...
GOWORK=off go mod tidy -diff
GOWORK=off go mod verify
git diff --check
```

Use a disposable PostgreSQL service with a CREATEDB-capable test role. Fixtures create and drop random databases; never supply a production DSN:

```sh
docker run --detach --rm --name xops-mcp-tests -e POSTGRES_PASSWORD=synthetic-test-password -p 127.0.0.1:15432:5432 postgres:18
docker exec xops-mcp-tests pg_isready -U postgres
```

After `pg_isready` reports accepting connections:

```sh
export GOWORK=off
export XOPS_TEST_POSTGRES_DSN='postgres://postgres:synthetic-test-password@127.0.0.1:15432/postgres?sslmode=disable'
XOPS_TEST_BACKEND=sqlite go test -race -count=1 -timeout=120s ./...
XOPS_TEST_BACKEND=postgres go test -race -count=1 -timeout=120s ./...
docker stop xops-mcp-tests
```

PostgreSQL-specific tests skip without a DSN. Explicitly selecting the PostgreSQL backend without a DSN fails. Only evidence with a real participating PostgreSQL server counts as dual-backend acceptance.

## Browser checks

```sh
cd web
npm ci --ignore-scripts
npx playwright install chromium
npm run check
npm run test:browser
```

`npm run check` checks JavaScript syntax and embedded asmcrypto.js against the pinned npm package. Browser acceptance runs UI regressions and both HTTPS/Web Crypto and ordinary HTTP/embedded-encryption protocol flows. Fixtures use temporary certificates, databases and SSH peers, never personal credentials or deployed hosts. Set `XOPS_TEST_CHROME=/path/to/chrome` to use an installed browser. Screenshots go to ignored `web/test-results/`.

After an asmcrypto.js update, run `npm run vendor:crypto` and commit the lockfile, licensed asset and validation together.

## Documentation

- Keep user pages in `docs/user/` and `docs/en/user/`; development material belongs in the matching `development/` directories.
- Check relative links, anchors and bilingual page parity. Update README, AGENTS and command-help references after moves.
- Validate commands using the executable or image specified in the deployment instructions.
- Replay key generation, initialization, import, backup and recovery in isolated temporary directories. Validate RSA with `openssl pkey -check -noout` without printing private material.
- Use isolated image tags, project names, ports and volumes for container checks and verify UID 65532 access. Do not replace or stop deployed user containers.
- Documentation work does not automatically authorize commits or pushes. Identify and preserve existing user edits.

For joint core/server development, use a workspace outside both repositories and explicitly set `GOWORK=/path/to/go.work`. Set `XOPS_TEST_GOWORK=/path/to/go.work` to use it for the browser fixture build. After publishing core and updating the module pin, repeat standalone validation with `GOWORK=off`.
