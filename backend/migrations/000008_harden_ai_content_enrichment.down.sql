DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM velis.async_tasks
        WHERE generation_repair_used_at IS NOT NULL
    ) OR EXISTS (
        SELECT 1 FROM velis.ai_model_calls
        WHERE call_kind = 'generation_repair'
    ) THEN
        RAISE EXCEPTION '拒绝回滚 AI 内容增强加固迁移：存在已使用的格式纠正额度或纠正调用记录';
    END IF;
END $$;

ALTER TABLE velis.ai_model_calls
    DROP CONSTRAINT ai_model_calls_error_reason_check,
    DROP CONSTRAINT ai_model_calls_structured_output_mode_check,
    DROP CONSTRAINT ai_model_calls_call_kind_check,
    DROP COLUMN error_reason,
    DROP COLUMN structured_output_mode,
    ADD CONSTRAINT ai_model_calls_call_kind_check
        CHECK (call_kind IN ('generation_single', 'generation_map', 'generation_reduce', 'embedding'));

ALTER TABLE velis.async_tasks
    DROP COLUMN generation_repair_used_at;
