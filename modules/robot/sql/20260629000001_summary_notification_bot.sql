-- +migrate Up
-- PR#483 (OCT-5) — 固定常量化 summary bot 自举。
--
-- 背景：智能总结的异步通知以「总结助手」身份发出。原方案走 env 驱动的动态 UID
-- （SUMMARY_BOT_UID / SUMMARY_BOT_TOKEN）+ 运行时自举（modules/robot/summary_bot.go）。
-- 三个 reviewer 一致卡 🔴 P1：动态 UID + ensureFriend 插真实 space_member 行会让 bot
-- 顺带获得 Space 级能力、并污染 member_count / 名册 / @选择器。Boss 定稿方案改为：
--   - 固定常量 UID = summary_notification（写进 pkg/space.SystemBots 静态常量）；
--   - bot_token **不再写死进本迁移**（公开仓库明文凭据风险，reviewer 卡 🔴）。本迁移
--     只插入 user/robot 身份行，bot_token 留空串（''）。token 由 server 启动时
--     **自动生成强随机值写回**（见 modules/robot/api.go ensureSummaryBotToken）：
--     首启检测空 token → crypto/rand 生成 bf_ 前缀 token → 带空值条件 UPDATE 写库，
--     幂等且并发安全。每部署唯一、源码零明文、运维零准备。
--     smart-summary（共享同一 IM 库）启动时 SELECT 该 token 用于鉴权（方案 D）。
--     命中 bot_api/auth.go 的 User Bot 分支（db.go queryRobotByBotToken 要求
--     bot_token!='' AND status=1）—— 故空 token 时鉴权天然失败直到 server 写回。
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

-- robot 行：bot_token 留**空串 ''**（不写死明文凭据）；status=1 启用；username=uid。
-- token 由 server 启动时 ensureSummaryBotToken() 自动生成强随机值并 UPDATE 写回
-- （见 modules/robot/api.go）。其余列（app_id / description / im_token_cache /
-- bot_commands / auto_approve / placeholder 等）走列默认值。
INSERT IGNORE INTO `robot`
  (robot_id, token, bot_token, username, status, `version`, creator_uid, description)
VALUES
  ('summary_notification', 'summary_notification', '',
   'summary_notification', 1, 1, '', '智能总结通知助手（系统 bot，固定 UID）');

-- +migrate Down
DELETE FROM `robot` WHERE robot_id='summary_notification';
DELETE FROM `user` WHERE uid='summary_notification';
