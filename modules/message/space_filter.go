package message

import (
	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-server/modules/space"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	spacepkg "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"go.uber.org/zap"
)

// FilterConversationsBySpace 对已获取的会话列表按 spaceID 过滤。
// 关键逻辑：
// - 群聊 space_id 不在 channel_id 前缀中，需查 group 表
// - 系统 Bot (botfather, u_10000, fileHelper) 所有 Space 可见
// - 普通 Bot 需查 space_member 表确认是否在目标 Space
// - 默认 Space（用户最早加入的）中显示裸 UID 旧会话
// - DB 查询失败时 skipBotFilter=true，不过滤避免误删
func FilterConversationsBySpace(
	conversations []*SyncUserConversationResp,
	filterSpaceID string,
	loginUID string,
	ctx *config.Context,
	groupService group.IService,
) []*SyncUserConversationResp {
	if len(conversations) == 0 {
		return conversations
	}

	// 查用户的默认 Space（最早加入的），裸 UID 旧会话只在默认 Space 显示
	defaultSpaceID := space.GetUserDefaultSpaceID(ctx, loginUID)

	// 群聊的 channel_id 是裸 group_no（没有 Space 前缀），ParseChannelID 返回 spaceID=""。
	// 需要从 group 表查出真实 space_id。
	groupNoSeen := make(map[string]struct{})
	var bareGroupNos []string
	var bareDMUIDs []string
	addGroupNo := func(no string) {
		if _, ok := groupNoSeen[no]; ok {
			return
		}
		groupNoSeen[no] = struct{}{}
		bareGroupNos = append(bareGroupNos, no)
	}
	for _, conv := range conversations {
		if conv.SpaceID == "" && conv.ChannelType == common.ChannelTypeGroup.Uint8() {
			addGroupNo(conv.ChannelID)
		}
		if conv.SpaceID == "" && conv.ChannelType == common.ChannelTypePerson.Uint8() {
			bareDMUIDs = append(bareDMUIDs, conv.ChannelID)
		}
		// 子区会话需要按父群的 space_id 决定可见性，把父群 groupNo 也加入查询。
		// 同一父群的多个子区/父群本身都可能命中，dedup 避免下游 GetGroups 重复查询。
		if conv.ChannelType == common.ChannelTypeCommunityTopic.Uint8() {
			if parentNo, _, err := thread.ParseChannelID(conv.ChannelID); err == nil {
				addGroupNo(parentNo)
			}
		}
	}

	// 构建 groupNo -> spaceID 映射
	skipGroupFilter := false
	groupSpaceMap, err := spacepkg.GetGroupSpaceMap(bareGroupNos, func(nos []string) ([]spacepkg.GroupSpaceInfo, error) {
		infos, err := groupService.GetGroups(nos)
		if err != nil {
			return nil, err
		}
		result := make([]spacepkg.GroupSpaceInfo, 0, len(infos))
		for _, g := range infos {
			result = append(result, spacepkg.GroupSpaceInfo{GroupNo: g.GroupNo, SpaceID: g.SpaceID})
		}
		return result, nil
	})
	if err != nil {
		log.Warn("查询群 SpaceID 错误，跳过群过滤", zap.Error(err))
		skipGroupFilter = true
	}

	// 查询用户作为外部成员加入的群 → { groupNo: sourceSpaceID }
	externalGroupMap, err := group.NewDB(ctx).QueryExternalGroupNosForUser(loginUID)
	if err != nil {
		log.Warn("查询外部群失败，跳过外部群过滤", zap.Error(err))
		externalGroupMap = make(map[string]string)
	}

	// Bot DM 过滤
	botSet, botInSpace, skipBotFilter := resolveBotFilter(ctx, filterSpaceID, bareDMUIDs)

	return filterConversationsCore(conversations, filterSpaceID, defaultSpaceID, groupSpaceMap, externalGroupMap, botSet, botInSpace, skipGroupFilter, skipBotFilter)
}

