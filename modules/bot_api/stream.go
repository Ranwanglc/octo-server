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

	// OCT-41: record stream_no → robotID so /v1/bot/stream/end can verify the
	// caller owns the stream before terminating it. Best-effort — the bubble is
	// already live, so a binding-write failure must not fail the open; it only
	// weakens the later ownership check (which then falls through to the channel
	// gate). Log and continue.
	if bindErr := ba.streamOwners().bind(streamNo, robotID); bindErr != nil {
		ba.Warn("记录stream owner失败", zap.Error(bindErr),
			zap.String("robotID", robotID), zap.String("streamNo", streamNo))
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
//
// OCT-41 — ownership binding. The channel gate alone does NOT prove the caller
// opened this stream_no: WuKongIM resolves a stream by its stream_no alone, and
// the channel_id/channel_type in MessageStreamEndReq are addressing/routing
// fields, not a stream↔channel authorization check (the deployed WuKongIM, an
// octo-im v2.2.x fork, exposes no "does stream_no belong to channel_id?" check
// — the legacy /streammessage/* manager API that octo-lib targets is served
// only by older v1.x images and still keys streams by stream_no). So without
// the explicit owner check below, a co-member bot could observe a peer's live
// stream_no (it is rendered into the channel and visible via /messages/sync)
// and prematurely terminate the peer's bubble. We bind stream_no → robotID at
// start (streamStart) and reject an end whose recorded owner is a different
// bot. The binding is independent of channel_id, so it also closes the
// channel-gate-bypass concern (claiming a channel you may post to while ending
// a stream that lives elsewhere).
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

	// OCT-41 ownership gate. Posture: deny on a CONFIRMED owner mismatch; fall
	// through (allow) when no binding is found or the lookup errors. A binding
	// is present for the entire live window of any stream opened via
	// stream/start, so the griefing window — a co-member bot ending a peer's
	// still-live bubble — is fully covered. Absence means the stream is stale
	// (TTL elapsed, so it closed long ago) or was opened outside this path;
	// failing open there preserves the terminal-END guarantee (a missing END
	// leaves the octo-web bubble stuck "streaming") without reopening the
	// attack, since a stale/foreign stream_no has no live bubble to grief.
	if owner, ownErr := ba.streamOwners().owner(req.StreamNo); ownErr != nil {
		ba.Warn("查询stream owner失败，跳过归属校验", zap.Error(ownErr),
			zap.String("robotID", robotID), zap.String("streamNo", req.StreamNo))
	} else if owner != "" && owner != robotID {
		ba.Warn("拒绝结束非本bot的stream", zap.String("robotID", robotID),
			zap.String("owner", owner), zap.String("streamNo", req.StreamNo))
		httperr.ResponseErrorL(c, errcode.ErrBotAPIStreamNotOwned, nil, nil)
		return
	}

	req.ChannelID = ba.resolveSpaceChannelID(robotID, req.ChannelID, req.ChannelType)

	if err := ba.dispatchStreamEnd(req); err != nil {
		ba.Error("发送stream end消息失败", zap.Error(err), zap.String("robotID", robotID))
		httperr.ResponseErrorL(c, errcode.ErrBotAPISendFailed, nil, nil)
		return
	}

	// Stream closed: drop the binding so the stream_no does not linger in the
	// store. Best-effort — a stale binding only expires by TTL otherwise.
	if relErr := ba.streamOwners().release(req.StreamNo); relErr != nil {
		ba.Warn("释放stream owner失败", zap.Error(relErr),
			zap.String("robotID", robotID), zap.String("streamNo", req.StreamNo))
	}

	c.ResponseOK()
}
