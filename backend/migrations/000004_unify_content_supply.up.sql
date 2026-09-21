ALTER TABLE velis.sources
    ADD COLUMN fetch_interval_seconds integer NOT NULL DEFAULT 1800
        CHECK (fetch_interval_seconds BETWEEN 300 AND 86400),
    ADD COLUMN lock_version bigint NOT NULL DEFAULT 1 CHECK (lock_version > 0),
    ADD COLUMN lease_generation bigint NOT NULL DEFAULT 0 CHECK (lease_generation >= 0);

ALTER TABLE velis.articles DROP CONSTRAINT articles_status_check;
ALTER TABLE velis.articles DROP COLUMN sort_at;
ALTER TABLE velis.articles
    ALTER COLUMN source_id DROP NOT NULL,
    ALTER COLUMN dedupe_key DROP NOT NULL,
    ALTER COLUMN canonical_url DROP NOT NULL,
    ADD COLUMN origin_type varchar(8) NOT NULL DEFAULT 'rss',
    ADD COLUMN author_user_id uuid NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    ADD COLUMN published_at timestamptz NULL,
    ADD COLUMN current_revision_id bigint NULL,
    ADD COLUMN lock_version bigint NOT NULL DEFAULT 1 CHECK (lock_version > 0),
    ADD COLUMN offline_reason varchar(16) NULL CHECK (offline_reason IN ('author', 'admin')),
    ADD COLUMN offline_by_user_id uuid NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    ADD COLUMN offline_at timestamptz NULL,
    ADD COLUMN deleted_at timestamptz NULL;

UPDATE velis.articles
SET status = CASE status WHEN 'hidden' THEN 'offline' ELSE status END,
    published_at = discovered_at,
    offline_reason = CASE status WHEN 'hidden' THEN 'admin' ELSE NULL END,
    offline_at = CASE status WHEN 'hidden' THEN updated_at ELSE NULL END;

ALTER TABLE velis.articles
    ADD CONSTRAINT articles_status_check CHECK (status IN ('draft', 'published', 'offline', 'deleted')),
    ADD CONSTRAINT articles_origin_identity_check CHECK (
        (origin_type = 'rss' AND source_id IS NOT NULL AND dedupe_key IS NOT NULL
            AND canonical_url IS NOT NULL AND author_user_id IS NULL)
        OR
        (origin_type = 'user' AND source_id IS NULL AND dedupe_key IS NULL
            AND source_item_id IS NULL AND canonical_url IS NULL AND author_user_id IS NOT NULL)
    ),
    ADD CONSTRAINT articles_visibility_state_check CHECK (
        (status = 'draft' AND published_at IS NULL AND offline_reason IS NULL AND offline_at IS NULL AND deleted_at IS NULL)
        OR (status = 'published' AND published_at IS NOT NULL AND offline_reason IS NULL AND offline_at IS NULL AND deleted_at IS NULL)
        OR (status = 'offline' AND published_at IS NOT NULL AND offline_reason IS NOT NULL AND offline_at IS NOT NULL AND deleted_at IS NULL)
        OR (status = 'deleted' AND deleted_at IS NOT NULL)
    ),
    ADD CONSTRAINT articles_offline_actor_check CHECK (
        offline_by_user_id IS NULL OR offline_reason = 'admin'
    );

CREATE TABLE velis.article_versions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    article_id bigint NOT NULL REFERENCES velis.articles(id) ON DELETE CASCADE,
    revision_no integer NOT NULL CHECK (revision_no > 0),
    title varchar(500) NOT NULL,
    source_author_name varchar(300) NULL,
    user_markdown text NULL CHECK (user_markdown IS NULL OR octet_length(user_markdown) <= 262144),
    raw_description text NULL CHECK (raw_description IS NULL OR octet_length(raw_description) <= 262144),
    raw_content text NULL CHECK (raw_content IS NULL OR octet_length(raw_content) <= 1048576),
    raw_description_truncated boolean NOT NULL DEFAULT false,
    raw_content_truncated boolean NOT NULL DEFAULT false,
    sanitized_html text NULL,
    plain_text text NOT NULL,
    excerpt text NOT NULL,
    language varchar(16) NOT NULL,
    content_hash char(64) NOT NULL,
    sanitizer_version integer NOT NULL CHECK (sanitizer_version > 0),
    created_by_user_id uuid NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    UNIQUE (article_id, revision_no),
    UNIQUE (article_id, id)
);

INSERT INTO velis.article_versions (
    article_id, revision_no, title, source_author_name, raw_description, raw_content,
    raw_description_truncated, raw_content_truncated, sanitized_html,
    plain_text, excerpt, language, content_hash, sanitizer_version, created_at
)
SELECT a.id, 1, a.title, a.author_name, c.raw_description, c.raw_content,
       c.raw_description_truncated, c.raw_content_truncated, c.sanitized_html,
       c.plain_text, a.excerpt, a.language, a.content_hash, c.sanitizer_version, c.created_at
FROM velis.articles a
JOIN velis.article_contents c ON c.article_id = a.id
ORDER BY a.id;

UPDATE velis.articles a
SET current_revision_id = v.id
FROM velis.article_versions v
WHERE v.article_id = a.id AND v.revision_no = 1;

ALTER TABLE velis.articles
    ALTER COLUMN current_revision_id SET NOT NULL,
    ADD CONSTRAINT articles_current_revision_fk
        FOREIGN KEY (id, current_revision_id)
        REFERENCES velis.article_versions(article_id, id)
        DEFERRABLE INITIALLY DEFERRED;

