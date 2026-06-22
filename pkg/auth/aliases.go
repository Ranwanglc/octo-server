// Package auth is now a Deprecated alias shim. The canonical home for the
// token codec and CacheTokenParser is
// [github.com/Mininglamp-OSS/octo-server/modules/auth] — see its package doc
// for the dependency-direction invariant and the longer-term plan.
//
// This package is kept only so the six existing call sites (main.go,
// modules/{group,message,user,qrcode}/api.go, modules/user/api_manager.go)
// can be migrated incrementally and so out-of-tree forks have a deprecation
// window. Six months after this shim was introduced it WILL be removed; new
// code must import modules/auth directly.
//
// Deprecated: import
// "github.com/Mininglamp-OSS/octo-server/modules/auth" instead.
package auth

import (
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

// Encode is a function-variable alias preserving the exported call signature.
// `auth.Encode(info)` continues to work for all existing callers without
// source edits during the migration window.
//
// Deprecated: use [modulesauth.Encode].
var Encode = modulesauth.Encode

// Decode is a function-variable alias mirroring [Encode].
//
// Deprecated: use [modulesauth.Decode].
var Decode = modulesauth.Decode

// WithLanguageResolver is a function-variable alias for
// [modulesauth.WithLanguageResolver].
//
// Deprecated: use [modulesauth.WithLanguageResolver].
var WithLanguageResolver = modulesauth.WithLanguageResolver

// WithRoleResolver is a function-variable alias for
// [modulesauth.WithRoleResolver].
//
// Deprecated: use [modulesauth.WithRoleResolver].
var WithRoleResolver = modulesauth.WithRoleResolver

// NewCacheTokenParser is a function-variable alias for
// [modulesauth.NewCacheTokenParser]; the variadic signature is preserved so
// existing callers like `NewCacheTokenParser(c, prefix,
// WithLanguageResolver(r), WithRoleResolver(rr))` keep compiling unchanged.
//
// Deprecated: use [modulesauth.NewCacheTokenParser].
var NewCacheTokenParser = modulesauth.NewCacheTokenParser
