// Package auth is now a Deprecated alias shim. The canonical home for the
// token codec and CacheTokenParser is
// [github.com/Mininglamp-OSS/octo-server/modules/auth] — see its package doc
// for the dependency-direction invariant and the longer-term plan.
//
// This package is kept only so the six existing call sites (main.go,
// modules/{group,message,user,qrcode}/api.go, modules/user/api_manager.go)
// can be migrated incrementally and so out-of-tree forks have a deprecation
// window.
//
// # Removal schedule
//
// This shim is scheduled for removal on or after 2026-12-22 (six months
// after the alias was introduced in PR-A1 / #429). The removal is tracked
// alongside the Stage A epic [octo-server#428] so anyone touching pkg/auth
// in the interim can see the deadline + see who owns the migration.
//
// What the removal entails:
//   - Delete pkg/auth/aliases.go and pkg/auth/aliases_test.go.
//   - The six callers listed above must by then have switched their imports
//     to "github.com/Mininglamp-OSS/octo-server/modules/auth".
//   - Out-of-tree forks that still import pkg/auth at removal time will
//     get a clean compile error pointing them at the canonical path
//     (the package itself disappears; no silent breakage).
//
// To migrate a caller today: change
//
//	import "github.com/Mininglamp-OSS/octo-server/pkg/auth"
//
// to
//
//	import "github.com/Mininglamp-OSS/octo-server/modules/auth"
//
// The exported surface is identical (types via Go `type =` aliases;
// sentinel errors re-exported by value so [errors.Is] still works;
// functions re-exported as wrapper funcs preserving variadic signatures).
// `gofmt -r` or a one-shot `goimports` pass over the importing file is
// enough — no further code edits are needed.
//
// Deprecated: import
// "github.com/Mininglamp-OSS/octo-server/modules/auth" instead. To be
// removed on or after 2026-12-22 — see [octo-server#428] for the
// migration tracker.
package auth

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/cache"

	modulesauth "github.com/Mininglamp-OSS/octo-server/modules/auth"
)

// TokenInfo is an alias for [modulesauth.TokenInfo].
//
// Deprecated: use [modulesauth.TokenInfo].
type TokenInfo = modulesauth.TokenInfo

// LanguageResolver is an alias for [modulesauth.LanguageResolver].
//
// Deprecated: use [modulesauth.LanguageResolver].
type LanguageResolver = modulesauth.LanguageResolver

// RoleResolver is an alias for [modulesauth.RoleResolver].
//
// Deprecated: use [modulesauth.RoleResolver].
type RoleResolver = modulesauth.RoleResolver

// CacheTokenParser is an alias for [modulesauth.CacheTokenParser].
//
// Deprecated: use [modulesauth.CacheTokenParser].
type CacheTokenParser = modulesauth.CacheTokenParser

// ParserOption is an alias for [modulesauth.ParserOption].
//
// Deprecated: use [modulesauth.ParserOption].
type ParserOption = modulesauth.ParserOption

// ErrEmptyToken re-exports [modulesauth.ErrEmptyToken] by value so
// `errors.Is(err, auth.ErrEmptyToken)` keeps matching errors produced by the
// canonical package.
//
// Deprecated: use [modulesauth.ErrEmptyToken].
var ErrEmptyToken = modulesauth.ErrEmptyToken

// ErrInvalidToken re-exports [modulesauth.ErrInvalidToken] by value; same
// sentinel-identity contract as [ErrEmptyToken].
//
// Deprecated: use [modulesauth.ErrInvalidToken].
var ErrInvalidToken = modulesauth.ErrInvalidToken

// Encode is a wrapper preserving the exported call signature for callers
// importing this shim. Forwarding to the canonical implementation keeps the
// function immutable (unlike a `var = ...` re-export, which would let an
// importer reassign the symbol package-globally).
//
// Deprecated: use [modulesauth.Encode].
func Encode(info TokenInfo) (string, error) {
	return modulesauth.Encode(info)
}

// Decode is a wrapper mirroring [Encode]; see Encode for the wrapper-vs-var
// rationale.
//
// Deprecated: use [modulesauth.Decode].
func Decode(raw string) (TokenInfo, error) {
	return modulesauth.Decode(raw)
}

// WithLanguageResolver is a wrapper for [modulesauth.WithLanguageResolver].
//
// Deprecated: use [modulesauth.WithLanguageResolver].
func WithLanguageResolver(r LanguageResolver) ParserOption {
	return modulesauth.WithLanguageResolver(r)
}

// WithRoleResolver is a wrapper for [modulesauth.WithRoleResolver].
//
// Deprecated: use [modulesauth.WithRoleResolver].
func WithRoleResolver(r RoleResolver) ParserOption {
	return modulesauth.WithRoleResolver(r)
}

// NewCacheTokenParser is a wrapper for [modulesauth.NewCacheTokenParser];
// the variadic signature is preserved so existing callers like
// `NewCacheTokenParser(c, prefix, WithLanguageResolver(r), WithRoleResolver(rr))`
// keep compiling unchanged.
//
// Deprecated: use [modulesauth.NewCacheTokenParser].
func NewCacheTokenParser(c cache.Cache, prefix string, opts ...ParserOption) *CacheTokenParser {
	return modulesauth.NewCacheTokenParser(c, prefix, opts...)
}
