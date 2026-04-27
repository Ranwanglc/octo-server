package oidc

import (
	"embed"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
)

//go:embed sql
var sqlFS embed.FS

func init() {
	register.AddModule(func(ctx interface{}) register.Module {
		x := ctx.(*config.Context)
		o := New(x)
		return register.Module{
			Name: "oidc",
			SetupAPI: func() register.APIRouter {
				return o
			},
			SQLDir: register.NewSQLFS(sqlFS),
			// Stop 在 graceful shutdown 时关闭 redisStateStore 自有连接池;
			// dmwork-lib 共享 Redis 连接由 framework 关,无需在此处理。
			Stop: o.Close,
		}
	})
}
