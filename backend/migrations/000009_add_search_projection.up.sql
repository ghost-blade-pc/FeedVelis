-- 搜索投影：每文章唯一的收敛槽位、按物理索引的 delivery、索引与重建状态。
-- OpenSearch 只是可丢弃的派生投影；这里持久化的始终是「推进到 PostgreSQL 当前事实」的意图。

CREATE SEQUENCE velis.search_projection_change_seq;

-- 索引服务状态是单行表：当前读写别名、当前服务索引、回滚窗口内的前一索引。
-- delivery 只能指向这里的当前索引、回滚索引或活动重建候选索引。
CREATE TABLE velis.search_index_state (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    read_alias varchar(64) NOT NULL CHECK (read_alias ~ '^[a-z][a-z0-9._-]{1,63}$'),
    write_alias varchar(64) NOT NULL CHECK (write_alias ~ '^[a-z][a-z0-9._-]{1,63}$'),
    current_index varchar(255) NOT NULL CHECK (current_index ~ '^[a-z][a-z0-9._-]{1,254}$'),
    rollback_index varchar(255) NULL CHECK (rollback_index IS NULL OR rollback_index ~ '^[a-z][a-z0-9._-]{1,254}$'),
    rollback_deadline timestamptz NULL,
    schema_version integer NOT NULL CHECK (schema_version > 0),
    schema_identity varchar(256) NOT NULL CHECK (btrim(schema_identity) <> ''),
    updated_at timestamptz NOT NULL,
    CHECK (read_alias <> write_alias),
    CHECK (rollback_index IS NULL OR rollback_index <> current_index),
    CHECK ((rollback_index IS NULL) = (rollback_deadline IS NULL))
);

CREATE TABLE velis.search_projection_jobs (
    article_id bigint PRIMARY KEY REFERENCES velis.articles(id) ON DELETE RESTRICT,
    action varchar(16) NOT NULL CHECK (action IN ('upsert', 'tombstone')),
    generation bigint NOT NULL CHECK (generation > 0),
    change_seq bigint NOT NULL CHECK (change_seq > 0),
    article_lock_version bigint NOT NULL CHECK (article_lock_version > 0),
    revision_id bigint NOT NULL,
    generation_result_id uuid NULL,
    embedding_result_id uuid NULL,
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'running', 'retry_wait', 'succeeded', 'failed')),
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_error_code varchar(64) NULL CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    last_error_message varchar(512) NULL,
    lease_owner varchar(128) NULL,
    lease_token uuid NULL,
    lease_expires_at timestamptz NULL,
    target_changed_at timestamptz NOT NULL,
    completed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (article_id, revision_id)
        REFERENCES velis.article_versions(article_id, id) ON DELETE RESTRICT,
    -- tombstone 只携带身份与版本，不保留任何 AI 结果引用。
    CHECK (action = 'upsert' OR (generation_result_id IS NULL AND embedding_result_id IS NULL)),
    CHECK ((status = 'running' AND lease_owner IS NOT NULL AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status <> 'running' AND lease_owner IS NULL AND lease_token IS NULL AND lease_expires_at IS NULL)),
    CHECK ((status = 'succeeded' AND completed_at IS NOT NULL)
        OR (status <> 'succeeded' AND completed_at IS NULL))
);

-- 认领只扫描到期槽位。
CREATE INDEX search_projection_jobs_due_idx
    ON velis.search_projection_jobs (next_attempt_at, article_id)
    WHERE status IN ('pending', 'retry_wait');
CREATE INDEX search_projection_jobs_expired_lease_idx
    ON velis.search_projection_jobs (lease_expires_at, article_id)
    WHERE status = 'running';
CREATE INDEX search_projection_jobs_status_idx
    ON velis.search_projection_jobs (status, updated_at, article_id);
-- 重建增量追赶按全局 change sequence 扫描。
CREATE INDEX search_projection_jobs_change_seq_idx
    ON velis.search_projection_jobs (change_seq, article_id);

