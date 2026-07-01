package robot

import (
	"os"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newSqlmockRobotDB 返回一个 dbr session 由 sqlmock 支撑的 *robotDB，用于在不起真 DB
// 的前提下验证 ensureSummaryBotToken 的 SQL 边界行为（OCT-5 / 方案 D）。closer 须 defer。
func newSqlmockRobotDB(t *testing.T) (*robotDB, sqlmock.Sqlmock, func()) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	conn := &dbr.Connection{DB: rawDB, EventReceiver: &dbr.NullEventReceiver{}, Dialect: dialect.MySQL}
	session := conn.NewSession(nil)
	return &robotDB{session: session}, mock, func() { _ = rawDB.Close() }
}

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
// 并在 startup 调用，应在 code review 时被拒（运行时不得用 stale env 覆盖已由
// ensureSummaryBotToken 生成/写入的 bot_token；迁移不写死 token）。这里只做一个轻量 env 无副作用断言：设置 env 后调用 SummaryBotUID()
// 仍是常量，且不依赖任何运行时自举。
func TestSummaryBot_NoEnvBootstrapSymbols(t *testing.T) {
	// 确保 env 不会以任何方式改变固定 UID 行为（间接验证无 env 自举）。
	_ = os.Setenv("SUMMARY_BOT_UID", "")
	defer os.Unsetenv("SUMMARY_BOT_UID")
	assert.Equal(t, "summary_notification", SummaryBotUID())
}

// TestGenSummaryBotToken_StrongRandom 验证自动生成的 token：非空、带 bf_ 前缀、
// 長度符合 32 字节 hex，且两次生成不同（强随机）。
func TestGenSummaryBotToken_StrongRandom(t *testing.T) {
	t1, err := genSummaryBotToken()
	require.NoError(t, err)
	assert.NotEmpty(t, t1, "生成的 token 不得为空")
	assert.True(t, len(t1) > len(summaryBotTokenPrefix), "token 应长于前缀本身")
	assert.Equal(t, summaryBotTokenPrefix, t1[:len(summaryBotTokenPrefix)], "token 应带 bf_ 前缀")
	assert.Equal(t, len(summaryBotTokenPrefix)+summaryBotTokenBytes*2, len(t1), "bf_ + 32字节的hex = 3+64")

	t2, err := genSummaryBotToken()
	require.NoError(t, err)
	assert.NotEqual(t, t1, t2, "两次生成的 token 应不同（强随机）")
}

// TestEnsureSummaryBotToken_GeneratesWhenEmpty 验证：bot_token 为空时，ensureSummaryBotToken
// 生成一个非空 token 并用带空值 WHERE 条件的 UPDATE 写回（OCT-5 / 方案 D 核心路径）。
func TestEnsureSummaryBotToken_GeneratesWhenEmpty(t *testing.T) {
	d, mock, closer := newSqlmockRobotDB(t)
	defer closer()
	rb := &Robot{db: *d, Log: log.NewTLog("RobotTest")}

	// 1. 查 token 返回空串。
	mock.ExpectQuery("SELECT IFNULL\\(bot_token,''\\) FROM robot WHERE \\(robot_id=").
		WillReturnRows(sqlmock.NewRows([]string{"bot_token"}).AddRow(""))
	// 2. 带空值 WHERE 条件的 UPDATE，影响 1 行（dbr MySQL dialect 内联参数）。
	mock.ExpectExec("UPDATE `robot` SET `bot_token` = .*WHERE .*bot_token='' OR bot_token IS NULL").
		WillReturnResult(sqlmock.NewResult(0, 1))

	rb.ensureSummaryBotToken()
	require.NoError(t, mock.ExpectationsWereMet(), "空 token 时应发生 SELECT + 写回 UPDATE")
}

// TestEnsureSummaryBotToken_SkipsWhenPresent 验证幂等性：已有非空 token 时不生成、
// 不覆盖（不发 UPDATE）。sqlmock 未声明 UPDATE，若发生则 ExpectationsWereMet 会报错。
func TestEnsureSummaryBotToken_SkipsWhenPresent(t *testing.T) {
	d, mock, closer := newSqlmockRobotDB(t)
	defer closer()
	rb := &Robot{db: *d, Log: log.NewTLog("RobotTest")}

	mock.ExpectQuery("SELECT IFNULL\\(bot_token,''\\) FROM robot WHERE \\(robot_id=").
		WillReturnRows(sqlmock.NewRows([]string{"bot_token"}).AddRow("bf_existing_token"))
	// 故意不声明任何 ExpectExec：若 ensureSummaryBotToken 误发 UPDATE，sqlmock 会 fail。

	rb.ensureSummaryBotToken()
	require.NoError(t, mock.ExpectationsWereMet(), "已有非空 token 时不得发生任何写入")
}
