package robot

import (
	"os"
	"testing"

	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"github.com/stretchr/testify/assert"
)

// TestSummaryBotUID_FixedConstant_IgnoresEnv 是 PR#483 第二轮 🟡 MAJOR 的回归守卫。
//
// 固定常量化后 summary bot 完全由迁移拥有：SummaryBotUID() 必须始终返回固定常量
// summary_notification（= pkg/space.SummaryNotificationBotUID），**绝不读 env**。
// 旧实现读 SUMMARY_BOT_UID，会让 UID 随部署 env 漂移；这里通过设置一个不同的 env
// 值断言返回值不受影响。
func TestSummaryBotUID_FixedConstant_IgnoresEnv(t *testing.T) {
	t.Setenv("SUMMARY_BOT_UID", "some_other_uid_from_stale_env")
	t.Setenv("SUMMARY_BOT_TOKEN", "bf_stale_env_token_should_be_ignored")

	assert.Equal(t, pkgspace.SummaryNotificationBotUID, SummaryBotUID(),
		"SummaryBotUID() must return the fixed migration-owned constant, never an env-driven value")
	assert.Equal(t, "summary_notification", SummaryBotUID(),
		"the fixed summary bot UID constant must be summary_notification")
	// IsSystemBot 单一真源也必须认它。
	assert.True(t, pkgspace.IsSystemBot(SummaryBotUID()),
		"the fixed summary bot UID must be registered as a system bot")
}

// TestSummaryBot_NoEnvBootstrapSymbols 文档化：env 自举/reconcile 路径已被移除。
// 若有人重新引入 insertSummaryRobot / reconcileSummaryRobot / loadSummaryBotConfig
// 并在 startup 调用，应在 code review 时被拒（运行时不得用 stale env 覆盖迁移写死的
// 固定 bot_token）。这里只做一个轻量 env 无副作用断言：设置 env 后调用 SummaryBotUID()
// 仍是常量，且不依赖任何运行时自举。
func TestSummaryBot_NoEnvBootstrapSymbols(t *testing.T) {
	// 确保 env 不会以任何方式改变固定 UID 行为（间接验证无 env 自举）。
	_ = os.Setenv("SUMMARY_BOT_UID", "")
	defer os.Unsetenv("SUMMARY_BOT_UID")
	assert.Equal(t, "summary_notification", SummaryBotUID())
}
