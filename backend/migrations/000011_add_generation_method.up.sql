ALTER TABLE velis.ai_generation_results
    ADD COLUMN generation_method varchar(16) NOT NULL DEFAULT 'model'
    CHECK (generation_method IN ('model', 'extractive'));

DO $$
DECLARE
    old_constraint text;
BEGIN
    SELECT conname INTO old_constraint
    FROM pg_constraint
    WHERE conrelid = 'velis.ai_generation_results'::regclass
      AND contype = 'u'
      AND pg_get_constraintdef(oid) = 'UNIQUE (article_id, revision_id, provider, model, profile_version, workflow_version, prompt_version, generation_input_hash)';
    IF old_constraint IS NULL THEN
        RAISE EXCEPTION '找不到原有生成结果唯一约束';
    END IF;
    EXECUTE format('ALTER TABLE velis.ai_generation_results DROP CONSTRAINT %I', old_constraint);
END $$;

ALTER TABLE velis.ai_generation_results
    ADD CONSTRAINT ai_generation_results_target_method_unique
    UNIQUE (article_id, revision_id, provider, model, profile_version, workflow_version,
            prompt_version, generation_input_hash, generation_method);
