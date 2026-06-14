package errcode

import (
	"net/http"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

// err.server.conversation_ext.* — modules/conversation_ext business error codes
// (api.go, the /v1/follow API). DefaultMessage holds the en-US source (D4); the
// zh-CN runtime translation lives in pkg/i18n/locales/active.zh-CN.toml.
// Internal=true codes never surface their message on the wire — callers MUST log
// the underlying err with full context (zap.Error) before responding.
var (
	// ---- validation (400) ----------------------------------------------------

	// ErrConvExtRequestInvalid is the catch-all for missing/malformed request
	// input (BindJSON failure, empty space_id / peer_uid / group_no /
	// thread_channel_id, empty items, empty target_id, invalid target_type). The
	// offending field is surfaced via Details when the caller can identify it.
	ErrConvExtRequestInvalid = register(codes.Code{
		ID:             "err.server.conversation_ext.request_invalid",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Invalid request.",
		SafeDetailKeys: []string{"field"},
	})
	// ErrConvExtItemsTooMany covers an UpdateSort payload whose items array
	// exceeds the per-request cap. The cap is surfaced so the client can render a
	// localized hint without hard-coding the limit.
	ErrConvExtItemsTooMany = register(codes.Code{
		ID:             "err.server.conversation_ext.items_too_many",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Too many items.",
		SafeDetailKeys: []string{"max"},
	})
	// ErrConvExtDuplicateItem covers a duplicate (target_type, target_id) pair in
	// the UpdateSort items array. The offending pair is surfaced so the client can
	// pinpoint the duplicate.
	ErrConvExtDuplicateItem = register(codes.Code{
		ID:             "err.server.conversation_ext.duplicate_item",
		HTTPStatus:     http.StatusBadRequest,
		DefaultMessage: "Duplicate item in the request.",
		SafeDetailKeys: []string{"target_type", "target_id"},
	})

	// ---- permission / authorization (403) ------------------------------------

	// ErrConvExtFollowForbidden covers the follow authorization guards on
	// group/thread follow (caller is not a member / the target is not visible in
	// the request space). No enumeration: the specific reason is logged, the
	// client sees one generic "not allowed to follow".
	ErrConvExtFollowForbidden = register(codes.Code{
		ID:             "err.server.conversation_ext.follow_forbidden",
		HTTPStatus:     http.StatusForbidden,
		DefaultMessage: "You are not allowed to follow this target.",
	})
	// ErrConvExtCategoryForbidden covers a FollowDM whose category_id is not owned
	// by the caller or has been deleted (ErrDMCategoryForbidden). Surfaced as a
	// distinct business error so the client knows to refresh its category list
	// rather than retrying blindly.
	ErrConvExtCategoryForbidden = register(codes.Code{
		ID:             "err.server.conversation_ext.category_forbidden",
		HTTPStatus:     http.StatusForbidden,
		DefaultMessage: "The category does not exist or does not belong to you.",
	})

	// ---- business conflicts the client must recover from (404 / 409) ---------

	// ErrConvExtSortTargetNotFound covers an UpdateSort whose items reference a
	// target the user no longer follows (ErrSortTargetNotFound). It is a
	// swagger-promised, client-handleable error: the client recovers by
	// re-fetching the follow list and retrying.
	ErrConvExtSortTargetNotFound = register(codes.Code{
		ID:             "err.server.conversation_ext.sort_target_not_found",
		HTTPStatus:     http.StatusNotFound,
		DefaultMessage: "One or more sort targets were not found.",
	})
	// ErrConvExtVersionConflict covers the optimistic-concurrency (CAS) failure on
	// UpdateSort (ErrVersionConflict): the client's follow_version is stale. The
	// client recovers by re-fetching the follow list and retrying.
	ErrConvExtVersionConflict = register(codes.Code{
		ID:             "err.server.conversation_ext.version_conflict",
		HTTPStatus:     http.StatusConflict,
		DefaultMessage: "The follow list changed; please retry.",
	})

	// ---- internal (500, Internal=true) ---------------------------------------

	// ErrConvExtFollowFailed covers follow-path failures (FollowDM / FollowChannel
	// / FollowThread service or store errors). Log the underlying err before
	// responding.
	ErrConvExtFollowFailed = register(codes.Code{
		ID:             "err.server.conversation_ext.follow_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to follow the target.",
		Internal:       true,
	})
	// ErrConvExtUnfollowFailed covers unfollow-path failures (UnfollowDM /
	// UnfollowChannel / UnfollowThread service or store errors). Log the
	// underlying err before responding.
	ErrConvExtUnfollowFailed = register(codes.Code{
		ID:             "err.server.conversation_ext.unfollow_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to unfollow the target.",
		Internal:       true,
	})
	// ErrConvExtSortUpdateFailed covers UpdateSort write-path failures
	// (default-followed group materialization, the CAS UpdateSort store error).
	// Log the underlying err before responding.
	ErrConvExtSortUpdateFailed = register(codes.Code{
		ID:             "err.server.conversation_ext.sort_update_failed",
		HTTPStatus:     http.StatusInternalServerError,
		DefaultMessage: "Failed to update the sort order.",
		Internal:       true,
	})
)
