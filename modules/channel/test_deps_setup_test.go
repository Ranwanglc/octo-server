package channel

// test_deps_setup_test.go ensures every module whose migrations are
// referenced by transitively-pulled migrations registers itself via its
// init(). Without these blank imports testutil.NewTestServer fails the
// space module's 20260308000002_space_legacy01.sql migration with
// "Table 'test.robot' doesn't exist", because space (transitively
// imported via group → user → space) JOINs the robot table — but the
// robot package, where that table is created, is not pulled in by this
// test package's production code.
//
// Mirrors the pattern used in modules/botfather/api_bot_group_test.go.

import (
	"github.com/Mininglamp-OSS/octo-lib/server"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"

	_ "github.com/Mininglamp-OSS/octo-server/modules/robot"
)

// wireI18nRendererForChannelTest wires the i18n error renderer onto the route
// returned by testutil.NewTestServer, mirroring what main.go does at boot (and
// what modules/group/message/category do in their full-server tests).
// Post-migration, modules/channel handlers respond via httperr.ResponseErrorL →
// c.RenderError; without a renderer wired the route falls back to octo-lib's
// legacy {msg,status} envelope (English DefaultMessage, no error.code field),
// so assertions on the localized envelope — including error.code — would fail.
// testutil.NewTestServer lives in octo-lib and is intentionally not touched here.
func wireI18nRendererForChannelTest(s *server.Server) {
	s.GetRoute().SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
}
