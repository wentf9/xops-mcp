ALTER TABLE mcp_tokens ADD COLUMN node_scope TEXT NOT NULL DEFAULT 'all' CHECK(node_scope IN ('all', 'selected'));
ALTER TABLE mcp_tokens ADD COLUMN node_ids JSONB NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(node_ids) = 'array' AND (node_scope = 'selected' OR jsonb_array_length(node_ids) = 0));
