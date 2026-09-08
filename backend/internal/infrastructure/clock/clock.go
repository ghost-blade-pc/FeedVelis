// Package clock 提供可替换的系统时钟。
package clock

import "time"

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }
