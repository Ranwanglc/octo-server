// Package authz provides declarative, route-level authorization middleware for
// management endpoints (#366 Part 2). Instead of every /v1/manager handler
// hand-rolling `if err := c.CheckLoginRole...; err != nil { respondForbidden;
// return }`, the role requirement is mounted at route registration, next to the
// path:
//
//	auth := r.Group("/v1/manager", m.ctx.AuthMiddleware(r), authz.RequireSuperAdmin())
//	// or per-route:
//	auth.DELETE("/spaces/:space_id", authz.RequireSuperAdmin(), m.forceDisband)
//
// This keeps the privilege surface visible at the route table and removes the
// per-handler boilerplate. It is non-breaking: the tiers and the response are
// identical to the in-handler checks it replaces.
//
// # Ordering
//
// These middlewares read the per-request resolved role off the request context
// (c.GetLoginRole, populated by AuthMiddleware → CacheTokenParser, role-resolver
// aware since #364). They MUST be mounted AFTER AuthMiddleware — exactly like
// SharedUIDRateLimiter. Mounted before it, the role is empty and the middleware
// fails closed (always denies): safe, but a broken route.
//
// # Anti-enumeration
//
// Denial always returns the single generic errcode.ErrSharedForbidden via the
// legacy-compatible facade (wire 400, real 403 in error.http_status, identical
// to the existing space/report in-handler checks). The required tier is never
// leaked to the caller — an `admin` hitting a superAdmin-only route gets the
// same 403 as a normal user, so the route's privilege level cannot be probed.
package authz

import (
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"

	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
)

// roleCheck is the wkhttp.Context role assertion a tier delegates to. Both
// CheckLoginRole and CheckLoginRoleIsSuperAdmin match this shape (method
// expressions), so the tiers differ only by which one they pass in.
type roleCheck func(*wkhttp.Context) error

// requireRole builds a middleware that enforces check before the handler runs.
// On failure it writes the generic forbidden envelope and aborts the chain so
// the handler is never reached; on success it calls Next().
func requireRole(check roleCheck) wkhttp.HandlerFunc {
	return func(c *wkhttp.Context) {
		if err := check(c); err != nil {
			// Single generic code regardless of tier (anti-enumeration); the
			// specific role gap stays out of the response. Abort is required
			// because ResponseErrorL only writes the body, it does not stop the
			// chain (see pkg/httperr ResponseErrorL doc).
			httperr.ResponseErrorL(c, errcode.ErrSharedForbidden, nil, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireAdmin gates a route to the admin ∪ superAdmin tier (read / low-risk
// management endpoints). Mount AFTER AuthMiddleware.
func RequireAdmin() wkhttp.HandlerFunc {
	return requireRole((*wkhttp.Context).CheckLoginRole)
}

// RequireSuperAdmin gates a route to superAdmin only (cross-space destructive /
// supply-chain-sensitive writes). Mount AFTER AuthMiddleware.
func RequireSuperAdmin() wkhttp.HandlerFunc {
	return requireRole((*wkhttp.Context).CheckLoginRoleIsSuperAdmin)
}
