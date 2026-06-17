package bot_api

import (
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// streamStart handles POST /v1/bot/stream/start.
//
// OCT-31 — public bot-API mirror of modules/robot streamStart (api.go
// L305-322). It opens a WuKongIM message stream so a bot can render a live,
// token-by-token bubble in a channel. The returned stream_no is then passed
// to /v1/bot/sendMessage for each incremental chunk (send.go already forwards
// StreamNo to MsgSendReq), and /v1/bot/stream/end terminates the bubble.
//
// Security (PR review gate): unlike the internal robot path — which trusts the
// client-supplied FromUID — this is a PUBLIC route that emits into a channel,
// so two things are enforced here:
//
//  1. Channel gate: the same checkSendPermission used by sendMessage runs
//     first, so a bot can only open a stream in a channel it may post to.
//     hasOBOContext=false — stream start always dispatches AS the bot (we
//     force FromUID below), never as an OBO grantor, so the friend-gate
//     bypass must not apply.
//  2. Sender scoping: any client-supplied FromUID is overwritten with the
//     authenticated robotID, so the live bubble can only ever be attributed
//     to the calling bot.
func (ba *BotAPI) streamStart(c *wkhttp.Context) {
	var req config.MessageStreamStartReq
	if err := c.BindJSON(&req); err != nil {
		respondBotAPIRequestInvalid(c, "")
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		respondBotAPIRequestInvalid(c, "channel_id")
		return
	}
	if req.ChannelType == 0 {
		respondBotAPIRequestInvalid(c, "channel_type")
		return
	}

	robotID := getRobotIDFromContext(c)
	botKind := getBotKindFromContext(c)

	// Channel send-permission / membership gate — identical to sendMessage.
	if err := ba.checkSendPermission(c, botKind, robotID, req.ChannelID, req.ChannelType, false); err != nil {
		respondSendPermissionError(c, err)
		return
	}

	// Scope the stream to the authenticated bot. The client cannot choose the
	// stream's sender identity; overwrite any supplied value so the bubble is
	// always attributed to this bot.
	req.FromUID = robotID
	req.ChannelID = ba.resolveSpaceChannelID(robotID, req.ChannelID, req.ChannelType)

	streamNo, err := ba.dispatchStreamStart(req)
	if err != nil {
		ba.Error("发送stream start消息失败", zap.Error(err), zap.String("robotID", robotID))
		httperr.ResponseErrorL(c, errcode.ErrBotAPISendFailed, nil, nil)
		return
	}
	c.Response(gin.H{
		"stream_no": streamNo,
	})
}

// streamEnd handles POST /v1/bot/stream/end.
//
// OCT-31 — public bot-API mirror of modules/robot streamEnd (api.go
// L324-338). This emits the terminal END (streamFlag == END) that the
// octo-web client waits on to stop the live "streaming" bubble. It is
// REQUIRED: a missing END leaves the client bubble stuck "streaming"
// indefinitely (the exact failure Frontend flagged on OCT-10/OCT-18). The
// gateway (cc-channel-octo) must therefore always call this in a finally so
// END fires even on agent error/abort/truncation.
//
// Gated by the same checkSendPermission as stream/start and sendMessage so a
// bot can only close streams in channels it may post to.
func (ba *BotAPI) streamEnd(c *wkhttp.Context) {
	var req config.MessageStreamEndReq
	if err := c.BindJSON(&req); err != nil {
		respondBotAPIRequestInvalid(c, "")
		return
	}
	if strings.TrimSpace(req.StreamNo) == "" {
		respondBotAPIRequestInvalid(c, "stream_no")
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		respondBotAPIRequestInvalid(c, "channel_id")
		return
	}
	if req.ChannelType == 0 {
		respondBotAPIRequestInvalid(c, "channel_type")
		return
	}

	robotID := getRobotIDFromContext(c)
	botKind := getBotKindFromContext(c)

	if err := ba.checkSendPermission(c, botKind, robotID, req.ChannelID, req.ChannelType, false); err != nil {
		respondSendPermissionError(c, err)
		return
	}

	req.ChannelID = ba.resolveSpaceChannelID(robotID, req.ChannelID, req.ChannelType)

	if err := ba.dispatchStreamEnd(req); err != nil {
		ba.Error("发送stream end消息失败", zap.Error(err), zap.String("robotID", robotID))
		httperr.ResponseErrorL(c, errcode.ErrBotAPISendFailed, nil, nil)
		return
	}
	c.ResponseOK()
}
