DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.article_read_windows LIMIT 1)
        OR EXISTS (SELECT 1 FROM velis.article_favorites LIMIT 1)
        OR EXISTS (SELECT 1 FROM velis.article_not_interested LIMIT 1) THEN
        RAISE EXCEPTION '拒绝删除非空反馈表：请先备份、导出并人工确认清理反馈事实';
    END IF;
END $$;

DROP TABLE velis.article_not_interested;
DROP TABLE velis.article_favorites;
DROP TABLE velis.article_read_windows;
