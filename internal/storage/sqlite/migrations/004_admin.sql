CREATE TABLE admin (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  username TEXT NOT NULL, password_hash BLOB NOT NULL, version TEXT NOT NULL
);
CREATE TABLE admin_sessions (
  digest BLOB PRIMARY KEY CHECK (length(digest) = 32),
  admin_version TEXT NOT NULL, expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_expiry ON admin_sessions(expires_at);
