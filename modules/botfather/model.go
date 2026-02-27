package botfather

// BotRegisterResp Bot自注册响应
type BotRegisterResp struct {
	RobotID        string `json:"robot_id"`
	IMToken        string `json:"im_token"`
	WSURL          string `json:"ws_url"`
	APIURL         string `json:"api_url"`
	OwnerUID       string `json:"owner_uid"`
	OwnerChannelID string `json:"owner_channel_id"`
}

// BotSendMessageReq Bot发送消息请求
type BotSendMessageReq struct {
	ChannelID   string                 `json:"channel_id"`
	ChannelType uint8                  `json:"channel_type"`
	StreamNo    string                 `json:"stream_no"`
	Payload     map[string]interface{} `json:"payload"`
}

// BotTypingReq Bot输入状态请求
type BotTypingReq struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

// BotEventsReq Bot获取事件请求
type BotEventsReq struct {
	EventID int64 `json:"event_id"`
	Limit   int64 `json:"limit"`
}

// BotEventAckReq Bot确认事件请求
type BotEventAckReq struct {
	EventID int64 `json:"event_id"`
}

// BotStreamStartReq 流式消息开始请求
type BotStreamStartReq struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	Payload     []byte `json:"payload"`
}

// BotStreamEndReq 流式消息结束请求
type BotStreamEndReq struct {
	StreamNo    string `json:"stream_no"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

// BotReadReceiptReq Bot阅读回执请求
type BotReadReceiptReq struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType uint8    `json:"channel_type"`
	MessageIDs  []string `json:"message_ids"`
}

// BotHeartbeatReq Bot心跳请求（REST模式）
type BotHeartbeatReq struct{}

// BotInfo BotFather中的机器人信息
type BotInfo struct {
	RobotID     string `json:"robot_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	BotToken    string `json:"bot_token"`
	CreatorUID  string `json:"creator_uid"`
	Status      int    `json:"status"`
}
