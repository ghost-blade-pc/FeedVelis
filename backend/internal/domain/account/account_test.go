package account

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeUsername(t *testing.T) {
	got, err := NormalizeUsername("Alice_01")
	if err != nil {
		t.Fatal(err)
	}
	if got != "alice_01" {
		t.Fatalf("normalized = %q", got)
	}
	if _, err := NormalizeUsername(strings.Repeat("a", MaxUsernameBytes)); err != nil {
		t.Fatalf("32 位用户名应有效: %v", err)
	}
	for _, raw := range []string{
		"al", "2alice", "_alice", "alice-01", "ali ce", " alice", "alice ",
		"álice", "爱丽丝", strings.Repeat("a", MaxUsernameBytes+1), "",
	} {
		if _, err := NormalizeUsername(raw); !errors.Is(err, ErrInvalidUsername) {
			t.Fatalf("%q 应被拒绝: %v", raw, err)
		}
	}
}

func TestNormalizeNickname(t *testing.T) {
	got, err := NormalizeNickname("  中文昵称  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "中文昵称" {
		t.Fatalf("nickname = %q", got)
	}
	if _, err := NormalizeNickname(strings.Repeat("测", MaxNicknameRunes)); err != nil {
		t.Fatalf("32 码点昵称应有效: %v", err)
	}
	for _, raw := range []string{
		"", "   ", strings.Repeat("测", MaxNicknameRunes+1), "昵\n称", "昵\r称", "昵\t称", "昵\x00称",
	} {
		if _, err := NormalizeNickname(raw); !errors.Is(err, ErrInvalidNickname) {
			t.Fatalf("%q 应被拒绝: %v", raw, err)
		}
	}
}

func TestDefaultNicknameUsesNormalizedUsername(t *testing.T) {
	normalized, err := NormalizeUsername("Alice")
	if err != nil {
		t.Fatal(err)
	}
	if got := DefaultNickname(normalized); got != "alice" {
		t.Fatalf("default nickname = %q", got)
	}
}

func TestCheckLastAdmin(t *testing.T) {
	admin := User{Role: RoleAdmin, Status: StatusActive}
	if err := admin.CheckLastAdmin(0); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("最后一位管理员应受保护: %v", err)
	}
	if err := admin.CheckLastAdmin(1); err != nil {
		t.Fatalf("仍有其他管理员时不应拒绝: %v", err)
	}
	user := User{Role: RoleUser, Status: StatusActive}
	if err := user.CheckLastAdmin(0); err != nil {
		t.Fatalf("普通用户不受最后管理员保护: %v", err)
	}
	disabled := User{Role: RoleAdmin, Status: StatusDisabled}
	if err := disabled.CheckLastAdmin(0); err != nil {
		t.Fatalf("已禁用管理员不计入保护: %v", err)
	}
}
