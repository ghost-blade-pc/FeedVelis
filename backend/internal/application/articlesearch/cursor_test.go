package articlesearch

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCursorCodecRoundTripAndLimitDoesNotChangeFingerprint(t *testing.T) {
	codec, _ := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	query := Query{Q: "搜索", Filters: Filters{Keyword: "Go", SourceID: 7}, Limit: 20}
	position := SortPosition{Score: 1.25, PublishedAt: now.Add(-time.Hour), ArticleID: 9}
	token, err := codec.Encode(query, "pit-secret", position, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	query.Limit = 50
	pitID, decoded, err := codec.Decode(query, token, now)
	if err != nil || pitID != "pit-secret" || decoded != position {
		t.Fatalf("往返失败: pit=%q position=%+v err=%v", pitID, decoded, err)
	}
	query.Q = "其它"
	if _, _, err := codec.Decode(query, token, now); CodeOf(err) != CodeInvalidCursor {
		t.Fatalf("查询改变必须拒绝: %v", err)
	}
}

func TestCursorCodecRejectsInvalidInputs(t *testing.T) {
	codec, _ := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	query := Query{Q: "q", Limit: 20}
	position := SortPosition{Score: 1, PublishedAt: now, ArticleID: 1}
	valid, _ := codec.Encode(query, "pit", position, now.Add(time.Minute))

	unknown := cursorPayload{Version: 1, QueryPlanVersion: QueryPlanVersion, Fingerprint: QueryFingerprint(query), PITID: "pit", After: position, ExpiresAt: now.Add(time.Minute)}
	payload, _ := json.Marshal(unknown)
	payload = append(payload[:len(payload)-1], []byte(`,"unknown":true}`)...)
	unknownToken := signedToken(codec, payload)

	badVersion := unknown
	badVersion.Version = 2
	badVersionPayload, _ := json.Marshal(badVersion)
	badSort := unknown
	badSort.After.Score = math.NaN()

	cases := []string{
		"", strings.Repeat("a", maxCursorBytes+1), valid + "x", unknownToken,
		signedToken(codec, badVersionPayload), signedToken(codec, []byte(`{"v":1}`)),
	}
	for index, token := range cases {
		if _, _, err := codec.Decode(query, token, now); CodeOf(err) != CodeInvalidCursor {
			t.Fatalf("case %d 未拒绝: %v", index, err)
		}
	}
	if _, _, err := codec.Decode(query, valid, now.Add(time.Minute)); CodeOf(err) != CodeInvalidCursor {
		t.Fatalf("到期游标未拒绝: %v", err)
	}
	if _, err := codec.Encode(query, "pit", badSort.After, now.Add(time.Minute)); CodeOf(err) != CodeInvalidCursor {
		t.Fatalf("非法排序值未拒绝: %v", err)
	}
}

func signedToken(codec *CursorCodec, payload []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(codec.sign(payload))
}
