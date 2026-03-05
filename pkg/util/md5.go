package util

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// Deprecated: MD5 is cryptographically broken. Use SHA256 for new code.
// Kept for backward compatibility with existing password hashes in database.
func MD5(str string) string {
	h := md5.New()
	h.Write([]byte(str)) // 需要加密的字符串
	passwordmdsBys := h.Sum(nil)
	return hex.EncodeToString(passwordmdsBys)
}

// Deprecated: SHA1 is cryptographically weak. Use SHA256 for new code.
func SHA1(str string) string {
	h := sha1.New()
	h.Write([]byte(str))
	bs := h.Sum(nil)
	return hex.EncodeToString(bs)
}

// SHA256 returns the SHA-256 hex digest of the input string.
func SHA256Hex(str string) string {
	h := sha256.New()
	h.Write([]byte(str))
	bs := h.Sum(nil)
	return hex.EncodeToString(bs)
}

// Deprecated: HMACSHA1 uses SHA1 which is cryptographically weak. Use HMACSHA256 instead.
func HMACSHA1(keyStr string, data string) string {
	//hmac ,use sha1
	key := []byte(keyStr)
	mac := hmac.New(sha1.New, key)
	mac.Write([]byte(data))
	srcBytes := mac.Sum(nil)
	return base64.StdEncoding.EncodeToString(srcBytes)
}

// HMACSHA256 computes HMAC using SHA-256 and returns base64-encoded result.
func HMACSHA256(keyStr string, data string) string {
	key := []byte(keyStr)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	srcBytes := mac.Sum(nil)
	return base64.StdEncoding.EncodeToString(srcBytes)
}
