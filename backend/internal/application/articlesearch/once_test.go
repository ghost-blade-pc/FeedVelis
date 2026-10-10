package articlesearch

import (
	"context"
	"errors"
	"testing"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type onceIndex struct {
	latest    string
	closed    string
	fail      bool
	waitClose bool
	cancel    context.CancelFunc
}

func (i *onceIndex) CreatePIT(context.Context, time.Duration) (string, error) { return "initial", nil }
func (i *onceIndex) Search(ctx context.Context, _ IndexRequest) (CandidateBatch, error) {
	i.latest = "latest"
	if i.cancel != nil {
		i.cancel()
		return CandidateBatch{PITID: i.latest}, ctx.Err()
	}
	if i.fail {
		return CandidateBatch{PITID: i.latest}, errors.New("失败")
	}
	return CandidateBatch{PITID: i.latest, Candidates: []Candidate{{ArticleID: 1}, {ArticleID: 2}}, Exhausted: true}, nil
}
func (i *onceIndex) ClosePIT(ctx context.Context, id string) error {
	i.closed = id
	if ctx.Err() != nil {
		return errors.New("清理继承了取消")
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 250*time.Millisecond {
		return errors.New("清理预算无效")
	}
	if i.waitClose {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func TestSearchOnceClosesLatestWithoutCursorAndKeepsRevision(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancel", "cleanup_timeout"} {
		t.Run(mode, func(t *testing.T) {
			index := &onceIndex{fail: mode == "failure", waitClose: mode == "cleanup_timeout"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				index.cancel = cancel
			}
			reader := &currentFake{items: map[int64]CurrentArticle{1: {Item: articleDomain.ListItem{ID: 1, Title: "当前"}, Identity: VectorIdentity{RevisionID: 11}}, 2: {Item: articleDomain.ListItem{ID: 2}, Identity: VectorIdentity{RevisionID: 12}}}}
			codec, err := NewCursorCodec(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			s := NewService(index, reader, codec, nil, Config{PITKeepAlive: time.Minute, CandidateBatchSize: 10, MaxCandidatesPerRequest: 20}).WithHybrid(nil, reader)
			started := time.Now()
			page, err := s.SearchOnce(ctx, Request{Q: "go", Limit: 1})
			if index.closed != "latest" {
				t.Fatalf("未清理最新PIT: %s", index.closed)
			}
			if time.Since(started) > 500*time.Millisecond {
				t.Fatal("清理超出有界预算")
			}
			if mode == "failure" || mode == "cancel" {
				if err == nil {
					t.Fatal("故障不应伪装成功")
				}
				return
			}
			if err != nil || page.NextCursor != nil || !page.HasMore || len(page.Items) != 1 || page.Items[0].RevisionID != 11 {
				t.Fatalf("一次搜索: %+v %v", page, err)
			}
		})
	}
}

// KNN 即使返回错误或非法候选，也可能轮换 PIT；只能丢弃候选，不能丢弃清理身份。
type rotatedHybridIndex struct {
	*hybridIndex
	closedID string
	invalid  bool
}

func (i *rotatedHybridIndex) SearchKNN(context.Context, KNNRequest) (CandidateBatch, error) {
	if i.invalid {
		return CandidateBatch{PITID: "rotated", Candidates: []Candidate{{ArticleID: -1}}}, nil
	}
	return CandidateBatch{PITID: "rotated"}, errors.New("KNN失败")
}
func (i *rotatedHybridIndex) ClosePIT(ctx context.Context, id string) error {
	i.closedID = id
	return ctx.Err()
}
func TestSearchOnceHybridFailureClosesRotatedPIT(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		s, base, _, _ := hybridFixture(t)
		index := &rotatedHybridIndex{hybridIndex: base, invalid: invalid}
		s.index = index
		page, err := s.SearchOnce(context.Background(), Request{Q: "go", Limit: 1})
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != 1 || page.NextCursor != nil || index.closedID != "rotated" {
			t.Fatalf("KNN降级或清理失败: %+v %v closed=%s", page, err, index.closedID)
		}
	}
}
