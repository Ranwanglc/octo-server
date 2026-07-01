package robot

import (
	"crypto/rand"
	"encoding/hex"

	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"go.uber.org/zap"
)

// 总结助手（Summary Assistant）专属账号 —— 固定 UID 系统 bot，token 启动自生成。
//
// 设计要点（PR#483 / OCT-5 方案 D：共享 DB + 启动自动生成 token）：
//   - 「总结助手」是固定 UID 的系统 bot（summary_notification，单一真源 =
//     pkg/space.SummaryNotificationBotUID，同时登记在 SystemBots 中）。
//   - bot 的 user 行 / robot 行 由迁移
//     modules/robot/sql/20260629000001_summary_notification_bot.sql 插入；但
//     **bot_token 不写死在迁移里**（避免在公开仓库写死明文凭据），迁移里
//     bot_token 留空（''）。
//   - token 改为 server **首次启动时自动生成强随机值并写回 robot 表**（见
//     ensureSummaryBotToken，幂等 + 并发安全）；smart-summary（共享同一 IM 库）
//     在投递时 lazy SELECT 该 token 用于鉴权。运维零准备、源码零明文、每部署唯一。
//   - 运行时不再做 env 驱动的自举 / reconcile / token 改写（token 不依赖 env 状态）。
//
// 为什么不再用 env 自举 / 不再把 token 写死迁移：
//   早期方案在 startup 调 insertSummaryRobot()，读 SUMMARY_BOT_UID / SUMMARY_BOT_TOKEN，
//   若已存在则 reconcileSummaryRobot() 以 env 为准纠偏 bot_token——会用 stale env
//   覆盖已写入的 token，破坏鉴权。后来曾改为「迁移写死固定 token」，但那会在
//   公开仓库暴露明文凭据。最终定稿为方案 D：迁移只插身份行、token 留空，
//   由 server 启动自动生成写库，summary 共享 DB lazy 读取。
//
// 仍保留 SummaryBotUID()：被 bot_api 的 ensureFriend 端点引用做 UID 白名单门控
// （仅放行这一个 UID），以及其它代码引用固定常量。

// SummaryBotUID 返回「总结助手」的固定常量 UID。
//
// 单一真源 = pkg/space.SummaryNotificationBotUID（同时在 SystemBots 中）。供
// bot_api 的 ensureFriend 端点做 UID 白名单门控（仅放行这一个 UID）。不读任何 env
// —— 避免部署间 UID 漂移；bot 身份（UID）由迁移拥有，token 由 server 启动自生成。
func SummaryBotUID() string {
	return pkgspace.SummaryNotificationBotUID
}

// summaryBotTokenBytes 是自动生成的 bot_token 的随机字节数（32 字节 → 64 hex 字符）。
const summaryBotTokenBytes = 32

// summaryBotTokenPrefix 与历史固定 token 一致（bf_），命中 bot_api/auth.go 的 User Bot 分支。
const summaryBotTokenPrefix = "bf_"

// genSummaryBotToken 生成一个强随机 bot_token：bf_ 前缀 + 32 字节 crypto/rand 的 hex。
// 纯函数（除随机源外无副作用），便于单测断言「非空 / 带前缀 / 每次不同」。
func genSummaryBotToken() (string, error) {
	b := make([]byte, summaryBotTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return summaryBotTokenPrefix + hex.EncodeToString(b), nil
}

// ensureSummaryBotToken 在 server 启动时确保「总结助手」(summary_notification) 的
// bot_token 已就绪（OCT-5 / 方案 D：共享 DB）。
//
// 背景：迁移 20260629000001_summary_notification_bot.sql 只插入 user/robot 身份行，
// bot_token 留空（''）—— 避免在公开仓库写死明文凭据（reviewer 卡点）。token 改为
// 由 server **首次启动时自动生成强随机值并写回 robot 表**；smart-summary（共享同一
// IM 库）启动时 SELECT 该 token 用于鉴权。运维零准备、源码零明文、每部署唯一。
//
// 逻辑（幂等）：
//  1. 查 robot 表 bot_token（where robot_id=summary_notification）；非空则跳过；
//  2. 为空（''）时生成强随机 token，用带空值 WHERE 条件的 UPDATE 写回。
//
// 并发安全：多实例同时启动时，UPDATE 的 `WHERE bot_token='' OR bot_token IS NULL`
// 保证只有第一个提交的 UPDATE 把空值改成非空、影响 1 行；其余实例的 UPDATE 命中
// 0 行（WHERE 已不再匹配），不会覆盖已写入的 token。各自下一次启动复查也会看到非空
// token 而跳过。无需额外锁。
//
// 失败只 log 不 panic（与 insertSystemRobot 失败处理一致）：token 暂缺只会让通知
// 鉴权失败、降级为不发通知，不应阻断整个 server 启动。
func (rb *Robot) ensureSummaryBotToken() {
	robotID := pkgspace.SummaryNotificationBotUID

	current, err := rb.db.queryBotTokenByRobotID(robotID)
	if err != nil {
		rb.Error("查询 summary bot token 失败", zap.String("robot_id", robotID), zap.Error(err))
		return
	}
	if current != "" {
		// 已有非空 token（迁移外或前次启动写入），幂等跳过。
		return
	}

	token, err := genSummaryBotToken()
	if err != nil {
		rb.Error("生成 summary bot token 失败", zap.String("robot_id", robotID), zap.Error(err))
		return
	}

	affected, err := rb.db.updateRobotBotTokenIfEmpty(robotID, token)
	if err != nil {
		rb.Error("写入 summary bot token 失败", zap.String("robot_id", robotID), zap.Error(err))
		return
	}
	if affected == 0 {
		// 并发场景：另一实例抢先写入了 token，本次 UPDATE 命中 0 行。属正常幂等结果。
		rb.Info("summary bot token 已由其它实例写入，跳过", zap.String("robot_id", robotID))
		return
	}
	rb.Info("summary bot token 已自动生成并写入", zap.String("robot_id", robotID))
}