// filterConversationsCore 是纯过滤逻辑，不依赖 DB/ctx，便于单元测试。
func filterConversationsCore(
	conversations []*SyncUserConversationResp,
	filterSpaceID string,
	defaultSpaceID string,
	groupSpaceMap map[string]string,
	externalGroupMap map[string]string,
	botSet map[string]bool,
	botInSpace map[string]bool,
	skipGroupFilter bool,
	skipBotFilter bool,
) []*SyncUserConversationResp {
	filtered := make([]*SyncUserConversationResp, 0, len(conversations))
	for _, conv := range conversations {
		spaceID := conv.SpaceID
		// 群聊用 group 表的 space_id 替代 ParseChannelID 的结果
		if spaceID == "" && conv.ChannelType == common.ChannelTypeGroup.Uint8() {
			if skipGroupFilter {
				// 查询失败时不过滤群聊，直接保留
				filtered = append(filtered, conv)
				continue
			}
			spaceID = groupSpaceMap[conv.ChannelID]
		}

		if spaceID == filterSpaceID && conv.ChannelType != common.ChannelTypeCommunityTopic.Uint8() {
			filtered = append(filtered, conv)
		} else if conv.ChannelType == common.ChannelTypeCommunityTopic.Uint8() {
			// 子区：按父群的 space_id 决定可见性，规则与群聊一致（含外部群兜底、旧群放行）
			if filterThreadConv(conv, filterSpaceID, defaultSpaceID, groupSpaceMap, externalGroupMap, skipGroupFilter) {
				filtered = append(filtered, conv)
			}
		} else if conv.ChannelType == common.ChannelTypeGroup.Uint8() {
			// 外部群：用户作为外部成员加入的群，在其 source Space 下显示
			if sourceSpace, ok := externalGroupMap[conv.ChannelID]; ok {
				effectiveSource := sourceSpace
				if effectiveSource == "" {
					// fallback: 用户已离开 source Space 时，降级到默认 Space
					effectiveSource = defaultSpaceID
				}
				if effectiveSource == filterSpaceID {
					filtered = append(filtered, conv)
					continue
				}
			}
			if spaceID == "" {
				// 旧群（无 space_id）在所有 Space 可见
				filtered = append(filtered, conv)
			}
		} else if spaceID == "" && filterSpaceID == defaultSpaceID && conv.ChannelType != common.ChannelTypeCommunityTopic.Uint8() {
			// 裸 UID 旧会话只在默认 Space 显示
			// Bot DM：Bot 不在默认 Space 则排除（查询失败时不过滤，避免误删）
			// 子区已在上面按父群 space_id 处理过，不能再从这里漏过去
			if !skipBotFilter && conv.ChannelType == common.ChannelTypePerson.Uint8() && botSet[conv.ChannelID] && !botInSpace[conv.ChannelID] {
				continue
			}
			filtered = append(filtered, conv)
		} else if spaceID == "" && conv.ChannelType == common.ChannelTypePerson.Uint8() {
			// 非默认 Space 中的 DM 会话
			if skipBotFilter {
				// 查询失败时不过滤，保留所有 DM
				filtered = append(filtered, conv)
			} else if spacepkg.SystemBots[conv.ChannelID] {
				// 系统 Bot → 所有 Space 可见
				filtered = append(filtered, conv)
			} else if botSet[conv.ChannelID] && botInSpace[conv.ChannelID] {
				// 普通 Bot 在此 Space → 显示
				filtered = append(filtered, conv)
			} else if !botSet[conv.ChannelID] {
				// 普通 DM（非 Bot）→ 仅在 Recents 中有匹配 space_id 的消息时显示
				if personConvHasSpaceMessages(conv, filterSpaceID) {
					filtered = append(filtered, conv)
				}
			}
			// Bot 不在此 Space → 不显示
		}
	}
	return filtered
}

// filterThreadConv 判断子区会话是否应在 filterSpaceID 中显示。
// 规则：跟父群一致——按父群的 space_id 匹配，外部群走 source Space 兜底，旧群（无 space_id）所有 Space 可见。
// channel_id 解析失败的子区会话会被丢弃。
func filterThreadConv(
	conv *SyncUserConversationResp,
	filterSpaceID string,
	defaultSpaceID string,
	groupSpaceMap map[string]string,
	externalGroupMap map[string]string,
	skipGroupFilter bool,
) bool {
	parentNo, _, err := thread.ParseChannelID(conv.ChannelID)
	if err != nil {
		return false
	}
	if skipGroupFilter {
		return true
	}
	parentSpaceID := groupSpaceMap[parentNo]
	if parentSpaceID == filterSpaceID {
		return true
	}
	if sourceSpace, ok := externalGroupMap[parentNo]; ok {
		eff := sourceSpace
		if eff == "" {
			eff = defaultSpaceID
		}
		if eff == filterSpaceID {
			return true
		}
	}
	// 父群无 space_id（旧群） → 子区跟旧群一样所有 Space 可见
	return parentSpaceID == ""
}

