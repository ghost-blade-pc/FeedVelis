-- 标准回退为关闭 generation 并保留表；禁止通过删除用户事实让 down 成功。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.agent_runs)
       OR EXISTS (SELECT 1 FROM velis.agent_run_evidence)
       OR EXISTS (SELECT 1 FROM velis.agent_run_blocks)
       OR EXISTS (SELECT 1 FROM velis.agent_run_block_evidence)
       OR EXISTS (SELECT 1 FROM velis.agent_run_events)
       OR EXISTS (SELECT 1 FROM velis.idempotency_operations WHERE operation = 'agent.run.accept')
    THEN
        RAISE EXCEPTION '拒绝回滚 Agent 回合迁移：存在回合、预留、助手关联或接受幂等事实，请关闭生成并保留历史';
    END IF;
END $$;

DROP INDEX velis.idempotency_agent_run_resource_idx;
DROP TABLE velis.agent_run_events;
DROP TABLE velis.agent_run_block_evidence;
DROP TABLE velis.agent_run_blocks;
DROP TABLE velis.agent_run_evidence;
DROP TABLE velis.agent_runs;
ALTER TABLE velis.agent_messages DROP CONSTRAINT agent_messages_id_conversation_role_key;
ALTER TABLE velis.agent_conversations DROP CONSTRAINT agent_conversations_id_user_key;
