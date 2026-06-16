package messages_search

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-server/modules/message"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"github.com/Mininglamp-OSS/octo-server/modules/user"
	spacepkg "github.com/Mininglamp-OSS/octo-server/pkg/space"
	appwkhttp "github.com/Mininglamp-OSS/octo-server/pkg/wkhttp"
)

// Handler wires the four /v1/messages/_search* endpoints. New is invoked from
// 1module.go via the standard register.AddModule entry point.
type Handler struct {
	ctx *config.Context
	log.Log
	cfg            SearchConfig
	userService    user.IService
	groupService   group.IService
	messageService message.IService
	threadService  thread.IService
	// visibility is the post-filter probe used by the /_search* hot path
	// (see visibility.go::filterVisible). Defined as an interface so tests
	// can stub the four signals directly without standing up a real
	// message.IService — message.IService exposes its responses through
	// types unexported from modules/message, which a test fake outside
	// that package cannot name.
	visibility visibilityProbe

	limiter *uidLimiter
	cache   *senderCache
}

// New constructs the Handler. ES client setup is deferred to first request so
// that a missing OS dependency does not prevent the rest of the server from
// booting (the request layer will surface UPSTREAM_UNAVAILABLE instead).
func New(ctx *config.Context) *Handler {
	cfg := loadConfig()
	msgSvc := message.NewService(ctx)
	h := &Handler{
		ctx:            ctx,
		Log:            log.NewTLog("messages_search"),
		cfg:            cfg,
		userService:    user.NewService(ctx),
		groupService:   group.NewService(ctx),
		messageService: msgSvc,
		threadService:  thread.NewService(ctx),
		visibility:     newMessageVisibilityProbe(msgSvc),
		limiter:        newUIDLimiter(cfg.RateLimit.QPS, cfg.RateLimit.Burst),
		cache:          newSenderCache(senderCacheCapacity, senderCacheTTL),
	}
	if cfg.CursorHMAC == "" {
		// The fallback key in cursor.go is a published constant, so cursors
		// are forgeable. Tolerable (the cursor carries no authorization data
		// and access is gated server-side) but every real deployment should
		// set its own key — make the misconfiguration loud instead of silent.
		h.Warn("OCTO_SEARCH_CURSOR_HMAC is not set; falling back to the " +
			"built-in default cursor signing key. Set a per-deployment " +
			"secret in production.")
	}
	if cfg.OSInsecureSkipVerify {
		h.Warn("OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY=true; OpenSearch TLS " +
			"certificate verification is DISABLED. Use only in dev/test " +
			"environments with self-signed certificates.")
	}
	return h
}

// Route mounts the four endpoints under /v1/messages with the standard
// auth/space/uid-limit chain plus the per-user search rate limiter and the
// audit middleware (PRM-02). Individual handlers are wired in their own
// search_*.go files via the registerHandler helper.
func (h *Handler) Route(r *wkhttp.WKHttp) {
	g := r.Group("/v1/messages",
		h.ctx.AuthMiddleware(r),
		appwkhttp.SharedUIDRateLimiter(r, h.ctx),
		spacepkg.SpaceMiddleware(h.ctx),
		h.searchRateLimiter(),
		h.auditMiddleware(),
	)
	for _, mount := range routeMounters {
		mount(h, g)
	}
}

// routeMounters is populated by each search_*.go file's init() so handlers
// can be added in independent commits without churning api.go.
var routeMounters []func(*Handler, *wkhttp.RouterGroup)

func registerRoute(mount func(*Handler, *wkhttp.RouterGroup)) {
	routeMounters = append(routeMounters, mount)
}