// personConvHasSpaceMessages 检查 Person 会话的 Recents 中是否有 space_id 匹配的消息。
// 用于判断该 DM 会话是否"属于"目标 Space（有过消息往来）。
func personConvHasSpaceMessages(conv *SyncUserConversationResp, targetSpaceID string) bool {
	if conv == nil || len(conv.Recents) == 0 {
		return false
	}
	for _, msg := range conv.Recents {
		if msg.Payload != nil {
			if sid, ok := msg.Payload["space_id"]; ok {
				if sidStr, ok := sid.(string); ok && sidStr == targetSpaceID {
					return true
				}
			}
		}
	}
	return false
}

// EnsureSystemBotsPresent 保证 Space-scoped sync 响应中一定包含系统 Bot
// （目前 botfather / u_10000 / fileHelper）的 conversation entry。
//
// 背景 (YUJ-216 / GH#1280)：
//   - POST /v1/conversation/sync 带 X-Space-ID 时，IM 核心只会返回自
//     `version` 之后有新消息的 conversation。系统 Bot 若没有新消息就不会
//     出现在增量响应中，经 Space 过滤后客户端也拿不到。移动端没有 Web
//     那样的前端兜底，就会导致用户在某些 Space 下"消失"了 botfather 私聊。
//   - 修复策略：只要调用方开启了 Space 过滤，就在最终响应中显式补齐每一个
//     系统 Bot 的 entry。已经存在的 entry（有真实 Recents）保持不变；缺席的
//     以最小占位形式注入，兼容老客户端。
//
// 占位 entry 的字段原则：
//   - ChannelID / ChannelType：对齐 Person DM；
//   - SpaceID: 空串 —— 系统 Bot 不属于任何 Space；
//   - Recents / LastMsgSeq / Unread / Version / Timestamp 保持零值，避免
//     客户端误以为有新消息或错把占位写回 ack；
//   - 其他字段沿用结构体默认值，等价于"已知此频道、无新内容"。
//
// 不影响消息级 space_id 过滤：本函数只补 conversation-level 占位，
// 对 Recents 内 payload.space_id 字段不做任何修改。
func EnsureSystemBotsPresent(conversations []*SyncUserConversationResp) []*SyncUserConversationResp {
	systemBots := spacepkg.SystemBotList()
	if len(systemBots) == 0 {
		return conversations
	}

	present := make(map[string]bool, len(conversations))
	for _, conv := range conversations {
		if conv == nil {
			continue
		}
		if conv.ChannelType == common.ChannelTypePerson.Uint8() && spacepkg.IsSystemBot(conv.ChannelID) {
			present[conv.ChannelID] = true
		}
	}

	for _, uid := range systemBots {
		if present[uid] {
			continue
		}
		conversations = append(conversations, newSystemBotPlaceholder(uid))
	}
	return conversations
}

// newSystemBotPlaceholder 构造一个空的 Person 会话占位，字段口径与
// newSyncUserConversationResp 生成的真实会话保持一致，避免新老客户端解码
// 差异。Recents 明确初始化为空切片，保证 JSON 序列化为 `[]` 而非 `null`。
func newSystemBotPlaceholder(uid string) *SyncUserConversationResp {
	return &SyncUserConversationResp{
		ChannelID:   uid,
		ChannelType: common.ChannelTypePerson.Uint8(),
		SpaceID:     "",
		Recents:     []*MsgSyncResp{},
	}
}

