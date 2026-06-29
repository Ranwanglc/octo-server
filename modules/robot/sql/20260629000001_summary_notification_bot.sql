-- +migrate Up
-- PR#483 (OCT-5) — 固定常量化 summary bot 自举。
--
-- 背景：智能总结的异步通知以「总结助手」身份发出。原方案走 env 驱动的动态 UID
-- （SUMMARY_BOT_UID / SUMMARY_BOT_TOKEN）+ 运行时自举（modules/robot/summary_bot.go）。
-- 三个 reviewer 一致卡 🔴 P1：动态 UID + ensureFriend 插真实 space_member 行会让 bot
-- 顺带获得 Space 级能力、并污染 member_count / 名册 / @选择器。Boss 定稿方案改为：
--   - 固定常量 UID = summary_notification（写进 pkg/space.SystemBots 静态常量）；
--   - bot_token 写死进本迁移（不走 env 注入），命中 bot_api/auth.go 的 User Bot 分支
--     （db.go queryRobotByBotToken 要求 bot_token!='' AND status=1）；
--   - 不再依赖 space_member 行做能力/归属（见 ensure_friend.go 注释与 PR 报告 step4）。
--
-- 幂等：INSERT IGNORE，重复执行不报错（uid / robot_id 唯一索引命中即跳过）。

-- user 行：robot=1 标记为机器人用户；status=1 可用；category=system 让统计/搜索按
-- 系统账号处理（与 u_10000 / fileHelper 一致）。
--
-- short_no（PR#483 第二轮 🟢 MINOR 1 修复）：user.short_no 有 UNIQUE 索引。原值
-- '20483' 是一个**未保留**的数字短号，可能已被真实用户占用 —— 那样 INSERT IGNORE
-- 会静默跳过 user 行、但 robot 行仍插入，留下半残身份。系统账号 u_10000/fileHelper
-- 用的是保留数字段（10000/20000），但本仓没有 summary bot 的保留数字段登记，无法保证
-- 任意数字值不撞真实用户。因此这里改用 **UID 本身**（'summary_notification'）作 short_no：
--   1. 唯一性：UID 全局唯一（user.uid 主键 / robot.robot_id 唯一），借用作 short_no
--      天然唯一；
--   2. 不撞真实用户：真实用户的 short_no 由 modules/user/service.go AddUser 走
--      util.Ten2Hex(UnixNano())（纯 [0-9a-z] 数字/十六进制串）或 commonService
--      .GetShortno()（数字短号）生成，**永远不含下划线**，而 'summary_notification'
--      含 '_'，故二者值空间不相交；
--   3. 稳定：固定常量，幂等重跑不变。
-- （查询侧 modules/user/db.go 用 `short_no=? AND short_no<>''` 过滤空值，本值非空安全。）
INSERT IGNORE INTO `user`
  (uid, name, short_no, robot, category, status,
   search_by_phone, search_by_short, new_msg_notice, voice_on, shock_on, msg_show_detail,
   is_upload_avatar, `version`)
VALUES
  ('summary_notification', '总结助手', 'summary_notification', 1, 'system', 1,
   0, 0, 0, 0, 0, 0,
   0, 1);

-- robot 行：bot_token 写死（bf_ 前缀，固定常量）；status=1 启用；username=uid。
-- 其余列（app_id / description / im_token_cache / bot_commands / auto_approve / placeholder
-- 等）走列默认值。
INSERT IGNORE INTO `robot`
  (robot_id, token, bot_token, username, status, `version`, creator_uid, description)
VALUES
  ('summary_notification', 'summary_notification', 'bf_7c7314e3734b17dfe6988b1d9031ea52',
   'summary_notification', 1, 1, '', '智能总结通知助手（系统 bot，固定 UID）');

-- +migrate Down
DELETE FROM `robot` WHERE robot_id='summary_notification';
DELETE FROM `user` WHERE uid='summary_notification';
