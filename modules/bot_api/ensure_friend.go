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
