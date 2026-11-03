package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"go.uber.org/zap"
)

const (
	envWriteGateAutopostEnabled   = "WRITEGATE_AUTOPOST_ENABLED"
	envWriteGateAutopostURL       = "WRITEGATE_AUTOPOST_URL"
	envWriteGateAutopostTimeoutMS = "WRITEGATE_AUTOPOST_TIMEOUT_MS"

	defaultWriteGateAutopostTimeout = 8 * time.Second
)

var writeGateAutopostMissingURLOnce sync.Once

type writeGateAutopostConfig struct {
	enabled bool
	url     string
	timeout time.Duration
}

type writeGateAutopostBody struct {
	Content     string `json:"content"`
	SourceAgent string `json:"source_agent"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	MessageID   int64  `json:"message_id"`
	Timestamp   int32  `json:"timestamp"`
	ContentType int    `json:"content_type"`
	// TODO: add compact channel context when write-gate has a stable context contract.
	Context string `json:"context,omitempty"`
}

func (w *Webhook) autopostWriteGateMessages(messages []MsgResp) bool {
	cfg := loadWriteGateAutopostConfig()
	if !cfg.enabled {
		return false
	}
	if cfg.url == "" {
		writeGateAutopostMissingURLOnce.Do(func() {
			log.Warn("WRITEGATE_AUTOPOST_URL未配置，write-gate自动投递已禁用")
		})
		return false
	}

	// best-effort 旁路：后续过滤、JSON 提取和 HTTP 全部在后台执行。
	messages = append([]MsgResp(nil), messages...)
	go postWriteGateAutopostMessages(cfg, messages)
	return true
}

func loadWriteGateAutopostConfig() writeGateAutopostConfig {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(envWriteGateAutopostEnabled)))
	if err != nil {
		enabled = false
	}

	timeout := defaultWriteGateAutopostTimeout
	if v := strings.TrimSpace(os.Getenv(envWriteGateAutopostTimeoutMS)); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			timeout = time.Duration(ms) * time.Millisecond
		}
	}

	return writeGateAutopostConfig{
		enabled: enabled,
		url:     strings.TrimSpace(os.Getenv(envWriteGateAutopostURL)),
		timeout: timeout,
	}
}

func buildWriteGateAutopostBody(message MsgResp) (writeGateAutopostBody, bool) {
	if message.Header.SyncOnce == 1 || message.Header.NoPersist == 1 {
		return writeGateAutopostBody{}, false
	}
	if config.SettingFromUint8(message.Setting).Signal {
		return writeGateAutopostBody{}, false
	}

	content, contentType, ok := extractWriteGateAutopostContent(message)
	if !ok || strings.TrimSpace(content) == "" {
		return writeGateAutopostBody{}, false
	}

	return writeGateAutopostBody{
		Content:     content,
		SourceAgent: message.FromUID,
		ChannelID:   message.ChannelID,
		ChannelType: message.ChannelType,
		MessageID:   message.MessageID,
		Timestamp:   message.Timestamp,
		ContentType: int(contentType),
	}, true
}

func extractWriteGateAutopostContent(message MsgResp) (string, common.ContentType, bool) {
	payload := message.PayloadMap
	if payload == nil {
		var err error
		payload, err = util.JsonToMap(string(message.Payload))
		if err != nil {
			return "", 0, false
		}
	}

	contentType := common.ContentType(message.ContentType)
	if contentType == 0 {
		contentType = writeGatePayloadContentType(payload)
	}

	switch contentType {
	case common.Text:
		content, ok := payload["content"].(string)
		return content, contentType, ok
	case common.RichText:
		content := common.GetRichTextDisplayText(message.Payload)
		if content == common.GetDisplayText(common.RichText.Int()) {
			return "", 0, false
		}
		return content, contentType, true
	default:
		return "", 0, false
	}
}

func writeGatePayloadContentType(payload map[string]interface{}) common.ContentType {
	switch v := payload["type"].(type) {
	case json.Number:
		i, err := v.Int64()
		if err == nil {
			return common.ContentType(i)
		}
	case float64:
		return common.ContentType(int(v))
	case int:
		return common.ContentType(v)
	}
	return 0
}

func postWriteGateAutopostMessages(cfg writeGateAutopostConfig, messages []MsgResp) {
	defer func() {
		if r := recover(); r != nil {
			log.Warn("write-gate自动投递panic已恢复", zap.Any("panic", r), zap.ByteString("stack", debug.Stack()))
		}
	}()

	bodies := make([]writeGateAutopostBody, 0, len(messages))
	for _, message := range messages {
		body, ok := buildWriteGateAutopostBody(message)
		if ok {
			bodies = append(bodies, body)
		}
	}
	if len(bodies) == 0 {
		return
	}

	// 独立 ctx，避免 HTTP 请求结束取消 best-effort 旁路投递。
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	client := &http.Client{Timeout: cfg.timeout}
	for _, body := range bodies {
		if err := postWriteGateAutopostMessage(ctx, client, cfg.url, body); err != nil {
			log.Warn("write-gate自动投递失败（忽略，best-effort）", zap.Error(err),
				zap.Int64("messageID", body.MessageID), zap.String("channelID", body.ChannelID))
		}
	}
}

func postWriteGateAutopostMessage(ctx context.Context, client *http.Client, url string, body writeGateAutopostBody) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &writeGateAutopostStatusError{statusCode: resp.StatusCode}
	}
	return nil
}

type writeGateAutopostStatusError struct {
	statusCode int
}

func (e *writeGateAutopostStatusError) Error() string {
	return "write-gate autopost returned status " + strconv.Itoa(e.statusCode)
}
