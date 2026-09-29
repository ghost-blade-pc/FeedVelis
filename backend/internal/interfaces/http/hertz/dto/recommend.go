package dto

type ArticleRecommendItem struct {
	ArticleItem
	RecommendationReason string `json:"recommendation_reason"`
}

type ArticleRecommendPage struct {
	Items      []ArticleRecommendItem `json:"items"`
	NextCursor *string                `json:"next_cursor"`
	HasMore    bool                   `json:"has_more"`
	Mode       string                 `json:"mode"`
	Degraded   bool                   `json:"degraded"`
}
