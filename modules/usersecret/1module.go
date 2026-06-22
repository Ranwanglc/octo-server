package usersecret

import (
	"embed"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/register"
	"github.com/Mininglamp-OSS/octo-server/modules/auth"
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
				// Register self as the APIKeyLookup provider for modules/auth's
				// verify-api-key handler (PR-A4). For now this lookup is a stub
				// (see lookup.go); wiring it through ensures the contract works
				// the moment real uk_ storage lands.
				auth.SetAPIKeyLookup(a)
				return a
			},
			SQLDir: register.NewSQLFS(sqlFS),
		}
	})
}
