CREATE TABLE velis.agent_user_state (
    user_id uuid PRIMARY KEY REFERENCES velis.users(id) ON DELETE RESTRICT,
    conversation_count integer NOT NULL DEFAULT 0 CHECK (conversation_count >= 0)
);

CREATE TABLE velis.agent_conversations (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 100),
    title_version bigint NOT NULL DEFAULT 1 CHECK (title_version >= 1),
    message_count integer NOT NULL DEFAULT 0 CHECK (message_count >= 0),
    next_sequence bigint NOT NULL DEFAULT 1 CHECK (next_sequence >= 1),
    created_at timestamptz NOT NULL,
    last_activity_at timestamptz NOT NULL
);
CREATE INDEX agent_conversations_list_idx
    ON velis.agent_conversations (user_id, last_activity_at DESC, id DESC);

CREATE TABLE velis.agent_messages (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES velis.agent_conversations(id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    role varchar(16) NOT NULL CHECK (role IN ('user', 'assistant')),
    content text NOT NULL CHECK (char_length(content) > 0),
    created_at timestamptz NOT NULL,
    UNIQUE (conversation_id, sequence)
);

CREATE TABLE velis.agent_conversation_deletions (
    conversation_id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    deleted_at timestamptz NOT NULL
);
CREATE INDEX agent_conversation_deletions_user_idx ON velis.agent_conversation_deletions (user_id);

CREATE INDEX idempotency_agent_resource_idx
    ON velis.idempotency_operations (actor_user_id, resource_id)
    WHERE resource_type = 'agent_conversation'
      AND operation IN ('agent.conversation.create', 'agent.message.append', 'agent.conversation.rename');
