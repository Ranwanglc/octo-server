package auth

import (
	"context"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/redis"
	rd "github.com/go-redis/redis"
	"go.uber.org/zap"
)

func init() {
	register.AddModule(func(ctx interface{}) register.Module {
		x := ctx.(*config.Context)
		svc := NewService(x)
		log, _ := zap.NewProduction()
		api := NewAPI(svc, log)
		mod := &moduleAPI{ctx: x, api: api}
		return register.Module{
			Name:     "auth",
			SetupAPI: func() register.APIRouter { return mod },
		}
	})
}

// moduleAPI satisfies the register.APIRouter interface so the verify
// routes register at module boot. The Route hook receives the
// WKHttp router; we mount the verify endpoints here instead of in
// modules/user — the whole point of Stage A's modules/auth submodule.
type moduleAPI struct {
	ctx *config.Context
	api *API
}

// Route registers the verify endpoints with the same per-IP rate limit
// (StrictIPRateLimitMiddleware "verify" tag) the legacy modules/user
// route registration used (see legacy modules/user/api.go:202
// verifyLimit). Sharing the tag keeps the Redis bucket namespace
// identical so existing operator-side rate-limit dashboards and
// runbooks continue to apply.
func (m *moduleAPI) Route(r *wkhttp.WKHttp) {
	rlCtx := context.Background()
	rlRedis := rd.NewClient(redis.MustBuildOptions(m.ctx.GetConfig(), func(o *rd.Options) {
		o.MaxRetries = 1
		o.PoolSize = 10
	}))
	verifyLimit := r.StrictIPRateLimitMiddleware(rlCtx, rlRedis, "verify", 1000.0/60, 100)

	v := r.Group("/v1")
	{
		v.POST("/auth/verify", verifyLimit, m.api.verifyUserHTTP)
		v.POST("/auth/verify-bot", verifyLimit, m.api.verifyBotHTTP)
	}
}

