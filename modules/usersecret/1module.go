package usersecret

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
		a := New(x)
		return register.Module{
			Name: "usersecret",
			SetupAPI: func() register.APIRouter {
				return a
			},
			SQLDir: register.NewSQLFS(sqlFS),
		}
	})
}
