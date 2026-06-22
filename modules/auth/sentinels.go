package auth

import "errors"

// ErrAppBotUnpublished signals that an App Bot exists in the DB but its
// status is not 1 (published). It is the one extra sentinel that
// [BotLookup.LookupAppBot] is allowed to return alongside (nil, nil) and
// real infrastructure errors.
//
// PR-A3's verify-bot handler maps this sentinel to the
// `AUTH_BOT_UNAVAILABLE` (503) error code per plan §4.2 — different from
// `AUTH_TOKEN_INVALID` (401, "no such bot") because the client distinction
// matters: "bot exists but is currently unpublished" is a transient state
// the bot owner can fix, while "no such token" is a credential problem.
var ErrAppBotUnpublished = errors.New("auth: app bot is unpublished")
