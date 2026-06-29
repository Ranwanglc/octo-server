package bot_api

import (
	"fmt"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/robot"
	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"go.uber.org/zap"
)

// ensureFriendReq 是 POST /v1/bot/ensureFriend 的请求体。
//
// 契约（与 octo-smart-summary OCT-6 对齐）：
//   - TargetUID：被通知用户的 UID（必填）。
//   - SpaceID：可选。多 Space 部署下，IM 白名单 channel_id 需要 space 前缀
//     （s{spaceID}_{uid}），由 smart-summary 从 SummaryTask.SpaceID 传入。
//     缺省（单 Space / 平台级）时走裸 uid 分支，与发送路径 DM 的裸 uid 一致。
type ensureFriendReq struct {
	TargetUID string `json:"target_uid"`
	SpaceID   string `json:"space_id"`
}

// ensureFriend 为"总结助手"与目标用户建立双向好友关系（DM 可达前置）。
//
// 复刻 botfather/friend_approve.go 与 app_bot/app_bot.go 的好友建立配方：
//  1. friend 表双向（userService.AddFriend，底层 InsertOrUpdate 幂等）；
//  2. IM 白名单双向 —— 光插 friend 表不够，IM 层投递要白名单；channel_id 在有
//     Space 时必须用 s{spaceID}_{uid} 前缀形态（P1-1），否则多 Space 下白名单加错
//     channel，friend 表建了、应用层门过了，但 IM 层不放行 → DM 静默投递失败；
//  3. fixFriendVersion 双向（WKSDK 增量同步需要 version>0）；
//  4. SendCMD(CMDFriendAccept) 通知双端同步好友列表。
//
// 安全（P1-2）：本端点会强制建立（无需对方 opt-in）双向好友，是新攻击面。因此入口
// 硬校验调用方 robot_id 必须等于配置中的总结助手 UID，否则任何持有效 robot token 的
// bot 都能强制与任意用户建好友（钓鱼/垃圾消息向量）。范围严格限定那一个 UID。
//
// 整体幂等：重复调用无副作用（friend InsertOrUpdate / 白名单 add / version 修复
// 均可重复执行）。不改 send.go / auth.go 的发送鉴权主链路。
func (ba *BotAPI) ensureFriend(c *wkhttp.Context) {
	robotID := getRobotIDFromContext(c)
	if strings.TrimSpace(robotID) == "" {
		ba.respondBotAPIIdentityMissing(c)
		return
	}

	// P1-2：UID 白名单门控 —— 仅放行配置里的总结助手 UID。
	summaryUID := robot.SummaryBotUID()
	if summaryUID == "" || robotID != summaryUID {
		ba.Warn("ensureFriend 越权调用被拒（非总结助手 UID）", zap.String("robot_id", robotID))
		httperr.ResponseErrorLWithStatus(c, errcode.ErrBotAPIEnsureFriendForbidden, nil, nil)
		return
	}

	var req ensureFriendReq
	if err := c.BindJSON(&req); err != nil {
		respondBotAPIRequestInvalid(c, "")
		return
	}
	targetUID := strings.TrimSpace(req.TargetUID)
	if targetUID == "" {
		respondBotAPIRequestInvalid(c, "target_uid")
		return
	}

	spaceID := strings.TrimSpace(req.SpaceID)

	// P1-3：space 成员校验 —— 当请求带 space_id 时，target_uid 必须是该 Space 的活跃
	// 成员，否则拒绝。复用发送/可见性路径同款 pkg/space.CheckMembership（与
	// category/api.go、voice_adapter 等一致），防止借总结助手向非本 Space 用户强制建立
	// 好友（跨 Space 钓鱼/打扰向量）。spaceID 为空（单 Space/平台级）时跳过，与裸 uid
	// 发送路径语义一致。
	if spaceID != "" {
		isMember, err := pkgspace.CheckMembership(ba.db.session, spaceID, targetUID)
		if err != nil {
			ba.Error("ensureFriend space 成员校验失败", zap.String("space_id", spaceID), zap.Error(err))
			httperr.ResponseErrorL(c, errcode.ErrBotAPIStoreFailed, nil, nil)
			return
		}
		if !isMember {
			ba.Warn("ensureFriend 目标用户非 space 成员，拒绝", zap.String("space_id", spaceID), zap.String("target_uid", targetUID))
			httperr.ResponseErrorLWithStatus(c, errcode.ErrBotAPINotSpaceMember, nil, nil)
			return
		}

		// PR#483 Boss 定稿（step4，选择窄路径 —— 不再插 space_member 行）：
		//
		// 三个 reviewer 一致卡的 P1 是：给 summary bot 插真实 space_member 行会让
		// bot 顺带获得 Space 级能力（枚举成员、建群）+ 污染 member_count / 名册 /
		// @选择器。其中 member_count 是裸 `SELECT COUNT(*) FROM space_member ...`
		// （见 modules/space/db.go:72/86, db_manager.go:163），**不走 SystemBots 过滤**，
		// 所以只要还有 space_member 行，member_count 污染就无法根除。因此本次
		// **不再为 summary bot 插 space_member 行**。
		//
		// DM 的 Space 归属不靠 space_member 行，而是走 IsSystemBot 的窄路径：
		//   - 发送路径（/v1/bot/sendMessage）带 X-Space-ID 头时，isBotSpaceAuthorized
		//     对 IsSystemBot(robotID) 直接放行（系统 bot 在所有活跃 Space 可见，语义
		//     同 platform App Bot）→ X-Space-ID 被采纳 → enrichBotPayloadWithSpaceID
		//     注入权威 space_id，DM 不丢 Space 归属（详见 modules/bot_api/db.go
		//     isBotSpaceAuthorized 的 IsSystemBot 分支）。smart-summary 的 send 姿势始终
		//     携 X-Space-ID（SpaceID 来自 SummaryTask.SpaceID，与本端点 space_id 参数
		//     同源），所以这是唯一生产 send 形状。
		//
		// 结果：(a) DM 仍带正确 space_id（不回归 PR 之前的 DM-attribution bug）；
		//       (b) bot 无 space_member 行 → 无 Space 能力面；(c) 不污染计数 / 名册。
		// 本函数不再对 space_member 表做任何写入（friend 表 + IM 白名单仍是 DM 可达的
		// 唯一依赖，见下文）。
	}

	// 1. friend 表双向（幂等 InsertOrUpdate）。
	if err := ba.userService.AddFriend(targetUID, &user.FriendReq{
		UID:   targetUID,
		ToUID: robotID,
	}); err != nil {
		ba.Error("ensureFriend 添加好友(user->bot)失败", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrBotAPIStoreFailed, nil, nil)
		return
	}
	if err := ba.userService.AddFriend(robotID, &user.FriendReq{
		UID:   robotID,
		ToUID: targetUID,
	}); err != nil {
		ba.Error("ensureFriend 添加好友(bot->user)失败", zap.Error(err))
		httperr.ResponseErrorL(c, errcode.ErrBotAPIStoreFailed, nil, nil)
		return
	}

	// 2. IM 白名单双向。P1-1：有 Space 时 channel_id 用 s{spaceID}_{uid} 前缀形态
	//    （复刻 friend_approve.go:180-185 / app_bot.go:1130-1141）。白名单失败仅 warn，
	//    不阻断（与既有范式一致：best-effort，整体仍可重试）。
	userChannelID := targetUID
	botChannelID := robotID
	if spaceID != "" {
		userChannelID = fmt.Sprintf("s%s_%s", spaceID, targetUID)
		botChannelID = fmt.Sprintf("s%s_%s", spaceID, robotID)
	}
	if err := ba.ctx.IMWhitelistAdd(config.ChannelWhitelistReq{
		ChannelReq: config.ChannelReq{
			ChannelID:   userChannelID,
			ChannelType: common.ChannelTypePerson.Uint8(),
		},
		UIDs: []string{robotID},
	}); err != nil {
		ba.Warn("ensureFriend 添加IM白名单(user channel)失败", zap.String("channel_id", userChannelID), zap.Error(err))
	}
	if err := ba.ctx.IMWhitelistAdd(config.ChannelWhitelistReq{
		ChannelReq: config.ChannelReq{
			ChannelID:   botChannelID,
			ChannelType: common.ChannelTypePerson.Uint8(),
		},
		UIDs: []string{targetUID},
	}); err != nil {
		ba.Warn("ensureFriend 添加IM白名单(bot channel)失败", zap.String("channel_id", botChannelID), zap.Error(err))
	}

	// 3. 修复 friend version（双向），保证 WKSDK 增量同步可见。
	ba.fixFriendVersion(targetUID, robotID)
	ba.fixFriendVersion(robotID, targetUID)

	// 4. 通知双端同步好友列表。
	cmdParam := map[string]interface{}{
		"to_uid":   targetUID,
		"from_uid": robotID,
	}
	if spaceID != "" {
		cmdParam["space_id"] = spaceID
	}
	_ = ba.ctx.SendCMD(config.MsgCMDReq{
		CMD:         common.CMDFriendAccept,
		Subscribers: []string{targetUID, robotID},
		Param:       cmdParam,
	})

	c.ResponseOK()
}

// fixFriendVersion 修复好友 version=0（WKSDK 增量同步需要 version>0）。
// 复刻 botfather/command.go 的同名逻辑；best-effort，失败仅 warn 不阻断。
func (ba *BotAPI) fixFriendVersion(uid, toUID string) {
	var maxVer int64
	if err := ba.db.session.SelectBySql(
		"SELECT IFNULL(MAX(version),0) FROM friend WHERE uid=?", uid,
	).LoadOne(&maxVer); err != nil {
		ba.Warn("ensureFriend 查询好友最大version失败", zap.Error(err))
		return
	}
	if _, err := ba.db.session.UpdateBySql(
		"UPDATE friend SET version=? WHERE uid=? AND to_uid=? AND version=0", maxVer+1, uid, toUID,
	).Exec(); err != nil {
		ba.Warn("ensureFriend 更新好友version失败", zap.Error(err))
	}
}
