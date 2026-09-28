CREATE TABLE velis.article_read_windows (
    user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    article_id bigint NOT NULL REFERENCES velis.articles(id) ON DELETE RESTRICT,
    window_start timestamptz NOT NULL,
    first_seen_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, article_id, window_start),
    CHECK (window_start = date_bin('30 minutes', window_start, TIMESTAMPTZ '1970-01-01 00:00:00+00'))
);
CREATE INDEX article_read_windows_cleanup_idx ON velis.article_read_windows (first_seen_at);
CREATE INDEX article_read_windows_user_recent_idx ON velis.article_read_windows (user_id, first_seen_at DESC, article_id);

CREATE TABLE velis.article_favorites (
    user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    article_id bigint NOT NULL REFERENCES velis.articles(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, article_id)
);

CREATE TABLE velis.article_not_interested (
    user_id uuid NOT NULL REFERENCES velis.users(id) ON DELETE RESTRICT,
    article_id bigint NOT NULL REFERENCES velis.articles(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, article_id),
    CHECK (expires_at > created_at)
);
CREATE INDEX article_not_interested_cleanup_idx ON velis.article_not_interested (expires_at);
