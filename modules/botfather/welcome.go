package botfather

import (
	"fmt"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"go.uber.org/zap"
)

const (
	// welcomeSentKeyPrefix Redis key prefix for tracking welcome message sent status
	welcomeSentKeyPrefix = "botfather:welcome:sent:"
	// welcomeSentTTL TTL for welcome sent flag (7 days, in case user re-registers)
	welcomeSentTTL = 60 * 60 * 24 * 7
)

// DefaultWelcomeMessage is the default welcome message content
const DefaultWelcomeMessage = `👋 欢迎来到 Octo！企业级 Agent-Native 协作平台

在 DMwork，AI 不是工具，是你的同事：
🤝 AI 是一等公民 — 可管理、可审计、可信任的数字员工
🔗 你的 AI 属于你 — 跟着你走，为你工作

我是 BotFather，帮你创建和管理 AI 机器人：
· /newbot — 创建新机器人
· /help — 查看所有命令

💡 有想法或建议？进入 Bot 广场，添加「Octo 产品管家」反馈`

// handleUserRegisterEvent handles user registration event to send welcome message
func (bf *BotFather) handleUserRegisterEvent(data []byte, commit config.EventCommit) {
	// Parse event data
	var req map[string]interface{}
	err := util.ReadJsonByByte(data, &req)
	if err != nil {
		bf.Error("解析用户注册事件数据失败", zap.Error(err))
		commit(nil) // Don't block on parse error
		return
	}

	uid, ok := req["uid"].(string)
	if !ok || uid == "" {
		bf.Error("用户注册事件缺少uid")
		commit(nil)
		return
	}

	// Skip if it's a special system user
	if uid == BotFatherUID || uid == "u_10000" || uid == "fileHelper" {
		commit(nil)
		return
	}

	// Check idempotency: has welcome message already been sent?
	sentKey := fmt.Sprintf("%s%s", welcomeSentKeyPrefix, uid)
	sentValue, err := bf.ctx.GetRedisConn().GetString(sentKey)
	if err != nil && err.Error() != "redis: nil" {
		bf.Warn("检查欢迎消息发送状态失败", zap.Error(err), zap.String("uid", uid))
		// Continue anyway, worst case we send duplicate
	}
	if sentValue != "" {
		bf.Debug("欢迎消息已发送，跳过", zap.String("uid", uid))
		commit(nil)
		return
	}

	// Send welcome message
	err = bf.sendWelcomeMessage(uid)
	if err != nil {
		bf.Error("发送欢迎消息失败", zap.Error(err), zap.String("uid", uid))
		// Don't fail the event, welcome message is non-critical
		commit(nil)
		return
	}

	// Mark as sent (idempotency)
	err = bf.ctx.GetRedisConn().SetAndExpire(sentKey, "1", welcomeSentTTL)
	if err != nil {
		bf.Warn("标记欢迎消息已发送失败", zap.Error(err), zap.String("uid", uid))
	}

	bf.Info("欢迎消息发送成功", zap.String("uid", uid))
	commit(nil)
}

// sendWelcomeMessage sends a welcome message from BotFather to the new user
func (bf *BotFather) sendWelcomeMessage(toUID string) error {
	// Use default welcome message
	// Future: can be made configurable via database or env var
	welcomeContent := DefaultWelcomeMessage

	// Send message via IM
	_, err := bf.ctx.SendMessageWithResult(&config.MsgSendReq{
		Header: config.MsgHeader{
			RedDot: 1,
		},
		ChannelID:   toUID,
		ChannelType: common.ChannelTypePerson.Uint8(),
		FromUID:     BotFatherUID,
		Payload: []byte(util.ToJson(map[string]interface{}{
			"type":    common.Text,
			"content": welcomeContent,
		})),
	})

	return err
}
