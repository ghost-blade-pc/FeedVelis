package account

import (
	"errors"
	"testing"
)

type fakeBlocklist map[string]struct{}

func (f fakeBlocklist) Contains(loweredPassword string) bool {
	_, ok := f[loweredPassword]
	return ok
}

func TestValidatePasswordBoundaries(t *testing.T) {
	list := fakeBlocklist{}
	cases := []struct {
		name     string
		password string
		want     error
	}{
		{"7 位过短", "Abc123!", ErrPasswordLength},
		{"8 位合法", "Abcd123!", nil},
		{"20 位合法", "Abcd123!Abcd123!Abcd", nil},
		{"21 位过长", "Abcd123!Abcd123!Abcd1", ErrPasswordLength},
		{"仅两类", "abcdefg1", ErrPasswordClasses},
		{"三类合法", "Abcdefg1", nil},
		{"四类合法", "Abcdefg1!", nil},
		{"含空格", "Abcd 123!", ErrPasswordCharset},
		{"含中文", "Abcd123!中文", ErrPasswordCharset},
		{"含零宽字符", "Abcd123!​", ErrPasswordCharset},
		{"含制表符", "Abcd123!\t", ErrPasswordCharset},
		{"含全角字符", "Ａbcd123!", ErrPasswordCharset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePassword(tc.password, "alice", list); !errors.Is(err, tc.want) {
				t.Fatalf("ValidatePassword(%q) = %v，期望 %v", tc.password, err, tc.want)
			}
		})
	}
}

func TestValidatePasswordRejectsUsernameAndBlocklist(t *testing.T) {
	if err := ValidatePassword("alice_user1", "alice_user1", fakeBlocklist{}); !errors.Is(err, ErrPasswordEqualsUsername) {
		t.Fatalf("与用户名相同应被拒绝: %v", err)
	}
	common := fakeBlocklist{"password1": {}}
	if err := ValidatePassword("Password1", "alice", common); !errors.Is(err, ErrPasswordTooCommon) {
		t.Fatalf("大小写变体应命中密码表: %v", err)
	}
	if err := ValidatePassword("Password12", "alice", fakeBlocklist{"password1": {}}); err != nil {
		t.Fatalf("子串不得命中密码表: %v", err)
	}
	if err := ValidatePassword("Abcd123!", "alice", nil); !errors.Is(err, ErrBlocklistUnavailable) {
		t.Fatalf("缺少密码表应报错: %v", err)
	}
}

func TestValidatePasswordDoesNotTrimOrNormalize(t *testing.T) {
	if err := ValidatePassword(" Abcd123!", "alice", fakeBlocklist{}); !errors.Is(err, ErrPasswordCharset) {
		t.Fatalf("首尾空白不得被裁剪: %v", err)
	}
}
