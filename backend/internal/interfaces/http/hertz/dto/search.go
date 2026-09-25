package dto

type ArticleSearchPage struct {
	Items      []ArticleItem `json:"items"`
	NextCursor *string       `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
}
