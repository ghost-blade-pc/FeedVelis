ALTER TABLE velis.async_tasks
    ADD COLUMN generation_repair_used_at timestamptz NULL;

ALTER TABLE velis.ai_model_calls
    DROP CONSTRAINT ai_model_calls_call_kind_check,
    ADD COLUMN structured_output_mode varchar(16) NULL,
    ADD COLUMN error_reason varchar(64) NULL;

UPDATE velis.ai_model_calls
SET structured_output_mode = 'prompt'
WHERE stage = 'generation';

ALTER TABLE velis.ai_model_calls
    ADD CONSTRAINT ai_model_calls_call_kind_check
        CHECK (call_kind IN ('generation_single', 'generation_map', 'generation_reduce', 'generation_repair', 'embedding')),
    ADD CONSTRAINT ai_model_calls_structured_output_mode_check
        CHECK ((stage = 'generation' AND structured_output_mode IN ('prompt', 'json_object', 'json_schema'))
            OR (stage = 'embedding' AND structured_output_mode IS NULL)),
    ADD CONSTRAINT ai_model_calls_error_reason_check
        CHECK (error_reason IS NULL
            OR (status = 'failed'
                AND error_code = 'invalid_output'
                AND error_reason ~ '^[a-z][a-z0-9_]{0,63}$'));
