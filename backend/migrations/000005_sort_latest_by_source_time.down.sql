DROP INDEX velis.articles_latest_idx;

CREATE INDEX articles_latest_idx
    ON velis.articles (published_at DESC, id DESC)
    WHERE status = 'published';
