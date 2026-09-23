CREATE TABLE velis.outbox_events (
    event_id uuid PRIMARY KEY,
    event_type varchar(96) NOT NULL CHECK (event_type IN (
        'article.published.v1', 'article.revised.v1', 'article.offlined.v1', 'article.deleted.v1'
    )),
    aggregate_type varchar(32) NOT NULL CHECK (aggregate_type = 'article'),
    aggregate_id text NOT NULL CHECK (aggregate_id ~ '^[1-9][0-9]*$'),
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    envelope jsonb NOT NULL CHECK (jsonb_typeof(envelope) = 'object'),
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_attempt_at timestamptz NULL,
    published_at timestamptz NULL,
    publish_attempts integer NOT NULL DEFAULT 0 CHECK (publish_attempts >= 0),
    last_error_code varchar(64) NULL,
    last_error_message varchar(512) NULL,
    lease_owner varchar(128) NULL,
    lease_token uuid NULL,
    lease_expires_at timestamptz NULL,
    CHECK ((lease_owner IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL)
        OR (lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK ((envelope ->> 'event_id') = event_id::text),
    CHECK ((envelope ->> 'event_type') = event_type),
    CHECK ((envelope #>> '{aggregate,type}') = aggregate_type),
    CHECK ((envelope #>> '{aggregate,id}') = aggregate_id),
    CHECK ((envelope #>> '{aggregate,version}')::bigint = aggregate_version),
    UNIQUE (event_type, aggregate_type, aggregate_id, aggregate_version)
);

CREATE INDEX outbox_events_pending_idx
    ON velis.outbox_events (next_attempt_at, occurred_at, event_id)
    WHERE published_at IS NULL;
CREATE INDEX outbox_events_published_cleanup_idx
    ON velis.outbox_events (published_at, event_id)
    WHERE published_at IS NOT NULL;

CREATE TABLE velis.consumed_events (
    consumer_name varchar(96) NOT NULL,
    event_id uuid NOT NULL,
    event_type varchar(96) NOT NULL,
    aggregate_type varchar(32) NOT NULL,
    aggregate_id text NOT NULL,
    aggregate_version bigint NOT NULL CHECK (aggregate_version > 0),
    result varchar(16) NOT NULL CHECK (result IN ('applied', 'noop')),
    processed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (consumer_name, event_id)
);
CREATE INDEX consumed_events_aggregate_idx
    ON velis.consumed_events (aggregate_type, aggregate_id, aggregate_version DESC);

CREATE TABLE velis.async_tasks (
    id uuid PRIMARY KEY,
    task_type varchar(64) NOT NULL CHECK (task_type = 'article.enrichment'),
    aggregate_type varchar(32) NOT NULL CHECK (aggregate_type = 'article'),
    aggregate_id text NOT NULL CHECK (aggregate_id ~ '^[1-9][0-9]*$'),
    article_id bigint NOT NULL REFERENCES velis.articles(id) ON DELETE RESTRICT,
    revision_id bigint NOT NULL,
    revision_no integer NOT NULL CHECK (revision_no > 0),
    content_hash char(64) NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'canceled')),
    generation bigint NOT NULL CHECK (generation > 0),
    observed_aggregate_version bigint NOT NULL CHECK (observed_aggregate_version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    canceled_at timestamptz NULL,
    UNIQUE (task_type, aggregate_type, aggregate_id),
    FOREIGN KEY (article_id, revision_id)
        REFERENCES velis.article_versions(article_id, id) ON DELETE RESTRICT,
    CHECK (aggregate_id = article_id::text),
    CHECK ((status = 'pending' AND canceled_at IS NULL)
        OR (status = 'canceled' AND canceled_at IS NOT NULL)),
    CHECK (updated_at >= created_at)
);
CREATE INDEX async_tasks_pending_idx ON velis.async_tasks (updated_at, id) WHERE status = 'pending';