CREATE TABLE velis.search_index_rebuilds (
    id uuid PRIMARY KEY,
    candidate_index varchar(255) NOT NULL CHECK (candidate_index ~ '^[a-z][a-z0-9._-]{1,254}$'),
    target_schema_version integer NOT NULL CHECK (target_schema_version > 0),
    schema_identity varchar(256) NOT NULL CHECK (btrim(schema_identity) <> ''),
    phase varchar(16) NOT NULL CHECK (phase IN
        ('snapshot', 'catchup', 'validate', 'validated', 'cutover', 'serving', 'completed', 'abandoned', 'failed')),
    -- start 时用 nextval 取得的水位：change_seq 大于它的目标变化都属于增量追赶范围。
    start_change_seq bigint NOT NULL CHECK (start_change_seq >= 0),
    snapshot_watermark bigint NOT NULL DEFAULT 0 CHECK (snapshot_watermark >= 0),
    snapshot_documents bigint NOT NULL DEFAULT 0 CHECK (snapshot_documents >= 0),
    catchup_watermark bigint NOT NULL DEFAULT 0 CHECK (catchup_watermark >= 0),
    validation_report jsonb NULL,
    validated_at timestamptz NULL,
    cutover_at timestamptz NULL,
    rollback_deadline timestamptz NULL,
    last_error varchar(512) NULL,
    started_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (phase NOT IN ('snapshot', 'catchup', 'validate', 'validated')
        OR (cutover_at IS NULL AND rollback_deadline IS NULL)),
    CHECK (rollback_deadline IS NULL OR cutover_at IS NOT NULL)
);

-- 任一时刻只允许一个活动重建：快照、追赶、校验、切换阶段互斥。
CREATE UNIQUE INDEX search_index_rebuilds_active_idx
    ON velis.search_index_rebuilds ((true))
    WHERE phase IN ('snapshot', 'catchup', 'validate', 'validated', 'cutover');
CREATE INDEX search_index_rebuilds_candidate_idx
    ON velis.search_index_rebuilds (candidate_index, started_at DESC);

CREATE TABLE velis.search_projection_deliveries (
    article_id bigint NOT NULL REFERENCES velis.search_projection_jobs(article_id) ON DELETE CASCADE,
    physical_index varchar(255) NOT NULL CHECK (physical_index ~ '^[a-z][a-z0-9._-]{1,254}$'),
    required_generation bigint NOT NULL CHECK (required_generation > 0),
    status varchar(16) NOT NULL CHECK (status IN ('pending', 'running', 'retry_wait', 'succeeded', 'failed')),
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_result varchar(24) NULL CHECK (last_result IS NULL OR last_result IN
        ('created', 'updated', 'tombstoned', 'noop', 'retryable', 'permanent', 'unknown')),
    last_error_code varchar(64) NULL CHECK (last_error_code IS NULL OR last_error_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    last_error_message varchar(512) NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (article_id, physical_index)
);
CREATE INDEX search_projection_deliveries_lagging_idx
    ON velis.search_projection_deliveries (next_attempt_at, article_id, physical_index)
    WHERE status IN ('pending', 'retry_wait');
CREATE INDEX search_projection_deliveries_failed_idx
    ON velis.search_projection_deliveries (updated_at, article_id, physical_index)
    WHERE status = 'failed';
CREATE INDEX search_projection_deliveries_index_idx
    ON velis.search_projection_deliveries (physical_index, status);

-- delivery 只能指向当前服务索引、回滚窗口内的前一索引或活动重建候选索引。
-- 用触发器而不是 CHECK：判定需要读取索引与重建状态，CHECK 不允许子查询。
CREATE FUNCTION velis.search_projection_delivery_index_allowed(target varchar) RETURNS boolean
    LANGUAGE sql STABLE AS $$
    SELECT EXISTS (
        SELECT 1 FROM velis.search_index_state s
        WHERE s.current_index = target OR s.rollback_index = target
    ) OR EXISTS (
        SELECT 1 FROM velis.search_index_rebuilds r
        WHERE r.candidate_index = target
          AND r.phase IN ('snapshot', 'catchup', 'validate', 'validated', 'cutover', 'serving')
    );
$$;

CREATE FUNCTION velis.search_projection_delivery_index_guard() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    IF NOT velis.search_projection_delivery_index_allowed(NEW.physical_index) THEN
        RAISE EXCEPTION '拒绝为未服务索引 % 写入投影 delivery', NEW.physical_index
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER search_projection_deliveries_index_guard
    BEFORE INSERT OR UPDATE OF physical_index ON velis.search_projection_deliveries
    FOR EACH ROW EXECUTE FUNCTION velis.search_projection_delivery_index_guard();
