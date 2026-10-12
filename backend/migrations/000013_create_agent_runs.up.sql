-- 复合外键将所有者、会话、消息角色和回合身份绑定，旧消息格式保持兼容。
ALTER TABLE velis.agent_conversations ADD CONSTRAINT agent_conversations_id_user_key UNIQUE (id, user_id);
ALTER TABLE velis.agent_messages ADD CONSTRAINT agent_messages_id_conversation_role_key UNIQUE (id, conversation_id, role);

CREATE TABLE velis.agent_runs (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL,
    user_id uuid NOT NULL,
    auth_session_id uuid NOT NULL,
    user_message_id uuid NOT NULL,
    user_message_role varchar(16) NOT NULL DEFAULT 'user' CHECK (user_message_role = 'user'),
    assistant_message_id uuid,
    assistant_message_role varchar(16) NOT NULL DEFAULT 'assistant' CHECK (assistant_message_role = 'assistant'),
    attempt integer NOT NULL CHECK (attempt BETWEEN 1 AND 3),
    retry_of uuid,
    context_article_id bigint CHECK (context_article_id > 0),
    constraints jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(constraints) = 'object'),
    status varchar(16) NOT NULL CHECK (status IN ('accepted','running','succeeded','failed','canceled','timed_out')),
    assistant_reserved boolean NOT NULL,
    created_at timestamptz NOT NULL,
    deadline timestamptz NOT NULL CHECK (deadline > created_at),
    finished_at timestamptz,
    owner_token uuid,
    lease_until timestamptz,
    error_code varchar(64),
    answer jsonb CHECK (jsonb_typeof(answer) = 'object' AND answer->>'version' = '1'),
    usage jsonb NOT NULL DEFAULT '{"model_calls":0,"tool_calls":0,"input_tokens":null,"output_tokens":null,"total_tokens":null,"usage_complete":false}'::jsonb CHECK (jsonb_typeof(usage) = 'object'),
    execution_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(execution_metadata) = 'object'),
    event_count integer NOT NULL DEFAULT 0 CHECK (event_count BETWEEN 0 AND 128),
    event_bytes integer NOT NULL DEFAULT 0 CHECK (event_bytes BETWEEN 0 AND 524288),
    events_expired boolean NOT NULL DEFAULT false,
    UNIQUE (id, conversation_id, user_message_id),
    UNIQUE (user_message_id, attempt),
    UNIQUE (assistant_message_id),
    FOREIGN KEY (conversation_id, user_id) REFERENCES velis.agent_conversations(id, user_id) ON DELETE CASCADE,
    FOREIGN KEY (user_message_id, conversation_id, user_message_role) REFERENCES velis.agent_messages(id, conversation_id, role) ON DELETE CASCADE,
    FOREIGN KEY (assistant_message_id, conversation_id, assistant_message_role) REFERENCES velis.agent_messages(id, conversation_id, role) DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (retry_of, conversation_id, user_message_id) REFERENCES velis.agent_runs(id, conversation_id, user_message_id) DEFERRABLE INITIALLY DEFERRED,
    CHECK ((attempt = 1 AND retry_of IS NULL) OR (attempt > 1 AND retry_of IS NOT NULL AND retry_of <> id)),
    CHECK (assistant_reserved = (status IN ('accepted','running'))),
    CHECK ((status IN ('accepted','running') AND finished_at IS NULL) OR (status NOT IN ('accepted','running') AND finished_at IS NOT NULL)),
    CHECK ((status = 'running' AND owner_token IS NOT NULL AND lease_until IS NOT NULL AND lease_until <= deadline)
        OR (status <> 'running' AND owner_token IS NULL AND lease_until IS NULL)),
    CHECK ((status = 'succeeded' AND assistant_message_id IS NOT NULL AND answer IS NOT NULL AND error_code IS NULL)
        OR (status <> 'succeeded' AND assistant_message_id IS NULL AND answer IS NULL)),
    CHECK ((status IN ('failed','canceled','timed_out') AND error_code IS NOT NULL)
        OR (status IN ('accepted','running','succeeded') AND error_code IS NULL))
);
CREATE UNIQUE INDEX agent_runs_conversation_active_idx ON velis.agent_runs(conversation_id) WHERE status IN ('accepted','running');
CREATE UNIQUE INDEX agent_runs_message_success_idx ON velis.agent_runs(user_message_id) WHERE status = 'succeeded';
CREATE INDEX agent_runs_list_idx ON velis.agent_runs(user_id, conversation_id, created_at DESC, id DESC);
CREATE INDEX agent_runs_pending_idx ON velis.agent_runs(created_at, id) WHERE status = 'accepted';
CREATE INDEX agent_runs_active_account_idx ON velis.agent_runs(user_id) WHERE status IN ('accepted','running');
CREATE INDEX agent_runs_maintenance_idx ON velis.agent_runs(deadline, lease_until) WHERE status IN ('accepted','running');
CREATE INDEX agent_runs_retention_idx ON velis.agent_runs(finished_at, id) WHERE finished_at IS NOT NULL;

