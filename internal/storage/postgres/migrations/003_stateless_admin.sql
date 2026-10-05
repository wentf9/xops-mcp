-- Legacy opaque sessions cannot authenticate as JWTs. Preserve the administrator.
DROP TABLE IF EXISTS admin_sessions;
