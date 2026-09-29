package recommendation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

func TestRecommendCursorIdentityExpiryTamperAndVersion(t *testing.T) {
	codec, err := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := CursorState{Version: RankingVersion, Identity: IdentityBinding("user-a"), Mode: "personalized", ExpiresAt: now.Add(2 * time.Minute), Items: []FrozenItem{{ID: 1, Reason: "keyword_match"}}, Latest: &articleDomain.Cursor{SortAt: now, ArticleID: 10}}
	token, err := codec.Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := codec.Decode(token, "user-a", now)
	if err != nil || decoded.Items[0].ID != 1 || decoded.Latest.ArticleID != 10 {
		t.Fatalf("游标未恢复冻结计划: %+v %v", decoded, err)
	}
	for _, test := range []struct {
		token, user string
		at          time.Time
	}{
		{token, "user-b", now}, {token, "", now}, {token, "user-a", now.Add(3 * time.Minute)},
		{token + "x", "user-a", now}, {strings.Repeat("a", maxRecommendCursorBytes+1), "user-a", now},
	} {
		if _, err := codec.Decode(test.token, test.user, test.at); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("非法游标未拒绝: %v", err)
		}
	}
	state.Version = RankingVersion + 1
	if _, err := codec.Encode(state); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("未知版本未拒绝: %v", err)
	}
	state.Version = RankingVersion
	state.Items = make([]FrozenItem, 201)
	if _, err := codec.Encode(state); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("候选上限未拒绝: %v", err)
	}
}

func TestRecommendCursorWorstCaseTwoHundredSemanticCandidates(t *testing.T) {
	codec, _ := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Now().UTC()
	state := CursorState{Version: RankingVersion, Identity: IdentityBinding("user"), Mode: "personalized", ExpiresAt: now.Add(2 * time.Minute), VectorProfile: "embedding-v2"}
	for id := int64(1); id <= 200; id++ {
		generation := sha256.Sum256([]byte(fmt.Sprintf("generation:%d", id)))
		embedding := sha256.Sum256([]byte(fmt.Sprintf("embedding:%d", id)))
		state.Items = append(state.Items, FrozenItem{ID: id, Reason: "similar_content", Semantic: true, Identity: FrozenIdentity{
			RevisionID: id, GenerationID: fmt.Sprintf("%x-%x-%x-%x-%x", generation[:4], generation[4:6], generation[6:8], generation[8:10], generation[10:16]),
			EmbeddingID: fmt.Sprintf("%x-%x-%x-%x-%x", embedding[:4], embedding[4:6], embedding[6:8], embedding[8:10], embedding[10:16]),
		}})
	}
	token, err := codec.Encode(state)
	if err != nil || len(token) > 16*1024 {
		t.Fatalf("200 个语义候选游标不可编码: %d %v", len(token), err)
	}
	decoded, err := codec.Decode(token, "user", now)
	if err != nil || len(decoded.Items) != 200 {
		t.Fatalf("200 个语义候选游标不可解码: %v", err)
	}
}

func TestRecommendCursorRejectsSearchOrAnonymousCursor(t *testing.T) {
	codec, _ := NewCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	now := time.Now().UTC()
	state := CursorState{Version: RankingVersion, Identity: IdentityBinding(""), Mode: "cold_start", ExpiresAt: now.Add(time.Minute), LatestOn: true}
	token, err := codec.Encode(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode(token, "user-a", now); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("匿名游标被登录身份复用: %v", err)
	}
	if _, err := codec.Decode("2."+strings.TrimPrefix(token, "r1."), "", now); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("搜索游标被推荐接受: %v", err)
	}
}
