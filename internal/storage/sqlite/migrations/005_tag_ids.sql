CREATE TABLE tags_v5 (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE
);
INSERT INTO tags_v5(id,name) SELECT lower(hex(randomblob(16))),name FROM tags;
CREATE TABLE node_tags_v5 (
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  tag_id TEXT NOT NULL REFERENCES tags_v5(id), PRIMARY KEY (node_id, tag_id)
);
INSERT INTO node_tags_v5(node_id,tag_id)
  SELECT nt.node_id,t.id FROM node_tags nt JOIN tags_v5 t ON t.name=nt.tag;
DROP TABLE node_tags;
DROP TABLE tags;
ALTER TABLE tags_v5 RENAME TO tags;
ALTER TABLE node_tags_v5 RENAME TO node_tags;
