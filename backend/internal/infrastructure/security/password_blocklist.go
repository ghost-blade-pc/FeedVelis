package security

import (
	_ "embed"
	"strings"
)

//go:embed data/10k-most-common.txt
var commonPasswordsRaw string

// CommonPasswordBlocklist 实现 account.Blocklist：随应用发布的本地常见密码表。
// 只做完整匹配，比较前把输入密码的 ASCII 字母转小写；不访问外部服务。
type CommonPasswordBlocklist struct{ entries map[string]struct{} }

func NewCommonPasswordBlocklist() *CommonPasswordBlocklist {
	entries := make(map[string]struct{}, 10240)
	for _, line := range strings.Split(commonPasswordsRaw, "\n") {
		entry := strings.TrimRight(line, "\r")
		if entry == "" {
			continue
		}
		entries[strings.ToLower(entry)] = struct{}{}
	}
	return &CommonPasswordBlocklist{entries: entries}
}

// Size 返回装载的条目数，供启动日志与测试核对。
func (b *CommonPasswordBlocklist) Size() int { return len(b.entries) }

func (b *CommonPasswordBlocklist) Contains(loweredPassword string) bool {
	_, ok := b.entries[loweredPassword]
	return ok
}
