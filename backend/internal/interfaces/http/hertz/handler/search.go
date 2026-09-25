package handler

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/dto"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/middleware"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/interfaces/http/hertz/presenter"
)

type Search struct{ service searchApp.Searcher }

func NewSearch(service searchApp.Searcher) *Search {
	if service == nil {
		service = searchApp.UnavailableService{}
	}
	return &Search{service: service}
}

func (h *Search) Articles(ctx context.Context, c *app.RequestContext) {
	args := c.Request.URI().QueryArgs()
	request := searchApp.Request{Q: c.Query("q"), Cursor: c.Query("cursor")}
	if args.Has("keyword") {
		value := c.Query("keyword")
		request.Keyword = &value
	}
	if args.Has("topic") {
		value := c.Query("topic")
		request.Topic = &value
	}
	if args.Has("source_id") {
		value, err := strconv.ParseInt(c.Query("source_id"), 10, 64)
		if err != nil {
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "source_id 必须是正整数", middleware.RequestIDFrom(c))
			return
		}
		request.SourceID = &value
	}
	if args.Has("limit") {
		value, err := strconv.Atoi(c.Query("limit"))
		if err != nil {
			presenter.WriteError(c, consts.StatusBadRequest, presenter.CodeValidationFailed, "limit 必须是整数", middleware.RequestIDFrom(c))
			return
		}
		request.Limit = value
	}
	page, err := h.service.Search(ctx, request)
	if err != nil {
		presenter.WriteMapping(c, presenter.MapSearchError(err), middleware.RequestIDFrom(c))
		return
	}
	items := make([]dto.ArticleItem, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, toArticleItem(item))
	}
	c.JSON(consts.StatusOK, dto.ArticleSearchPage{Items: items, NextCursor: page.NextCursor, HasMore: page.HasMore})
}
