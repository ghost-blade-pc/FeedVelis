package agentconversation

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestEncryptedCursorScopeExpiryAndContinuation(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	codec, err := NewCursorCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	list := Cursor{UserID: unitUser, Purpose: ListPurpose, ID: unitID, ActivityAt: now, StartedAt: now, ExpiresAt: now.Add(time.Hour)}
	history := Cursor{UserID: unitUser, Purpose: HistoryPurpose, ConversationID: unitID, BeforeSequence: 46, StartedAt: now, ExpiresAt: now.Add(time.Hour)}
	for _, value := range []Cursor{list, history} {
		raw, err := codec.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
		second, err := codec.Encode(value)
		if err != nil || raw == second {
			t.Fatal("随机nonce未变化")
		}
		data, _ := base64.RawURLEncoding.DecodeString(raw)
		if bytes.Contains(data, []byte(unitUser)) || bytes.Contains(data, []byte(unitID)) {
			t.Fatal("游标泄露身份")
		}
		decoded, err := codec.Decode(raw, unitUser, value.Purpose, value.ConversationID, now.Add(30*time.Minute))
		if err != nil || decoded != value {
			t.Fatalf("往返: %+v %v", decoded, err)
		}
		// 改变批次不参与绑定；续页必须沿用原截止时间。
		decoded.BeforeSequence = value.BeforeSequence
		continued, err := codec.Encode(decoded)
		if err != nil {
			t.Fatal(err)
		}
		last, err := codec.Decode(continued, unitUser, value.Purpose, value.ConversationID, now.Add(59*time.Minute))
		if err != nil || !last.ExpiresAt.Equal(value.ExpiresAt) {
			t.Fatal("续页延长期限")
		}
		other, _ := NewCursorCodec(bytes.Repeat([]byte{8}, 32))
		if _, err := other.Decode(raw, unitUser, value.Purpose, value.ConversationID, now); err != ErrInvalidCursor {
			t.Fatal("密钥变化未拒绝")
		}
		for _, test := range []struct {
			raw, user, purpose, id string
			now                    time.Time
		}{
			{raw, unitKey, value.Purpose, value.ConversationID, now},
			{raw, unitUser, "other", value.ConversationID, now},
			{raw, unitUser, value.Purpose, unitKey, now},
			{raw, unitUser, value.Purpose, value.ConversationID, value.ExpiresAt},
			{raw + "!", unitUser, value.Purpose, value.ConversationID, now},
			{strings.Repeat("a", 4097), unitUser, value.Purpose, value.ConversationID, now},
			{"", unitUser, value.Purpose, value.ConversationID, now},
		} {
			if _, err := codec.Decode(test.raw, test.user, test.purpose, test.id, test.now); err != ErrInvalidCursor {
				t.Fatal("无效绑定或格式未拒绝")
			}
		}
		data[0] = 2
		if _, err := codec.Decode(base64.RawURLEncoding.EncodeToString(data), unitUser, value.Purpose, value.ConversationID, now); err != ErrInvalidCursor {
			t.Fatal("未知版本未拒绝")
		}
		data[0] = 1
		data[len(data)-1] ^= 1
		if _, err := codec.Decode(base64.RawURLEncoding.EncodeToString(data), unitUser, value.Purpose, value.ConversationID, now); err != ErrInvalidCursor {
			t.Fatal("篡改未拒绝")
		}
	}
	if _, err := NewCursorCodec(key[:31]); err == nil {
		t.Fatal("短密钥未拒绝")
	}
}
