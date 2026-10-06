CREATE TABLE mcp_tokens (
    id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL,
    digest TEXT NOT NULL UNIQUE,
    version INTEGER NOT NULL CHECK(version > 0),
    enabled INTEGER NOT NULL CHECK(enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER NOT NULL
);
CREATE INDEX mcp_tokens_client ON mcp_tokens(client_id);
