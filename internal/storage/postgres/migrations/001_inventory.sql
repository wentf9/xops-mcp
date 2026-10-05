-- PostgreSQL has its own schema/version history, independent of SQLite v5.
CREATE TABLE deployment (
 singleton SMALLINT PRIMARY KEY CHECK(singleton=1),
 domain_id TEXT NOT NULL UNIQUE,
 revision BIGINT NOT NULL CHECK(revision>=0), key_check BYTEA NOT NULL
);
CREATE TABLE policies (singleton SMALLINT PRIMARY KEY CHECK(singleton=1), config JSONB NOT NULL);
CREATE TABLE credentials (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
 version TEXT NOT NULL, ciphertext BYTEA NOT NULL
);
CREATE TABLE credential_versions (
 id TEXT NOT NULL, version TEXT NOT NULL, kind TEXT NOT NULL, ciphertext BYTEA NOT NULL,
 PRIMARY KEY(id,version)
);
CREATE TABLE hosts (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, address TEXT NOT NULL,
 port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535), host_key TEXT NOT NULL
);
CREATE TABLE identities (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, username TEXT NOT NULL,
 credential_id TEXT REFERENCES credentials(id)
);
CREATE TABLE nodes (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
 host_id TEXT NOT NULL REFERENCES hosts(id), identity_id TEXT NOT NULL REFERENCES identities(id),
 sudo_mode TEXT NOT NULL, privilege_credential_id TEXT REFERENCES credentials(id), disabled BOOLEAN NOT NULL
);
CREATE TABLE node_jumps (
 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 position INTEGER NOT NULL CHECK(position BETWEEN 0 AND 30),
 jump_id TEXT NOT NULL REFERENCES nodes(id) DEFERRABLE INITIALLY DEFERRED,
 PRIMARY KEY(node_id,position), UNIQUE(node_id,jump_id)
);
CREATE TABLE aliases (alias TEXT PRIMARY KEY, node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE);
CREATE TABLE tags (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE node_tags (
 node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 tag_id TEXT NOT NULL REFERENCES tags(id), PRIMARY KEY(node_id,tag_id)
);
CREATE TABLE tombstones (id TEXT PRIMARY KEY);
CREATE TABLE sources (
 token TEXT NOT NULL, node_id TEXT NOT NULL, host TEXT NOT NULL, port INTEGER NOT NULL,
 username TEXT NOT NULL, purpose TEXT NOT NULL,
 credential_id TEXT NOT NULL, version TEXT NOT NULL, host_key TEXT NOT NULL,
 PRIMARY KEY(token,node_id,host,port,username,purpose)
);
