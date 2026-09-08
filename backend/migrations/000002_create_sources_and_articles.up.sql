CREATE TABLE velis.sources (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    feed_url text NOT NULL CHECK (octet_length(feed_url) BETWEEN 1 AND 4096 AND feed_url ~* '^https?://'),
    normalized_feed_url text NOT NULL UNIQUE CHECK (octet_length(normalized_feed_url) BETWEEN 1 AND 4096 AND normalized_feed_url ~ '^https?://'),
    site_url text NULL CHECK (site_url IS NULL OR (octet_length(site_url) <= 4096 AND site_url ~ '^https?://')),
    title varchar(500) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('active', 'paused', 'degraded')),
    etag text NULL CHECK (etag IS NULL OR octet_length(etag) <= 1024),
    last_modified text NULL CHECK (last_modified IS NULL OR octet_length(last_modified) <= 1024),
    next_fetch_at timestamptz NOT NULL,
    last_checked_at timestamptz NULL,
    last_success_at timestamptz NULL,
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    last_error_code varchar(64) NULL,
    lease_owner varchar(128) NULL,
    lease_expires_at timestamptz NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK ((lease_owner IS NULL) = (lease_expires_at IS NULL))
);

CREATE INDEX sources_due_idx ON velis.sources (next_fetch_at, id) WHERE status = 'active';

CREATE TABLE velis.articles (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id bigint NOT NULL REFERENCES velis.sources(id) ON DELETE RESTRICT,
    dedupe_key char(64) NOT NULL,
    source_item_id text NULL CHECK (source_item_id IS NULL OR octet_length(source_item_id) <= 2048),
    canonical_url text NOT NULL CHECK (octet_length(canonical_url) BETWEEN 1 AND 4096 AND canonical_url ~ '^https?://'),
    title varchar(500) NOT NULL,
    author_name varchar(300) NULL,
    excerpt text NOT NULL,
    language varchar(16) NOT NULL,
    source_published_at timestamptz NULL,
    discovered_at timestamptz NOT NULL,
    sort_at timestamptz GENERATED ALWAYS AS (COALESCE(source_published_at, discovered_at)) STORED,
    source_updated_at timestamptz NULL,
    content_hash char(64) NOT NULL,
    status varchar(16) NOT NULL CHECK (status IN ('published', 'hidden')),
    last_seen_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (source_id, dedupe_key)
);

CREATE INDEX articles_list_idx ON velis.articles (sort_at DESC, id DESC) WHERE status = 'published';
CREATE INDEX articles_source_seen_idx ON velis.articles (source_id, last_seen_at DESC);

CREATE TABLE velis.article_contents (
    article_id bigint PRIMARY KEY REFERENCES velis.articles(id) ON DELETE CASCADE,
    raw_description text NULL,
    raw_content text NULL,
    raw_description_truncated boolean NOT NULL DEFAULT false,
    raw_content_truncated boolean NOT NULL DEFAULT false,
    sanitized_html text NULL,
    plain_text text NOT NULL,
    sanitizer_version integer NOT NULL CHECK (sanitizer_version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (raw_description IS NULL OR octet_length(raw_description) <= 262144),
    CHECK (raw_content IS NULL OR octet_length(raw_content) <= 1048576)
);
