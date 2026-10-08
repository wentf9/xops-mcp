ALTER TABLE mcp_tokens ADD COLUMN node_scope TEXT NOT NULL DEFAULT 'all' CHECK(node_scope IN ('all', 'selected'));
ALTER TABLE mcp_tokens ADD COLUMN node_ids TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(node_ids) AND json_type(node_ids) = 'array' AND (node_scope = 'selected' OR json_array_length(node_ids) = 0));
