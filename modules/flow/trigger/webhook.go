// Package trigger 实现 Flow 触发器（webhook / cron / manual）。
//
// 每个触发器在收到事件后构造 TriggerData 并调用 engine.StartExecution。
// 触发器自己不感知节点，只负责"把外部事件翻译成 flow 的一次启动"。
package trigger

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// VerifyWebhookSignature 校验 HMAC-SHA256 签名。
//
// 约定与 GitHub 兼容：header 值形如 "sha256=<hex>"。
//
// 返回 nil 表示通过。secret 为空时跳过校验（部分 webhook 不开签名）。
func VerifyWebhookSignature(body []byte, secret, headerValue, algo string) error {
	if secret == "" {
		return nil
	}
	if headerValue == "" {
		return errors.New("webhook: missing signature header")
	}
	switch strings.ToLower(algo) {
	case "", "hmac-sha256", "sha256":
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		// 也兼容裸 hex
		if hmac.Equal([]byte(expected), []byte(headerValue)) {
			return nil
		}
		if hmac.Equal([]byte(expected[len("sha256="):]), []byte(headerValue)) {
			return nil
		}
		return errors.New("webhook: signature mismatch")
	default:
		return errors.New("webhook: unsupported algo " + algo)
	}
}
