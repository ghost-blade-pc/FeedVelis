package articlefeedback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	feedback "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/articlefeedback"
)

type fakeProfileRepository struct {
	excluded []int64
	samples  []Sample
}

func (r fakeProfileRepository) Excluded(context.Context, string, []int64, time.Time) ([]int64, error) {
	return r.excluded, nil
}
func (r fakeProfileRepository) Samples(context.Context, string, time.Time) ([]Sample, error) {
	return r.samples, nil
}

func TestProfileBoundsAndNegativePriority(t *testing.T) {
	source := int64(3)
	repo := fakeProfileRepository{excluded: []int64{1}, samples: []Sample{
		{ArticleID: 1, ReadDays: 30, Favorited: true, NotInterested: true, Keywords: []string{"go", "go"}, Topics: []string{"go", "go"}, SourceID: &source},
		{ArticleID: 2, ReadDays: 30, Favorited: true, Keywords: []string{"go"}, Topics: []string{"go"}, SourceID: &source},
		{ArticleID: 3, ReadDays: 1, Topics: []string{""}},
	}}
	service := NewProfileService(repo, fixedClock{time.Now()})
	profile, err := service.Profile(context.Background(), "72000000-0000-0000-0000-000000000001", []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Excluded[1] != recommendation.NotInterestedArticle || len(profile.Excluded) != 1 {
		t.Fatalf("排除原因错误: %+v", profile.Excluded)
	}
	if item := profile.Topics["go"]; item.Weight != 5 || item.PositiveArticles != 1 || item.NegativeArticles != 1 {
		t.Fatalf("主题证据错误: %+v", item)
	}
	if item := profile.Keywords["go"]; item.Weight != 5 || item.PositiveWeight != 6 || item.NegativeWeight != 1 || item.PositiveArticles != 1 || item.NegativeArticles != 1 {
		t.Fatalf("关键词证据错误: %+v", item)
	}
	if item := profile.Sources[source]; item.Weight != 5 {
		t.Fatalf("来源证据错误: %+v", item)
	}
	if len(profile.Topics) != 1 {
		t.Fatalf("缺失主题生成了证据: %+v", profile.Topics)
	}
	for _, ids := range [][]int64{{0}, {1, 1}, make([]int64, 201)} {
		if _, err := service.Profile(context.Background(), "72000000-0000-0000-0000-000000000001", ids); !errors.Is(err, feedback.ErrInvalidInput) {
			t.Fatalf("非法候选未拒绝: %v", err)
		}
	}
}

func TestProfileEvidenceClamps(t *testing.T) {
	samples := make([]Sample, 501)
	for i := range samples {
		samples[i] = Sample{ArticleID: int64(i + 1), ReadDays: 99, Favorited: true, Keywords: []string{"go"}, Topics: []string{"go"}}
	}
	service := NewProfileService(fakeProfileRepository{excluded: []int64{999}, samples: samples}, fixedClock{time.Now()})
	profile, err := service.Profile(context.Background(), "72000000-0000-0000-0000-000000000001", []int64{999})
	if err != nil || profile.Topics["go"].Weight != 20 || profile.Keywords["go"].Weight != 20 || profile.Keywords["go"].PositiveArticles != 500 || profile.Excluded[999] != recommendation.NotInterestedArticle {
		t.Fatalf("证据上限错误: %+v %v", profile, err)
	}
}

func TestProfileEvidenceClampsAfterSumming(t *testing.T) {
	samples := make([]Sample, 50)
	for index := range samples {
		samples[index] = Sample{ArticleID: int64(index + 1), Keywords: []string{"go"}, Topics: []string{"go"}}
		if index < 25 {
			samples[index].ReadDays = 1
		} else {
			samples[index].NotInterested = true
		}
	}
	service := NewProfileService(fakeProfileRepository{samples: samples}, fixedClock{time.Now()})
	profile, err := service.Profile(context.Background(), "72000000-0000-0000-0000-000000000001", nil)
	if err != nil || profile.Topics["go"].Weight != 0 || profile.Keywords["go"].Weight != 0 || profile.Topics["go"].PositiveArticles != 25 || profile.Topics["go"].NegativeArticles != 25 {
		t.Fatalf("正负证据未先合计再限幅: %+v %v", profile, err)
	}
}

func TestProfileDuplicateSampleDoesNotIncreaseEvidence(t *testing.T) {
	sample := Sample{ArticleID: 1, ReadDays: 1, Keywords: []string{"go", "go"}}
	service := NewProfileService(fakeProfileRepository{samples: []Sample{sample, sample}}, fixedClock{time.Now()})
	profile, err := service.Profile(context.Background(), "72000000-0000-0000-0000-000000000001", nil)
	if err != nil || profile.Keywords["go"].Weight != 1 || profile.Keywords["go"].PositiveArticles != 1 {
		t.Fatalf("重复样本放大关键词证据: %+v %v", profile, err)
	}
}
