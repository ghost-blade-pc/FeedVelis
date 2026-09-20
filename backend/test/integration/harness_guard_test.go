package integration

import "testing"

// TestTestDatabaseNameUsesEffectiveTarget 钉住 _test 库保护所依据的取值：
// 必须是 DSN 实际生效的库名，而不是 path 的字面内容。
// 回归背景：查询串里的 dbname/database 会覆盖 path，旧实现只看 path，
// 于是「path 以 _test 结尾、实际连到别的库」能通过检查，
// 随后的迁移与 TRUNCATE ... RESTART IDENTITY CASCADE 会打到那个库。
func TestTestDatabaseNameUsesEffectiveTarget(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want string
	}{
		{"只有路径", "postgres://velis:velis@localhost:5432/velis_test?sslmode=disable", "velis_test"},
		{"查询串 dbname 覆盖路径", "postgres://velis:velis@localhost:5432/velis_test?dbname=velis", "velis"},
		{"查询串 database 覆盖路径", "postgres://velis:velis@localhost:5432/velis_test?database=velis", "velis"},
		{"查询串同时指定库名与主机", "postgres://velis:velis@localhost:5432/velis_test?host=db.example.com&dbname=velis", "velis"},
		{"只有查询串给出库名", "postgres://velis:velis@localhost:5432/?dbname=velis", "velis"},
		{"keyword/value 形式", "host=localhost port=5432 dbname=velis_test", "velis_test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := testDatabaseName(tc.dsn)
			if err != nil {
				t.Fatalf("解析 %q: %v", tc.dsn, err)
			}
			if got != tc.want {
				t.Fatalf("实际生效的库名：期望 %q，实际 %q", tc.want, got)
			}
		})
	}
}