CREATE TABLE velis.agent_run_evidence (
    run_id uuid NOT NULL REFERENCES velis.agent_runs(id) ON DELETE CASCADE,
    evidence_id uuid NOT NULL,
    version integer NOT NULL CHECK (version = 1),
    kind varchar(16) NOT NULL CHECK (kind = 'article'),
    article_id bigint NOT NULL CHECK (article_id > 0),
    revision_id bigint NOT NULL CHECK (revision_id > 0),
    obtained_at timestamptz NOT NULL,
    content_kind varchar(16) NOT NULL CHECK (content_kind IN ('summary','body')),
    truncated boolean NOT NULL,
    content text,
    reference_number integer CHECK (reference_number BETWEEN 1 AND 10),
    card jsonb CHECK (jsonb_typeof(card) = 'object'),
    quote_checks jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(quote_checks) = 'array'),
    PRIMARY KEY (run_id, evidence_id)
);

CREATE TABLE velis.agent_run_blocks (
    run_id uuid NOT NULL REFERENCES velis.agent_runs(id) ON DELETE CASCADE,
    block_id varchar(100) NOT NULL CHECK (char_length(block_id) > 0),
    position integer NOT NULL CHECK (position BETWEEN 1 AND 32),
    text text NOT NULL CHECK (char_length(text) > 0),
    quotes jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(quotes) = 'array'),
    PRIMARY KEY (run_id, block_id),
    UNIQUE (run_id, position)
);

-- 显式关系保证跨回合 evidence_id 不能落库；顺序用于稳定重建片段引用。
CREATE TABLE velis.agent_run_block_evidence (
    run_id uuid NOT NULL,
    block_id varchar(100) NOT NULL,
    evidence_id uuid NOT NULL,
    position integer NOT NULL CHECK (position > 0),
    PRIMARY KEY (run_id, block_id, evidence_id),
    UNIQUE (run_id, block_id, position),
    FOREIGN KEY (run_id, block_id) REFERENCES velis.agent_run_blocks(run_id, block_id) ON DELETE CASCADE,
    FOREIGN KEY (run_id, evidence_id) REFERENCES velis.agent_run_evidence(run_id, evidence_id) ON DELETE CASCADE
);

CREATE TABLE velis.agent_run_events (
    run_id uuid NOT NULL REFERENCES velis.agent_runs(id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence BETWEEN 1 AND 128),
    version integer NOT NULL CHECK (version = 1),
    kind varchar(32) NOT NULL CHECK (kind IN ('run.status','tool.status','answer.sources','answer.block','run.terminal')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    payload_bytes integer GENERATED ALWAYS AS (octet_length(payload::text)) STORED,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (run_id, sequence),
    CHECK (payload_bytes <= 524288)
);

-- 新接受操作纳入关联查询；旧索引不改写，便于只回退本次迁移。
CREATE INDEX idempotency_agent_run_resource_idx ON velis.idempotency_operations(actor_user_id, resource_id)
    WHERE resource_type = 'agent_conversation' AND operation = 'agent.run.accept';
