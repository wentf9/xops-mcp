CREATE TABLE node_jumps (
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
  position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 30),
  jump_id TEXT NOT NULL REFERENCES nodes(id) DEFERRABLE INITIALLY DEFERRED,
  PRIMARY KEY (node_id, position), UNIQUE (node_id, jump_id)
);
INSERT INTO node_jumps SELECT id, 0, jump_id FROM nodes WHERE jump_id IS NOT NULL;
-- Preserve the old column for schema compatibility, but keep a single source
-- of truth for current references in the ordered relation.
UPDATE nodes SET jump_id = NULL;