DROP TABLE velis.article_contents;
ALTER TABLE velis.articles
    DROP COLUMN title,
    DROP COLUMN excerpt,
    DROP COLUMN language,
    DROP COLUMN content_hash;

CREATE INDEX articles_latest_idx ON velis.articles (published_at DESC, id DESC)
    WHERE status = 'published';
CREATE INDEX articles_author_updated_idx ON velis.articles (author_user_id, updated_at DESC, id DESC)
    WHERE origin_type = 'user';

CREATE TABLE velis.article_assets (
    id uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    bound_article_id bigint NULL REFERENCES velis.articles(id) ON DELETE RESTRICT,
    object_key text NOT NULL UNIQUE CHECK (octet_length(object_key) BETWEEN 1 AND 1024),
    status varchar(24) NOT NULL CHECK (status IN ('pending', 'ready', 'delete_pending', 'deleted')),
    content_type varchar(32) NULL CHECK (content_type IS NULL OR content_type IN ('image/jpeg', 'image/png', 'image/webp')),
    size_bytes bigint NULL CHECK (size_bytes IS NULL OR size_bytes BETWEEN 1 AND 10485760),
    width integer NULL CHECK (width IS NULL OR width BETWEEN 1 AND 8192),
    height integer NULL CHECK (height IS NULL OR height BETWEEN 1 AND 8192),
    checksum text NULL CHECK (checksum IS NULL OR octet_length(checksum) <= 256),
    quota_counted_at timestamptz NULL,
    confirmed_at timestamptz NULL,
    delete_requested_at timestamptz NULL,
    deleted_at timestamptz NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (width IS NULL OR height IS NULL OR width::bigint * height::bigint <= 40000000),
    CHECK ((status = 'pending' AND content_type IS NULL AND size_bytes IS NULL AND confirmed_at IS NULL)
        OR (status <> 'pending' AND content_type IS NOT NULL AND size_bytes IS NOT NULL AND confirmed_at IS NOT NULL))
);

CREATE INDEX article_assets_owner_status_idx ON velis.article_assets (owner_user_id, status, created_at);
CREATE INDEX article_assets_pending_cleanup_idx ON velis.article_assets (created_at, id) WHERE status = 'pending';
CREATE INDEX article_assets_ready_cleanup_idx ON velis.article_assets (confirmed_at, id)
    WHERE status = 'ready' AND bound_article_id IS NULL;
CREATE INDEX article_assets_delete_cleanup_idx ON velis.article_assets (delete_requested_at, id)
    WHERE status = 'delete_pending';

CREATE TABLE velis.article_asset_references (
    article_version_id bigint NOT NULL REFERENCES velis.article_versions(id) ON DELETE CASCADE,
    asset_id uuid NOT NULL REFERENCES velis.article_assets(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (article_version_id, asset_id)
);
CREATE INDEX article_asset_references_asset_idx ON velis.article_asset_references (asset_id, article_version_id);

CREATE TABLE velis.idempotency_operations (
    actor_user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    operation varchar(64) NOT NULL,
    idempotency_key uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'succeeded')),
    result_version integer NULL CHECK (result_version IS NULL OR result_version > 0),
    result_payload jsonb NULL,
    resource_type varchar(32) NULL,
    resource_id text NULL CHECK (resource_id IS NULL OR octet_length(resource_id) <= 128),
    created_at timestamptz NOT NULL,
    completed_at timestamptz NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (actor_user_id, operation, idempotency_key),
    CHECK (expires_at > created_at),
    CHECK ((status = 'pending' AND result_version IS NULL AND result_payload IS NULL AND completed_at IS NULL)
        OR (status = 'succeeded' AND result_version IS NOT NULL AND result_payload IS NOT NULL AND completed_at IS NOT NULL))
);
CREATE INDEX idempotency_operations_expiry_idx ON velis.idempotency_operations (expires_at);

CREATE TABLE velis.source_fetch_runs (
    id uuid PRIMARY KEY,
    source_id bigint NOT NULL REFERENCES velis.sources(id) ON DELETE RESTRICT,
    trigger varchar(16) NOT NULL CHECK (trigger IN ('scheduled', 'manual')),
    actor_user_id uuid NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    status varchar(16) NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'aborted')),
    lease_generation bigint NOT NULL CHECK (lease_generation > 0),
    not_modified boolean NOT NULL DEFAULT false,
    inserted_count integer NOT NULL DEFAULT 0 CHECK (inserted_count >= 0),
    updated_count integer NOT NULL DEFAULT 0 CHECK (updated_count >= 0),
    unchanged_count integer NOT NULL DEFAULT 0 CHECK (unchanged_count >= 0),
    skipped_count integer NOT NULL DEFAULT 0 CHECK (skipped_count >= 0),
    error_code varchar(64) NULL,
    started_at timestamptz NOT NULL,
    completed_at timestamptz NULL,
    created_at timestamptz NOT NULL,
    CHECK ((trigger = 'manual' AND actor_user_id IS NOT NULL) OR trigger = 'scheduled'),
    CHECK ((status = 'running' AND completed_at IS NULL) OR (status <> 'running' AND completed_at IS NOT NULL)),
    CHECK (status = 'failed' OR error_code IS NULL)
);
CREATE INDEX source_fetch_runs_history_idx ON velis.source_fetch_runs (source_id, started_at DESC, id DESC);
CREATE UNIQUE INDEX source_fetch_runs_running_idx ON velis.source_fetch_runs (source_id) WHERE status = 'running';

SELECT setval(
    pg_get_serial_sequence('velis.articles', 'id'),
    COALESCE((SELECT max(id) FROM velis.articles), 1),
    EXISTS (SELECT 1 FROM velis.articles)
);
