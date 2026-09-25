-- 投影槽位、delivery 与重建记录是不可重建的诊断状态：只有在确认为空时才允许回滚。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM velis.search_projection_jobs)
        OR EXISTS (SELECT 1 FROM velis.search_projection_deliveries)
        OR EXISTS (SELECT 1 FROM velis.search_index_rebuilds) THEN
        RAISE EXCEPTION '拒绝回滚搜索投影迁移：存在投影槽位、delivery 或重建记录';
    END IF;
    IF EXISTS (SELECT 1 FROM velis.search_index_state WHERE rollback_index IS NOT NULL) THEN
        RAISE EXCEPTION '拒绝回滚搜索投影迁移：回滚窗口仍打开，需要先确认前一索引已停用';
    END IF;
END $$;

DROP TRIGGER search_projection_deliveries_index_guard ON velis.search_projection_deliveries;
DROP FUNCTION velis.search_projection_delivery_index_guard();
DROP FUNCTION velis.search_projection_delivery_index_allowed(varchar);

DROP TABLE velis.search_projection_deliveries;
DROP TABLE velis.search_index_rebuilds;
DROP TABLE velis.search_index_state;
DROP TABLE velis.search_projection_jobs;

DROP SEQUENCE velis.search_projection_change_seq;
