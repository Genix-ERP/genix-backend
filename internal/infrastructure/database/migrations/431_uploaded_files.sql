-- 431_uploaded_files.sql
--
-- Store uploaded files (logos, favicons, product images, work-order attachments)
-- directly in Postgres instead of the local filesystem. This keeps deployments
-- stateless and stops uploads from being accidentally committed to the repo
-- under storage/uploads/.
--
-- Files are served publicly by their unguessable 128-bit id via
-- GET /api/v1/files/:id; the id therefore acts as a capability token.

CREATE TABLE IF NOT EXISTS uploaded_files (
    id         TEXT PRIMARY KEY,
    filename   TEXT        NOT NULL DEFAULT '',
    mime_type  TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size       BIGINT      NOT NULL DEFAULT 0,
    content    BYTEA       NOT NULL,
    tenant_id  UUID,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_uploaded_files_tenant ON uploaded_files (tenant_id);