// filterPersonMessagesBySpace 按 X-Space-ID 过滤 Person (DM) 历史消息列表。
//
// 背景（YUJ-219-A / GH#1283，对应 analysis-report.md §4.1）：
//   - /v1/message/channel/sync 原先对消息级 payload.space_id 0 过滤。客户端
//     进入 botfather / u_10000 / fileHelper 或历史 DM 会话时，会拿到跨 Space
//     的全部历史消息；配合三端不一致的渲染过滤，用户实际看到跨 Space 消息。
//   - Phase 3 五层 Defense-in-Depth 全部作用在 conversation-list，message-level
//     没有权威 Space 标签，这是"BotFather 历史消息跨 Space 可见"回归的根因。
//
// 本函数仅针对 Person (DM) 路径：
//   - GROUP channel_id 本身做 Space 隔离（不同 Space 的群 channel_id 不同），
//     对历史消息再过滤反而会误杀老群，因此 GROUP/COMMUNITY_TOPIC 路径不走这里。
//   - 规则（与 Android ChatActivity.filterSystemBotMessages 口径对齐）：
//       1) payload.space_id == spaceID               → 保留（精确匹配当前 Space）
//       2) payload.space_id == "" && !isSystemBot    → 保留（老 DM 消息向前兼容）
//       3) payload.space_id == "" &&  isSystemBot    → 丢弃（SystemBot 无 space
//          标签的老消息默认隐藏，避免 fileHelper/u_10000 老消息跨 Space 泄露）
//       4) payload.space_id != "" && != spaceID      → 丢弃（跨 Space 明确污染）
//
// 调用方需保证 spaceID != ""（空串视为未启用 Space 过滤，直接返回原列表），
// 并只对 ChannelTypePerson 调用本函数。
func filterPersonMessagesBySpace(msgs []*MsgSyncResp, channelID, spaceID string) []*MsgSyncResp {
	if spaceID == "" || len(msgs) == 0 {
		return msgs
	}
	isSysBot := spacepkg.IsSystemBot(channelID)
	filtered := make([]*MsgSyncResp, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		msid := extractPayloadSpaceID(m.Payload)
		switch {
		case msid == spaceID:
			// 精确匹配当前 Space → 保留
			filtered = append(filtered, m)
		case msid == "" && !isSysBot:
			// 老 DM 消息无 space_id 字段，向前兼容保留，避免 Phase 3 前的历史
			// 消息被一刀切隐藏（对齐 filterConversationsCore 对普通 DM 的口径）。
			filtered = append(filtered, m)
		case msid == "" && isSysBot:
			// 系统 Bot 的无 space_id 历史消息一律隐藏。对齐 Android
			// filterSystemBotMessages 和 iOS filterMessagesBySpace，避免
			// 老的 botfather/fileHelper/u_10000 对话全量跨 Space 暴露。
			continue
		case msid != spaceID:
			// 明确跨 Space，丢弃。
			continue
		}
	}
	return filtered
}

// extractPayloadSpaceID 从已反序列化的消息 payload 中读取 space_id 字段。
// payload 非 map、字段缺失或类型不符时返回 ""，调用方据此走"无 space_id"分支。
func extractPayloadSpaceID(payload map[string]interface{}) string {
	if len(payload) == 0 {
		return ""
	}
	v, ok := payload["space_id"]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// resolveBotFilter 批量查询 Bot 状态和 Space 成员关系。
// 返回 botSet（哪些 UID 是 Bot）、botInSpace（哪些 Bot 在 filterSpaceID 中）、skipBotFilter（DB 错误时为 true）。
func resolveBotFilter(ctx *config.Context, filterSpaceID string, bareDMUIDs []string) (botSet map[string]bool, botInSpace map[string]bool, skipBotFilter bool) {
	botSet = make(map[string]bool)
	botInSpace = make(map[string]bool)

	if filterSpaceID == "" || len(bareDMUIDs) == 0 {
		return
	}

	var err error
	botSet, err = spacepkg.GetBotUIDs(ctx.DB(), bareDMUIDs)
	if err != nil {
		log.Warn("查询Bot UID错误，跳过Bot过滤", zap.Error(err))
		skipBotFilter = true
		return
	}

	if len(botSet) == 0 {
		return
	}

	botInSpace, err = spacepkg.CheckBotsInSpace(ctx.DB(), filterSpaceID, botSet)
	if err != nil {
		log.Warn("查询Bot Space成员错误，跳过Bot过滤", zap.Error(err))
		skipBotFilter = true
		return
	}
	return
}
