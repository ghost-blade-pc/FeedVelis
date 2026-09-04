// Package ports 声明 Application 层所需的技术能力端口。
package ports

import "context"

// HealthChecker 表示可用于就绪检查的必要依赖。
type HealthChecker interface {
	Ping(context.Context) error
}
