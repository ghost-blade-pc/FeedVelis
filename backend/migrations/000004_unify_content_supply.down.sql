DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.articles WHERE origin_type <> 'rss') THEN
        RAISE EXCEPTION '无法降级：存在用户投稿';
    END IF;
    IF EXISTS (SELECT 1 FROM velis.articles WHERE status NOT IN ('published', 'offline') OR offline_reason = 'author') THEN
        RAISE EXCEPTION '无法降级：存在旧模型无法表达的文章状态';
    END IF;
    IF EXISTS (SELECT 1 FROM velis.article_assets) THEN
        RAISE EXCEPTION '无法降级：存在文章资产';
    END IF;
    IF EXISTS (SELECT 1 FROM velis.article_versions WHERE revision_no <> 1) THEN
        RAISE EXCEPTION '无法降级：存在多版本文章';
    END IF;
    IF EXISTS (SELECT 1 FROM velis.idempotency_operations) THEN
        RAISE EXCEPTION '无法降级：存在 I2 幂等操作';
    END IF;
    IF EXISTS (SELECT 1 FROM velis.source_fetch_runs) THEN
        RAISE EXCEPTION '无法降级：存在抓取运行历史';
    END IF;
END $$;

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

INSERT INTO velis.article_contents (
    article_id, raw_description, raw_content, raw_description_truncated,
    raw_content_truncated, sanitized_html, plain_text, sanitizer_version, created_at, updated_at
)
SELECT a.id, v.raw_description, v.raw_content, v.raw_description_truncated,
       v.raw_content_truncated, v.sanitized_html, v.plain_text, v.sanitizer_version,
       v.created_at, a.updated_at
FROM velis.articles a
JOIN velis.article_versions v ON v.id = a.current_revision_id;

ALTER TABLE velis.articles DROP CONSTRAINT articles_current_revision_fk;
ALTER TABLE velis.articles DROP CONSTRAINT articles_visibility_state_check;
ALTER TABLE velis.articles DROP CONSTRAINT articles_offline_actor_check;
ALTER TABLE velis.articles DROP CONSTRAINT articles_origin_identity_check;
ALTER TABLE velis.articles DROP CONSTRAINT articles_status_check;

ALTER TABLE velis.articles
    ADD COLUMN title varchar(500),
    ADD COLUMN excerpt text,
    ADD COLUMN language varchar(16),
    ADD COLUMN content_hash char(64);

UPDATE velis.articles a
SET title = v.title,
    author_name = v.source_author_name,
    excerpt = v.excerpt,
    language = v.language,
    content_hash = v.content_hash,
    status = CASE a.status WHEN 'offline' THEN 'hidden' ELSE 'published' END
FROM velis.article_versions v
WHERE v.id = a.current_revision_id;

ALTER TABLE velis.articles
    ALTER COLUMN source_id SET NOT NULL,
    ALTER COLUMN dedupe_key SET NOT NULL,
    ALTER COLUMN canonical_url SET NOT NULL,
    ALTER COLUMN title SET NOT NULL,
    ALTER COLUMN excerpt SET NOT NULL,
    ALTER COLUMN language SET NOT NULL,
    ALTER COLUMN content_hash SET NOT NULL,
    ADD COLUMN sort_at timestamptz GENERATED ALWAYS AS (COALESCE(source_published_at, discovered_at)) STORED,
    ADD CONSTRAINT articles_status_check CHECK (status IN ('published', 'hidden'));

DROP INDEX velis.articles_latest_idx;
DROP INDEX velis.articles_author_updated_idx;
CREATE INDEX articles_list_idx ON velis.articles (sort_at DESC, id DESC) WHERE status = 'published';

DROP TABLE velis.article_asset_references;
DROP TABLE velis.article_assets;
DROP TABLE velis.idempotency_operations;
DROP TABLE velis.source_fetch_runs;
DROP TABLE velis.article_versions;

ALTER TABLE velis.articles
    DROP COLUMN origin_type,
    DROP COLUMN author_user_id,
    DROP COLUMN published_at,
    DROP COLUMN current_revision_id,
    DROP COLUMN lock_version,
    DROP COLUMN offline_reason,
    DROP COLUMN offline_by_user_id,
    DROP COLUMN offline_at,
    DROP COLUMN deleted_at;

ALTER TABLE velis.sources
    DROP COLUMN fetch_interval_seconds,
    DROP COLUMN lock_version,
    DROP COLUMN lease_generation;
