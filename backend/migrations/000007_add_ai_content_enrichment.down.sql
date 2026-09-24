DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.ai_model_calls LIMIT 1)
        OR EXISTS (SELECT 1 FROM velis.ai_current_selections LIMIT 1)
        OR EXISTS (SELECT 1 FROM velis.ai_embedding_results LIMIT 1)
        OR EXISTS (SELECT 1 FROM velis.ai_generation_results LIMIT 1)
        OR EXISTS (
            SELECT 1 FROM velis.async_tasks
            WHERE status NOT IN ('pending', 'canceled')
                OR stage <> 'generation'
                OR generation_profile_version IS NOT NULL
                OR embedding_profile_version IS NOT NULL
                OR generation_attempt <> 0
                OR embedding_attempt <> 0
                OR last_error_code IS NOT NULL
                OR last_error_message IS NOT NULL
                OR lease_owner IS NOT NULL
                OR lease_token IS NOT NULL
                OR lease_expires_at IS NOT NULL
                OR generation_completed_at IS NOT NULL
                OR embedding_completed_at IS NOT NULL
                OR completed_at IS NOT NULL
        ) THEN
        RAISE EXCEPTION '拒绝回滚 AI 内容增强迁移：存在增强结果、调用记录或不兼容任务状态';
    END IF;
END $$;

DROP TABLE velis.ai_model_calls;
DROP TABLE velis.ai_current_selections;
DROP TABLE velis.ai_embedding_results;
DROP TABLE velis.ai_generation_results;

DROP INDEX velis.async_tasks_stage_status_idx;
DROP INDEX velis.async_tasks_expired_lease_idx;
DROP INDEX velis.async_tasks_due_idx;

ALTER TABLE velis.async_tasks
    DROP CONSTRAINT async_tasks_completion_check,
    DROP CONSTRAINT async_tasks_cancel_check,
    DROP CONSTRAINT async_tasks_lease_check,
    DROP CONSTRAINT async_tasks_status_check,
    DROP COLUMN completed_at,
    DROP COLUMN embedding_completed_at,
    DROP COLUMN generation_completed_at,
    DROP COLUMN lease_expires_at,
    DROP COLUMN lease_token,
    DROP COLUMN lease_owner,
    DROP COLUMN last_error_message,
    DROP COLUMN last_error_code,
    DROP COLUMN next_attempt_at,
    DROP COLUMN embedding_attempt,
    DROP COLUMN generation_attempt,
    DROP COLUMN embedding_profile_version,
    DROP COLUMN generation_profile_version,
    DROP COLUMN stage,
    ADD CONSTRAINT async_tasks_status_check CHECK (status IN ('pending', 'canceled')),
    ADD CONSTRAINT async_tasks_check1 CHECK ((status = 'pending' AND canceled_at IS NULL)
        OR (status = 'canceled' AND canceled_at IS NOT NULL));

CREATE INDEX async_tasks_pending_idx ON velis.async_tasks (updated_at, id) WHERE status = 'pending';
