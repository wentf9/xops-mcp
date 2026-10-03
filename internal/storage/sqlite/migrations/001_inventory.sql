CREATE TABLE deployment (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  domain_id TEXT NOT NULL UNIQUE,
  revision INTEGER NOT NULL CHECK (revision >= 0),
  key_check BLOB NOT NULL
);
CREATE TABLE policies (singleton INTEGER PRIMARY KEY CHECK (singleton = 1), config BLOB NOT NULL);
CREATE TABLE credentials (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, kind TEXT NOT NULL,
  version TEXT NOT NULL, ciphertext BLOB NOT NULL
);
CREATE TABLE credential_versions (
  id TEXT NOT NULL, version TEXT NOT NULL, kind TEXT NOT NULL, ciphertext BLOB NOT NULL,
  PRIMARY KEY (id, version)
);
CREATE TABLE hosts (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, address TEXT NOT NULL,
  port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535), host_key TEXT NOT NULL
);
CREATE TABLE identities (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, username TEXT NOT NULL,
  credential_id TEXT REFERENCES credentials(id)
);
CREATE TABLE nodes (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE,
  host_id TEXT NOT NULL REFERENCES hosts(id), identity_id TEXT NOT NULL REFERENCES identities(id),
  jump_id TEXT REFERENCES nodes(id) DEFERRABLE INITIALLY DEFERRED,
  sudo_mode TEXT NOT NULL, privilege_credential_id TEXT REFERENCES credentials(id),
  disabled INTEGER NOT NULL CHECK (disabled IN (0, 1))
);
CREATE TABLE aliases (alias TEXT PRIMARY KEY, node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE);
CREATE TABLE tags (name TEXT PRIMARY KEY);
CREATE TABLE node_tags (
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  tag TEXT NOT NULL REFERENCES tags(name), PRIMARY KEY (node_id, tag)
);
CREATE TABLE tombstones (id TEXT PRIMARY KEY);
CREATE TABLE sources (
  token TEXT NOT NULL, node_id TEXT NOT NULL, host TEXT NOT NULL, port INTEGER NOT NULL,
  username TEXT NOT NULL, purpose TEXT NOT NULL,
  credential_id TEXT NOT NULL, version TEXT NOT NULL, host_key TEXT NOT NULL,
  PRIMARY KEY (token, node_id, host, port, username, purpose)
);
