package observability

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func RefreshAITaskStats(ctx context.Context, pool *pgxpool.Pool, metrics *AsyncMetrics) error {
	stages := []string{"generation", "embedding"}
	statuses := []string{"pending", "running", "retry_wait", "succeeded", "failed", "canceled"}
	for _, stage := range stages {
		for _, status := range statuses {
			metrics.AITasks.WithLabelValues(stage, status).Set(0)
			metrics.AIOldestSeconds.WithLabelValues(stage, status).Set(0)
		}
	}
	rows, err := pool.Query(ctx, `SELECT stage,status,count(*),GREATEST(0,EXTRACT(EPOCH FROM clock_timestamp()-min(updated_at))) FROM velis.async_tasks GROUP BY stage,status`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var stage, status string
		var count int
		var age float64
		if err := rows.Scan(&stage, &status, &count, &age); err != nil {
			return err
		}
		metrics.AITasks.WithLabelValues(stage, status).Set(float64(count))
		metrics.AIOldestSeconds.WithLabelValues(stage, status).Set(age)
	}
	return rows.Err()
}
