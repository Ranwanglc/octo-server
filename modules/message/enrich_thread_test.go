package message

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// simulatePayloadFromWuKongIM 模拟 WuKongIM 返回消息经过 util.ReadJsonByByte（UseNumber）反序列化后的 payload
func simulatePayloadFromWuKongIM(raw string) map[string]interface{} {
	var m map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	_ = dec.Decode(&m)
	return m
}

func TestEnrichThreadCreatedMessages_JsonNumberType(t *testing.T) {
	// 模拟 WuKongIM 返回的 ThreadCreated 消息 payload
	// 经过 json.Decoder.UseNumber() 反序列化后，数字字段是 json.Number 而非 float64
	rawPayload := `{
		"type": 1100,
		"short_id": "2042843928016719872",
		"content": "测试用户 创建了子区「测试子区」",
		"message_count": 1,
		"thread_name": "测试子区"
	}`

	payload := simulatePayloadFromWuKongIM(rawPayload)

	// 验证前提：UseNumber 下 type 是 json.Number，不是 float64
	_, isFloat := payload["type"].(float64)
	assert.False(t, isFloat, "UseNumber 下 type 不应该是 float64")

	_, isJsonNumber := payload["type"].(json.Number)
	assert.True(t, isJsonNumber, "UseNumber 下 type 应该是 json.Number")

	// 构造消息列表
	messages := []*MsgSyncResp{
		{
			Payload: payload,
		},
	}

	// 用当前（有 bug 的）extractThreadShortIDs 逻辑提取 shortIDs
	shortIDs := extractThreadShortIDs(messages)

	// BUG 复现：当前代码用 float64 断言，json.Number 下提取不到任何 shortID
	// 修复后这里应该能提取到 "2042843928016719872"
	assert.Equal(t, []string{"2042843928016719872"}, shortIDs,
		"应能从 json.Number 类型的 payload 中正确提取 short_id")
}

func TestEnrichThreadCreatedMessages_Float64Type(t *testing.T) {
	// 模拟直接构造的 payload（非 UseNumber，数字是 float64）
	payload := map[string]interface{}{
		"type":          float64(1100),
		"short_id":      "2042843928016719872",
		"message_count": float64(1),
	}

	messages := []*MsgSyncResp{
		{
			Payload: payload,
		},
	}

	shortIDs := extractThreadShortIDs(messages)
	assert.Equal(t, []string{"2042843928016719872"}, shortIDs,
		"float64 类型也应正常工作")
}

func TestEnrichThreadCreatedMessages_SkipNonThreadMessages(t *testing.T) {
	// 非 ThreadCreated 消息（type != 1100）不应被提取
	payload := simulatePayloadFromWuKongIM(`{"type": 1, "short_id": "123"}`)

	messages := []*MsgSyncResp{
		{Payload: payload},
		{Payload: nil},
	}

	shortIDs := extractThreadShortIDs(messages)
	assert.Empty(t, shortIDs)
}
