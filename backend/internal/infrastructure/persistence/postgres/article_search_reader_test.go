package postgres

import (
	"context"
	"testing"
)

func TestListPublishedByIDsEmptyInputDoesNotQuery(t *testing.T) {
	repository := &ArticleRepository{}
	items, err := repository.ListPublishedByIDs(context.Background(), nil)
	if err != nil || len(items) != 0 {
		t.Fatalf("空输入应直接返回空切片: items=%v err=%v", items, err)
	}
}
