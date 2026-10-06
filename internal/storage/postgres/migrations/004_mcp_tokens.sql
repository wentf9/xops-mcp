CREATE TABLE mcp_tokens (
    id TEXT PRIMARY KEY,
    client_id TEXT NOT NULL,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL,
    digest TEXT NOT NULL UNIQUE,
    version BIGINT NOT NULL CHECK(version > 0),
    enabled BOOLEAN NOT NULL,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    revoked_at BIGINT NOT NULL
);
CREATE INDEX mcp_tokens_client ON mcp_tokens(client_id);
