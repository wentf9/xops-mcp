CREATE TABLE audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, occurred_at TEXT NOT NULL,
  operation_id TEXT NOT NULL, tool TEXT NOT NULL, outcome TEXT NOT NULL, event BLOB NOT NULL
);
CREATE INDEX audit_operation ON audit_events(operation_id, id);
