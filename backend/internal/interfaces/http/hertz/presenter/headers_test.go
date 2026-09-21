package presenter

import (
	"errors"
	"testing"
)

func TestParseIdempotencyKeyAcceptsUUIDAndNormalizes(t *testing.T) {
	key, err := ParseIdempotencyKey("6F1C6B1E-2E4E-4F1B-9C7A-3F2D5B8A0C11")
	if err != nil {
		t.Fatalf("合法键被拒绝: %v", err)
	}
	if key != "6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c11" {
		t.Fatalf("规范化结果 = %q", key)
	}
}

func TestParseIdempotencyKeyRejectsMissingAndInvalid(t *testing.T) {
	cases := map[string]struct {
		header string
		want   error
	}{
		"缺失":     {"", ErrIdempotencyKeyRequired},
		"仅空白":    {"   ", ErrIdempotencyKeyRequired},
		"非 UUID": {"article-1", ErrIdempotencyKeyInvalid},
		"长度不足":   {"6f1c6b1e-2e4e-4f1b-9c7a", ErrIdempotencyKeyInvalid},
		"非法字符":   {"6f1c6b1e-2e4e-4f1b-9c7a-3f2d5b8a0c1z", ErrIdempotencyKeyInvalid},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseIdempotencyKey(testCase.header); !errors.Is(err, testCase.want) {
				t.Fatalf("err = %v，期望 %v", err, testCase.want)
			}
		})
	}
}

func TestParseIfMatchAcceptsStrongETagOnly(t *testing.T) {
	version, err := ParseIfMatch(` "7" `)
	if err != nil || version != 7 {
		t.Fatalf("version = %d err = %v", version, err)
	}
}

func TestParseIfMatchRejectsWeakWildcardListAndLeadingZero(t *testing.T) {
	cases := map[string]struct {
		header string
		want   error
	}{
		"缺失":     {"", ErrIfMatchRequired},
		"仅空白":    {"  ", ErrIfMatchRequired},
		"弱 ETag": {`W/"7"`, ErrIfMatchInvalid},
		"通配符":    {"*", ErrIfMatchInvalid},
		"未加引号":   {"7", ErrIfMatchInvalid},
		"列表":     {`"7", "8"`, ErrIfMatchInvalid},
		"前导零":    {`"07"`, ErrIfMatchInvalid},
		"零版本":    {`"0"`, ErrIfMatchInvalid},
		"负版本":    {`"-1"`, ErrIfMatchInvalid},
		"非数字":    {`"abc"`, ErrIfMatchInvalid},
		"空引号":    {`""`, ErrIfMatchInvalid},
		"溢出":     {`"99999999999999999999"`, ErrIfMatchInvalid},
		"内嵌引号":   {`"7"x"`, ErrIfMatchInvalid},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseIfMatch(testCase.header); !errors.Is(err, testCase.want) {
				t.Fatalf("err = %v，期望 %v", err, testCase.want)
			}
		})
	}
}

func TestETagRoundTripsThroughParseIfMatch(t *testing.T) {
	for _, version := range []int64{1, 42, 9007199254740993} {
		parsed, err := ParseIfMatch(ETag(version))
		if err != nil || parsed != version {
			t.Fatalf("ETag(%d) 往返得到 %d err = %v", version, parsed, err)
		}
	}
}
