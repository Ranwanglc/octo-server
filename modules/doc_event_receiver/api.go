package doc_event_receiver

// POST /v1/docs/events —— doc 侧 (octo-docs-html) 回投评论/事件。
//
// 契约（OCT-137/A）：
//   - 鉴权：X-Octo-Doc-Webhook-Token = env OCTO_DOC_EVENT_WEBHOOK_TOKEN，crypto/subtle 恒时比较。
//     未配 env → 直接 503 拒收（防止 token 缺失时事件被 200 静默丢），token 错 → 401。
//   - event_type：当前只落 comment.created；其他 202 忽略（doc 侧日后扩，不返 4xx 触发重试）。
//   - slug 未知 / slug 无 binding → 202 忽略（解绑的旧 slug doc 端仍会回投，别让它反复重试）。
//   - mount=group → 投 GroupNo (ChannelTypeGroup)；
//     mount=thread → thread.BuildChannelID(GroupNo, ThreadId) (ChannelTypeCommunityTopic)；
//     mount=space → 本单先 202 + warn（space→IM 投递方案未定）。
//   - payload JSON 坏 / 缺 slug/event_type → 400。
//
// 卡片文案：markdown text (ContentType=Text=1)，actor/title/text 一律 HTML escape 后拼。
// text 超 200 rune 截断加 …。这是最小可行版；日后前端对齐再改 richtext。

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/doc_binding"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"go.uber.org/zap"
)

const (
	tokenEnv     = "OCTO_DOC_EVENT_WEBHOOK_TOKEN"
	tokenHeader  = "X-Octo-Doc-Webhook-Token"
	routePath    = "/v1/docs/events"
	maxBodyBytes = 64 << 10 // 64KB 防超大 body；单条评论正常远小于此
	// commentPreviewRunes：卡片摘要文案最多 200 rune，超出加 …。
	commentPreviewRunes = 200
	// docBotUID：卡片消息的发送者；未配 sys uid 时的兜底占位。日后接系统机器人体系再换。
	docBotUID = "doc_bot"
)

// eventTypeCommentCreated 目前只落这一种，其他 event_type 202 忽略。
const eventTypeCommentCreated = "comment.created"

// bindingLookup 抽出接口是为了单测能塞 in-mem 实现，生产走 doc_binding.LookupSlug。
type bindingLookup interface {
	LookupSlug(slug string) (*doc_binding.Binding, error)
}

// sender 抽出投递接口，单测录 channel_id/type/payload 断言，不真调 IM。
type sender interface {
	SendMessage(req *config.MsgSendReq) error
}

// Receiver 是 doc_event_receiver module 的 HTTP handler。
type Receiver struct {
	ctx     *config.Context
	token   string // env 读一次冻结；空值 → endpoint 全 503
	lookup  bindingLookup
	sender  sender
	fromUID string
	log.Log
}

// realLookup 生产 lookup：直接调用 doc_binding.LookupSlug。
type realLookup struct{ ctx *config.Context }

func (r *realLookup) LookupSlug(slug string) (*doc_binding.Binding, error) {
	return doc_binding.LookupSlug(r.ctx, slug)
}

// realSender 生产投递：走 ctx.SendMessage（内部 → IM /message/send）。
type realSender struct{ ctx *config.Context }

func (r *realSender) SendMessage(req *config.MsgSendReq) error {
	return r.ctx.SendMessage(req)
}

// New 构造 Receiver。token 未配置也构造成功（Route 挂上，请求进来一律 503），
// 这样运维在部署后再补 env 无需重启进程注入 endpoint。
func New(ctx *config.Context) *Receiver {
	return &Receiver{
		ctx:     ctx,
		token:   strings.TrimSpace(os.Getenv(tokenEnv)),
		lookup:  &realLookup{ctx: ctx},
		sender:  &realSender{ctx: ctx},
		fromUID: docBotUID,
		Log:     log.NewTLog("doc_event_receiver"),
	}
}

// Route 挂 endpoint。**不套 AuthMiddleware**——鉴权走内部 token 头 X-Octo-Doc-Webhook-Token。
func (r *Receiver) Route(rt *wkhttp.WKHttp) {
	rt.POST(routePath, r.handle)
}

