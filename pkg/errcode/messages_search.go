package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.messages_search.* — modules/messages_search business error codes.
//
// These codes back the R2 12-item enum shipped to API clients (per the
// docs/messages-search/octo-server-search-dev.md §8 mapping table). The R2 wire
// codes are surfaced through the i18n localized error envelope and map to the
// HTTPStatus values declared here (renderer keeps wire status pinned to 400 for
// legacy compatibility while exposing the real status in error.http_status).
var (
	// VALIDATION_ERROR — bad request body / cursor / filter.
	ErrMessagesSearchValidationFailed = register(codes.Code{
		ID:             "err.server.messages_search.validation_failed",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid search request.",
		SafeDetailKeys: []string{"field", "reason", "max_length"},
	})

	// UPSTREAM_UNAVAILABLE — OS network / timeout / 5xx.
	ErrMessagesSearchUpstreamUnavailable = register(codes.Code{
		ID:             "err.server.messages_search.upstream_unavailable",
		HTTPStatus:     http.StatusServiceUnavailable,
		DefaultMessage: "Search service is temporarily unavailable.",
		Internal:       true,
	})

	// INTERNAL_ERROR — OS 4xx (DSL bug) / unexpected.
	ErrMessagesSearchInternal = register(codes.Code{
		ID:             "err.server.messages_search.internal",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Internal search error.",
		Internal:       true,
	})

	// RATE_LIMITED — per-loginUID 5 QPS / 20 burst exceeded.
	ErrMessagesSearchRateLimited = register(codes.Code{
		ID:             "err.server.messages_search.rate_limited",
		HTTPStatus:     http.StatusTooManyRequests,
		DefaultMessage: "Search rate limit exceeded.",
		SafeDetailKeys: []string{"retry_after"},
	})

	// NOT_FOUND — channel not visible / Space rejection.
	ErrMessagesSearchNotFound = register(codes.Code{
		ID:             "err.server.messages_search.not_found",
		HTTPStatus:     http.StatusNotFound,
		DefaultMessage: "Channel or resource not found for search.",
		SafeDetailKeys: []string{"resource"},
	})
)
