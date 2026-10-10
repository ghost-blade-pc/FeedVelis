-- 只允许空业务事实回退；用户状态行可重建，删除标记及过期去重记录也必须保护。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.agent_conversations)
	   OR EXISTS (SELECT 1 FROM velis.agent_user_state WHERE conversation_count <> 0)
       OR EXISTS (SELECT 1 FROM velis.agent_messages)
       OR EXISTS (SELECT 1 FROM velis.agent_conversation_deletions)
       OR EXISTS (SELECT 1 FROM velis.idempotency_operations
                  WHERE resource_type = 'agent_conversation'
                     OR operation IN ('agent.conversation.create', 'agent.message.append', 'agent.conversation.rename'))
    THEN
        RAISE EXCEPTION '拒绝回滚 Agent 会话迁移：存在历史、删除标记或幂等记录，请先备份并明确回滚方案';
    END IF;
END $$;

DROP INDEX velis.idempotency_agent_resource_idx;
DROP TABLE velis.agent_conversation_deletions;
DROP TABLE velis.agent_messages;
DROP TABLE velis.agent_conversations;
DROP TABLE velis.agent_user_state;
