package robot

import (
	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
)

// 总结助手（Summary Assistant）专属账号 —— 固定常量化，迁移拥有。
//
// 设计要点（PR#483 / OCT-5 Boss 定稿 + 第二轮 reviewer 🟡 MAJOR 收口）：
//   - 「总结助手」是固定 UID 的系统 bot（summary_notification，单一真源 =
//     pkg/space.SummaryNotificationBotUID，同时登记在 SystemBots 中）。
//   - bot 的 user 行 / robot 行 / 固定 bot_token 全部由迁移
//     modules/robot/sql/20260629000001_summary_notification_bot.sql 写死并拥有。
//   - **运行时不再做任何 env 驱动的自举 / reconcile / token 改写**。
//
// 为什么删掉 env 自举（第二轮 reviewer 🟡 MAJOR）：
//   原方案在 startup 调 insertSummaryRobot()，读 SUMMARY_BOT_UID / SUMMARY_BOT_TOKEN，
//   若已存在则 reconcileSummaryRobot() 以 env 为准纠偏 bot_token。问题：固定常量化后
//   bot_token 写死在迁移里；若旧部署仍带不同的 SUMMARY_BOT_TOKEN，reconcile 会**覆盖
//   迁移写死的 token**，破坏鉴权（smart-summary 用迁移里的固定 token 调
//   /v1/bot/sendMessage 会 401）。让鉴权依赖 stale env 状态与固定 token 设计自相矛盾。
//   因此彻底移除运行时 env 自举/reconcile 路径，bot 完全由迁移拥有。
//
// 仍保留 SummaryBotUID()：被 bot_api 的 ensureFriend 端点引用做 UID 白名单门控
// （仅放行这一个 UID），以及其它代码引用固定常量。

// SummaryBotUID 返回「总结助手」的固定常量 UID。
//
// 单一真源 = pkg/space.SummaryNotificationBotUID（同时在 SystemBots 中）。供
// bot_api 的 ensureFriend 端点做 UID 白名单门控（仅放行这一个 UID）。不读任何 env
// —— 避免部署间 UID 漂移；bot 身份完全由迁移拥有。
func SummaryBotUID() string {
	return pkgspace.SummaryNotificationBotUID
}
