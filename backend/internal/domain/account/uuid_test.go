package account

import (
	"errors"
	"testing"
)

func TestNewUUIDSetsVersionAndVariant(t *testing.T) {
	first, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("两次生成不应相同")
	}
	if first[6]>>4 != 4 {
		t.Fatalf("版本位 = %x", first[6]>>4)
	}
	if first[8]>>6 != 2 {
		t.Fatalf("变体位 = %x", first[8]>>6)
	}
	if len(first.String()) != 36 {
		t.Fatalf("字符串长度 = %d", len(first.String()))
	}
}

func TestParseUUID(t *testing.T) {
	value, err := ParseUUID("3f2504e0-4f89-41d3-9a0c-0305e82c3301")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.String(); got != "3f2504e0-4f89-41d3-9a0c-0305e82c3301" {
		t.Fatalf("round-trip = %q", got)
	}
	compact, err := ParseUUID("3f2504e04f8941d39a0c0305e82c3301")
	if err != nil || compact != value {
		t.Fatalf("紧凑形式解析失败: %v", err)
	}
	for _, raw := range []string{"", "zz", "3f2504e0-4f89-41d3-9a0c", "3f2504e0_4f89_41d3_9a0c_0305e82c3301"} {
		if _, err := ParseUUID(raw); !errors.Is(err, ErrInvalidUUID) {
			t.Fatalf("%q 应被拒绝: %v", raw, err)
		}
	}
	if !(UUID{}).IsZero() {
		t.Fatal("零值应为 IsZero")
	}
}
