DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.outbox_events LIMIT 1)
        OR EXISTS (SELECT 1 FROM velis.consumed_events LIMIT 1)
        OR EXISTS (SELECT 1 FROM velis.async_tasks LIMIT 1) THEN
        RAISE EXCEPTION '拒绝删除可靠异步表：表中仍有数据，请先备份并显式清理';
    END IF;
END $$;

DROP TABLE velis.async_tasks;
DROP TABLE velis.consumed_events;
DROP TABLE velis.outbox_events;
