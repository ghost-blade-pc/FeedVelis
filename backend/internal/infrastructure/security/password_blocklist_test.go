package security

import (
	"strings"
	"testing"
)

func TestCommonPasswordBlocklistMatchesWholePassword(t *testing.T) {
	list := NewCommonPasswordBlocklist()
	if list.Size() < 9000 {
		t.Fatalf("密码表条目数 = %d", list.Size())
	}
	for _, password := range []string{"password", "123456", "qwerty"} {
		if !list.Contains(password) {
			t.Fatalf("%q 应在常见密码表中", password)
		}
	}
	if list.Contains("password1extra") {
		t.Fatal("不得做子串或前缀匹配")
	}
	if list.Contains(strings.Repeat("z", 20)) {
		t.Fatal("随机长密码不应命中")
	}
}
