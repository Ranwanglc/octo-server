package messages_search

// CursorList is the R1 success envelope for cursor-paginated list endpoints.
//
// Matches the contract in docs/messages-search/api-spec-v2-server-to-frontend.html
// (v4.2): { "data": [...], "pagination": { "has_more": bool, "next_cursor": str } }.
//
// TODO: switch to a generic envelope.CursorList[T] once octo-lib publishes one.
type CursorList struct {
	Data       any        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// Pagination carries the server's opinion on whether more results exist plus
// the opaque cursor for fetching the next page. NextCursor is always emitted
// (even as "") because spec v4.2 §1.4 requires the field on the wire so
// clients can do a literal `pagination.next_cursor === ""` check.
type Pagination struct {
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor"`
}

// envelope wraps a typed slice into the CursorList shape.
func envelope[T any](items []T, hasMore bool, nextCursor string) CursorList {
	if items == nil {
		items = []T{}
	}
	return CursorList{
		Data:       items,
		Pagination: Pagination{HasMore: hasMore, NextCursor: nextCursor},
	}
}
