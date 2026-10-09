DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.ai_generation_results WHERE generation_method = 'extractive') THEN
        RAISE EXCEPTION '存在摘录降级结果：请先备份并人工确认回滚方案';
    END IF;
END $$;

ALTER TABLE velis.ai_generation_results
    DROP CONSTRAINT ai_generation_results_target_method_unique,
    DROP COLUMN generation_method;

ALTER TABLE velis.ai_generation_results
    ADD CONSTRAINT ai_generation_results_target_unique
    UNIQUE (article_id, revision_id, provider, model, profile_version, workflow_version,
            prompt_version, generation_input_hash);
