package space

import (
	"embed"
	"sync"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
)

//go:embed sql
var sqlFS embed.FS

//go:embed swagger/api.yaml
var swaggerContent string

func init() {
	// 两个子模块注册的 factory 都依赖同一个 *Space 实例。
	// 用 sync.Once 初始化，既能防止 factory 并发执行时出现 data race，
	// 也不依赖 register.AddModule 的执行顺序假设。
	var (
		sharedAPI  *Space
		sharedOnce sync.Once
	)
	ensureAPI := func(ctx interface{}) *Space {
		sharedOnce.Do(func() {
			sharedAPI = New(ctx.(*config.Context))
		})
		return sharedAPI
	}

	register.AddModule(func(ctx interface{}) register.Module {
		api := ensureAPI(ctx)
		return register.Module{
			Name: "space",
			SetupAPI: func() register.APIRouter {
				return api
			},
			SQLDir:  register.NewSQLFS(sqlFS),
			Swagger: swaggerContent,
		}
	})

	register.AddModule(func(ctx interface{}) register.Module {
		api := ensureAPI(ctx)
		return register.Module{
			Name: "space_manager",
			SetupAPI: func() register.APIRouter {
				return NewManager(ctx.(*config.Context), api)
			},
		}
	})
}