// docEventReq 是 doc → server 回调的 JSON schema。字段与 issue 描述契约一致。
type docEventReq struct {
	EventType string `json:"event_type"`
	Slug      string `json:"slug"`
	Actor     struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
	} `json:"actor"`
	Doc struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"doc"`
	Comment struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		CreatedAt string `json:"created_at"`
	} `json:"comment"`
}

// handle 是唯一 endpoint 的入口。
//
// 分支返回码：
//   - 503: env token 未配（服务端拒收，doc 端应停投）
//   - 401: header token 缺失 / 不匹配
//   - 400: body 读失败 / JSON 坏 / 关键字段缺失
//   - 202: 未知 event_type / 未知 slug / space 挂载 / 未来扩展位（doc 端不重试）
//   - 200: 投递成功
//   - 502: 下游 IM 投递失败（transient；doc 侧按需重试）
func (r *Receiver) handle(c *wkhttp.Context) {
	// 1) env token 闸：先判 server 侧是否具备鉴权能力，缺则 503 且不揭示更多信息。
	if r.token == "" {
		r.Warn("doc webhook 未配 token，全部事件拒收", zap.String(tokenEnv, ""))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	// 2) header token 比较：constant time 防旁道；长度不同也不短路。
	got := c.GetHeader(tokenHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(r.token)) != 1 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	// 3) 读 body，硬上限 64KB 防对端把大 payload 塞过来撑内存。
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes+1))
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if len(raw) > maxBodyBytes {
		c.AbortWithStatus(http.StatusRequestEntityTooLarge)
		return
	}
	var req docEventReq
	if err := json.Unmarshal(raw, &req); err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Slug) == "" || strings.TrimSpace(req.EventType) == "" {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// 4) event_type 白名单：本单只落 comment.created；其他 202 忽略并 log，
	//    让 doc 端别把新事件当成端点坏了拼命重试。
	if req.EventType != eventTypeCommentCreated {
		r.Info("event_type 未启用，忽略",
			zap.String("event_type", req.EventType), zap.String("slug", req.Slug))
		c.Status(http.StatusAccepted)
		return
	}

	// 5) slug → binding 反查。未命中 / 无绑定：202 忽略（可能是刚解绑的旧 slug）。
	bind, err := r.lookup.LookupSlug(req.Slug)
	if err != nil {
		r.Error("查 slug binding 失败", zap.String("slug", req.Slug), zap.Error(err))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if bind == nil {
		r.Info("slug 无 binding，忽略", zap.String("slug", req.Slug))
		c.Status(http.StatusAccepted)
		return
	}

	// 6) 挂载类型 → 投递坐标。space 暂不落地投递（方案未定），202 + warn。
	channelID, channelType, ok := resolveChannel(bind)
	if !ok {
		r.Warn("space 挂载暂不落地 IM 投递，忽略",
			zap.String("slug", req.Slug), zap.String("mount", bind.MountType))
		c.Status(http.StatusAccepted)
		return
	}

	// 7) 组卡片文案 + 投递。fromUID 若为空（无 sys bot）也照发，log warn 便于排查。
	if r.fromUID == "" {
		r.Warn("doc 消息 from_uid 为空，仍尝试发送", zap.String("slug", req.Slug))
	}
	payload := buildCommentPayload(&req)
	if err := r.sender.SendMessage(&config.MsgSendReq{
		Header:      config.MsgHeader{RedDot: 1},
		ChannelID:   channelID,
		ChannelType: channelType,
		FromUID:     r.fromUID,
		Payload:     []byte(util.ToJson(payload)),
	}); err != nil {
		r.Error("投递 doc 评论到 IM 失败",
			zap.String("slug", req.Slug), zap.String("channel_id", channelID),
			zap.Uint8("channel_type", channelType), zap.Error(err))
		c.AbortWithStatus(http.StatusBadGateway)
		return
	}
	c.Status(http.StatusOK)
}

// resolveChannel 把 mount_type + binding 字段 → (channelID, channelType, 是否投递)。
// ok=false：本单不投递 (space)；调用端返 202 并 log。
func resolveChannel(b *doc_binding.Binding) (string, uint8, bool) {
	switch b.MountType {
	case doc_binding.MountTypeGroup:
		return b.GroupNo, common.ChannelTypeGroup.Uint8(), true
	case doc_binding.MountTypeThread:
		return thread.BuildChannelID(b.GroupNo, b.ThreadId), common.ChannelTypeCommunityTopic.Uint8(), true
	case doc_binding.MountTypeSpace:
		return "", 0, false
	default:
		return "", 0, false
	}
}

// buildCommentPayload 组 IM Text (ContentType=1) 消息体。
// content 是 markdown 风格文案（客户端 webhook 消息按 markdown 渲染，纯文本时退化）。
// actor.name/doc.title/comment.text 一律 HTML escape 防 XSS；text 超 200 rune 截断。
func buildCommentPayload(req *docEventReq) map[string]interface{} {
	actor := html.EscapeString(strings.TrimSpace(req.Actor.Name))
	if actor == "" {
		actor = "有人"
	}
	title := html.EscapeString(strings.TrimSpace(req.Doc.Title))
	if title == "" {
		title = "文档"
	}
	text := truncateRunes(strings.TrimSpace(req.Comment.Text), commentPreviewRunes)
	text = html.EscapeString(text)
	url := strings.TrimSpace(req.Doc.URL)

	var content string
	if url != "" {
		content = fmt.Sprintf(`[doc 评论] %s 在 <a href="%s">%s</a> 评论了`+"\n> %s",
			actor, html.EscapeString(url), title, text)
	} else {
		content = fmt.Sprintf("[doc 评论] %s 在 %s 评论了\n> %s", actor, title, text)
	}

	return map[string]interface{}{
		"type":    int(common.Text),
		"content": content,
		"from": map[string]interface{}{
			"kind":       "doc_event",
			"event_type": req.EventType,
			"slug":       req.Slug,
			"comment_id": req.Comment.ID,
		},
	}
}

// truncateRunes 按 rune 截断，避免切到多字节字符中段；超出末尾加 …。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "…"
}
