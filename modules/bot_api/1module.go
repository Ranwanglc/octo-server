package bot_api

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
		return register.Module{
			Name: "bot_api",
			SetupAPI: func() register.APIRouter {
				ba := NewBotAPI(ctx.(*config.Context))
				// Register self as the BotLookup provider for modules/auth's
				// verify-bot handler. SetupAPI runs after all init()s, so
				// modules/auth's registry singleton is already initialised
				// when this call lands.
				auth.SetBotLookup(ba)
				return ba
			},
			SQLDir: register.NewSQLFS(sqlFS),
		}
	})
}
