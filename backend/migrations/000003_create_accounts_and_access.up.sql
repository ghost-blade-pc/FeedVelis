CREATE TABLE velis.users (
    id uuid PRIMARY KEY,
    username varchar(32) NOT NULL UNIQUE CHECK (username ~ '^[a-z][a-z0-9_]{2,31}$'),
    nickname varchar(32) NOT NULL CHECK (char_length(nickname) BETWEEN 1 AND 32),
    password_hash text NOT NULL CHECK (octet_length(password_hash) BETWEEN 1 AND 512),
    role varchar(16) NOT NULL CHECK (role IN ('user', 'admin')),
    status varchar(16) NOT NULL CHECK (status IN ('active', 'disabled')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE velis.auth_sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    last_refreshed_at timestamptz NOT NULL,
    revoked_at timestamptz NULL,
    revoke_reason varchar(32) NULL CHECK (revoke_reason IN ('logout', 'disabled', 'role_changed', 'replay')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    CHECK (expires_at > created_at),
    CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);

CREATE INDEX auth_sessions_user_active_idx ON velis.auth_sessions (user_id, expires_at) WHERE revoked_at IS NULL;
CREATE INDEX auth_sessions_cleanup_idx ON velis.auth_sessions (expires_at);

CREATE TABLE velis.refresh_tokens (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES velis.auth_sessions(id) ON DELETE CASCADE,
    digest bytea NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz NULL,
    -- 后继令牌与消费写入同一事务，外键延后到提交时校验以满足"先消费后插入"的顺序。
    successor_id uuid NULL REFERENCES velis.refresh_tokens(id) ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED,
    CHECK (expires_at > issued_at),
    CHECK (successor_id IS NULL OR consumed_at IS NOT NULL)
);

CREATE UNIQUE INDEX refresh_tokens_session_pending_idx ON velis.refresh_tokens (session_id) WHERE consumed_at IS NULL;
CREATE INDEX refresh_tokens_cleanup_idx ON velis.refresh_tokens (expires_at);

CREATE TABLE velis.login_failure_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    dimension varchar(16) NOT NULL CHECK (dimension IN ('account', 'ip')),
    lookup_key bytea NOT NULL CHECK (octet_length(lookup_key) = 32),
    occurred_at timestamptz NOT NULL
);

CREATE INDEX login_failure_events_window_idx ON velis.login_failure_events (dimension, lookup_key, occurred_at);

CREATE TABLE velis.login_blocks (
    dimension varchar(16) NOT NULL CHECK (dimension IN ('account', 'ip')),
    lookup_key bytea NOT NULL CHECK (octet_length(lookup_key) = 32),
    blocked_until timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (dimension, lookup_key)
);

CREATE INDEX login_blocks_until_idx ON velis.login_blocks (blocked_until);

CREATE TABLE velis.account_audit_logs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action varchar(64) NOT NULL,
    target_user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    target_username varchar(32) NOT NULL,
    from_role varchar(16) NULL CHECK (from_role IS NULL OR from_role IN ('user', 'admin')),
    to_role varchar(16) NULL CHECK (to_role IS NULL OR to_role IN ('user', 'admin')),
    from_status varchar(16) NULL CHECK (from_status IS NULL OR from_status IN ('active', 'disabled')),
    to_status varchar(16) NULL CHECK (to_status IS NULL OR to_status IN ('active', 'disabled')),
    source varchar(32) NOT NULL,
    operation_id uuid NOT NULL,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX account_audit_logs_target_idx ON velis.account_audit_logs (target_user_id, occurred_at DESC);
CREATE INDEX account_audit_logs_action_idx ON velis.account_audit_logs (action, occurred_at DESC);
