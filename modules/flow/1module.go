// Package flow 实现 Octo Flow 编排引擎 Phase 1：
// 数据模型、DAG 执行引擎、基础节点（script / http / condition）、
// 触发器（webhook / cron / manual）、REST API。
package flow

import (
	"embed"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
)

//go:embed sql
var sqlFS embed.FS

func init() {
	register.AddModule(func(ctx interface{}) register.Module {
		api := New(ctx.(*config.Context))
		return register.Module{
			Name: "flow",
			SetupAPI: func() register.APIRouter {
				return api
			},
			SQLDir: register.NewSQLFS(sqlFS),
			Start: func() error {
				return api.Start()
			},
			Stop: func() error {
				return api.Stop()
			},
		}
	})
}
