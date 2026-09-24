ALTER TABLE velis.async_tasks DROP CONSTRAINT async_tasks_status_check;

ALTER TABLE velis.async_tasks
    ADD COLUMN stage varchar(16) NOT NULL DEFAULT 'generation'
        CHECK (stage IN ('generation', 'embedding')),
    ADD COLUMN generation_profile_version varchar(96) NULL,
    ADD COLUMN embedding_profile_version varchar(96) NULL,
    ADD COLUMN generation_attempt integer NOT NULL DEFAULT 0 CHECK (generation_attempt >= 0),
    ADD COLUMN embedding_attempt integer NOT NULL DEFAULT 0 CHECK (embedding_attempt >= 0),
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN last_error_code varchar(64) NULL,
    ADD COLUMN last_error_message varchar(512) NULL,
    ADD COLUMN lease_owner varchar(128) NULL,
    ADD COLUMN lease_token uuid NULL,
    ADD COLUMN lease_expires_at timestamptz NULL,
    ADD COLUMN generation_completed_at timestamptz NULL,
    ADD COLUMN embedding_completed_at timestamptz NULL,
    ADD COLUMN completed_at timestamptz NULL,
    ADD CONSTRAINT async_tasks_status_check
        CHECK (status IN ('pending', 'running', 'retry_wait', 'succeeded', 'failed', 'canceled')),
    ADD CONSTRAINT async_tasks_lease_check
        CHECK ((status = 'running' AND lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
            OR (status <> 'running' AND lease_owner IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL)),
    ADD CONSTRAINT async_tasks_cancel_check
        CHECK ((status = 'canceled' AND canceled_at IS NOT NULL)
            OR (status <> 'canceled' AND canceled_at IS NULL)),
    ADD CONSTRAINT async_tasks_completion_check
        CHECK ((status = 'succeeded' AND completed_at IS NOT NULL)
            OR (status <> 'succeeded' AND completed_at IS NULL));

ALTER TABLE velis.async_tasks DROP CONSTRAINT async_tasks_check1;

DROP INDEX velis.async_tasks_pending_idx;
CREATE INDEX async_tasks_due_idx
    ON velis.async_tasks (next_attempt_at, updated_at, id)
    WHERE status IN ('pending', 'retry_wait');
CREATE INDEX async_tasks_expired_lease_idx
    ON velis.async_tasks (lease_expires_at, id)
    WHERE status = 'running';
CREATE INDEX async_tasks_stage_status_idx ON velis.async_tasks (stage, status, updated_at);

CREATE TABLE velis.ai_generation_results (
    id uuid PRIMARY KEY,
    article_id bigint NOT NULL,
    revision_id bigint NOT NULL,
    provider varchar(96) NOT NULL CHECK (btrim(provider) <> ''),
    model varchar(192) NOT NULL CHECK (btrim(model) <> ''),
    profile_version varchar(96) NOT NULL CHECK (btrim(profile_version) <> ''),
    workflow_version varchar(96) NOT NULL CHECK (btrim(workflow_version) <> ''),
    prompt_version varchar(96) NOT NULL CHECK (btrim(prompt_version) <> ''),
    generation_input_hash char(64) NOT NULL CHECK (generation_input_hash ~ '^[0-9a-f]{64}$'),
    input_truncated boolean NOT NULL,
    summary text NOT NULL CHECK (btrim(summary) <> '' AND char_length(summary) <= 4000),
    keywords text[] NOT NULL CHECK (cardinality(keywords) BETWEEN 1 AND 12),
    topics text[] NOT NULL CHECK (cardinality(topics) BETWEEN 1 AND 5),
    generated_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (article_id, revision_id)
        REFERENCES velis.article_versions(article_id, id) ON DELETE RESTRICT,
    UNIQUE (article_id, revision_id, provider, model, profile_version, workflow_version, prompt_version, generation_input_hash),
    UNIQUE (article_id, revision_id, id)
);

CREATE TABLE velis.ai_embedding_results (
    id uuid PRIMARY KEY,
    article_id bigint NOT NULL,
    revision_id bigint NOT NULL,
    generation_result_id uuid NOT NULL,
    provider varchar(96) NOT NULL CHECK (btrim(provider) <> ''),
    model varchar(192) NOT NULL CHECK (btrim(model) <> ''),
    profile_version varchar(96) NOT NULL CHECK (btrim(profile_version) <> ''),
    embedding_input_version varchar(96) NOT NULL CHECK (btrim(embedding_input_version) <> ''),
    embedding_input_hash char(64) NOT NULL CHECK (embedding_input_hash ~ '^[0-9a-f]{64}$'),
    dimensions integer NOT NULL CHECK (dimensions BETWEEN 1 AND 65536),
    vector real[] NOT NULL,
    generated_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (article_id, revision_id, generation_result_id)
        REFERENCES velis.ai_generation_results(article_id, revision_id, id) ON DELETE RESTRICT,
    CHECK (array_ndims(vector) = 1 AND cardinality(vector) = dimensions),
    CHECK (array_position(vector, 'NaN'::real) IS NULL
        AND array_position(vector, 'Infinity'::real) IS NULL
        AND array_position(vector, '-Infinity'::real) IS NULL),
    UNIQUE (article_id, revision_id, generation_result_id, provider, model, profile_version, embedding_input_version, embedding_input_hash, dimensions),
    UNIQUE (article_id, revision_id, id)
);

CREATE TABLE velis.ai_current_selections (
    article_id bigint PRIMARY KEY REFERENCES velis.articles(id) ON DELETE RESTRICT,
    revision_id bigint NOT NULL,
    generation_result_id uuid NULL,
    embedding_result_id uuid NULL,
    generation_profile_version varchar(96) NULL,
    embedding_profile_version varchar(96) NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (article_id, revision_id)
        REFERENCES velis.article_versions(article_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (article_id, revision_id, generation_result_id)
        REFERENCES velis.ai_generation_results(article_id, revision_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (article_id, revision_id, embedding_result_id)
        REFERENCES velis.ai_embedding_results(article_id, revision_id, id) ON DELETE RESTRICT,
    CHECK ((generation_result_id IS NULL) = (generation_profile_version IS NULL)),
    CHECK ((embedding_result_id IS NULL) = (embedding_profile_version IS NULL))
);

CREATE TABLE velis.ai_model_calls (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL REFERENCES velis.async_tasks(id) ON DELETE RESTRICT,
    task_generation bigint NOT NULL CHECK (task_generation > 0),
    stage varchar(16) NOT NULL CHECK (stage IN ('generation', 'embedding')),
    call_kind varchar(24) NOT NULL CHECK (call_kind IN ('generation_single', 'generation_map', 'generation_reduce', 'embedding')),
    attempt integer NOT NULL CHECK (attempt > 0),
    provider varchar(96) NOT NULL CHECK (btrim(provider) <> ''),
    model varchar(192) NOT NULL CHECK (btrim(model) <> ''),
    profile_version varchar(96) NOT NULL CHECK (btrim(profile_version) <> ''),
    workflow_version varchar(96) NULL,
    prompt_version varchar(96) NULL,
    embedding_input_version varchar(96) NULL,
    input_hash char(64) NOT NULL CHECK (input_hash ~ '^[0-9a-f]{64}$'),
    input_tokens integer NULL CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens integer NULL CHECK (output_tokens IS NULL OR output_tokens >= 0),
    total_tokens integer NULL CHECK (total_tokens IS NULL OR total_tokens >= 0),
    duration_ms bigint NOT NULL CHECK (duration_ms >= 0),
    status varchar(16) NOT NULL CHECK (status IN ('succeeded', 'failed', 'stale')),
    error_code varchar(64) NULL,
    error_message varchar(512) NULL,
    created_at timestamptz NOT NULL,
    CHECK ((status = 'succeeded' AND error_code IS NULL AND error_message IS NULL)
        OR status IN ('failed', 'stale'))
);

CREATE INDEX ai_generation_results_revision_idx
    ON velis.ai_generation_results (article_id, revision_id, generated_at DESC);
CREATE INDEX ai_embedding_results_revision_idx
    ON velis.ai_embedding_results (article_id, revision_id, generated_at DESC);
CREATE INDEX ai_model_calls_task_idx
    ON velis.ai_model_calls (task_id, task_generation, stage, attempt, created_at);
CREATE INDEX ai_model_calls_error_idx
    ON velis.ai_model_calls (error_code, created_at) WHERE status = 'failed';
