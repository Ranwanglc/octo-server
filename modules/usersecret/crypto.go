package usersecret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// 加密说明(YUJ-3538 §2):
//
// 不自造轮子。沿用 octo-server 已有的对称加密做法 —— 与 modules/botfather 的
// user-api-key 加密(service_userapikey_crypto.go)完全同源:
//   - 主密钥:环境变量 OCTO_USER_API_KEY_SECRET(32 字节),与 botfather 共用同一
//     落点。这是本仓库现有「用户外部密钥」加密的既定主密钥,复用它让别名密文与
//     已有 uk_ 体系处在同一密钥域,运维只需管理一把主密钥。
//   - 子密钥派生:HMAC-SHA256(masterKey, domain),domain 用本模块独立的
//     userSecretCipherDomain,与 botfather/oidc 的子密钥在密码学上相互独立
//     (同主密钥派生不同子密钥,避免跨原语复用)。
//   - AEAD:AES-256-GCM,密文 = nonce(12B) || ciphertext || tag(16B),
//     外层再加版本前缀 cipherVersionPrefix,便于未来轮换密钥/算法时识别版本。
//
// 主密钥轮换:与 botfather user-api-key 一致 —— 当前为单密钥(无内建多版本轮换),
// 轮换需运维替换 OCTO_USER_API_KEY_SECRET 并对存量密文重新加密。版本前缀
// (enc:v1:)为未来引入多版本主密钥预留了识别位。详见交付文档「主密钥落点」一节。
const (
	cipherVersionPrefix    = "enc:v1:"
	userSecretSecretEnv    = "OCTO_USER_API_KEY_SECRET"
	userSecretCipherDomain = "octo-user-secret-alias-cipher-v1"
)

// errSecretKeyUnset 主密钥缺失/长度非法。归类为 5xx:这是部署配置问题,不是
// 用户输入问题,handler 据此返回内部错误而非 4xx。
var errSecretKeyUnset = fmt.Errorf("%s must be exactly 32 bytes", userSecretSecretEnv)

// encryptor 封装别名密文的加解密,主密钥在构造期解析一次并缓存派生子密钥。
type encryptor struct {
	gcm cipher.AEAD
}

// newEncryptor 读取主密钥并构造 AES-256-GCM AEAD。
//
// 主密钥缺失/长度非法直接返错,由 Init 决定是否阻断启动:本模块在 Init 阶段
// 探测一次,缺失则不挂载写接口路由(见 1module.go / api.go),避免运行期才暴雷。
func newEncryptor() (*encryptor, error) {
	master, err := masterKey()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte(userSecretCipherDomain))
	sub := mac.Sum(nil) // 32B 子密钥
	block, err := aes.NewCipher(sub)
	if err != nil {
		return nil, fmt.Errorf("usersecret: aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("usersecret: cipher.NewGCM: %w", err)
	}
	return &encryptor{gcm: gcm}, nil
}

// encrypt 加密明文,返回带版本前缀的密文字节(存 varbinary)。
func (e *encryptor) encrypt(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return nil, errors.New("usersecret: empty plaintext")
	}
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("usersecret: rand nonce: %w", err)
	}
	sealed := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(cipherVersionPrefix)+len(sealed))
	out = append(out, []byte(cipherVersionPrefix)...)
	out = append(out, sealed...)
	return out, nil
}

// decrypt 解密 encrypt 产生的密文,认证失败/前缀错误返回错误。
func (e *encryptor) decrypt(cipherText []byte) (string, error) {
	if len(cipherText) < len(cipherVersionPrefix) ||
		!strings.HasPrefix(string(cipherText[:len(cipherVersionPrefix)]), cipherVersionPrefix) {
		return "", errors.New("usersecret: unknown cipher prefix")
	}
	raw := cipherText[len(cipherVersionPrefix):]
	ns := e.gcm.NonceSize()
	if len(raw) < ns+e.gcm.Overhead() {
		return "", errors.New("usersecret: cipher too short")
	}
	plaintext, err := e.gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("usersecret: gcm open: %w", err)
	}
	return string(plaintext), nil
}

// masterKey 读取并校验主密钥(32 字节)。与 botfather userAPIKeySecret 同语义。
func masterKey() ([]byte, error) {
	secret := []byte(os.Getenv(userSecretSecretEnv))
	if len(secret) != 32 {
		return nil, errSecretKeyUnset
	}
	return secret, nil
}

// maskTail 取明文尾 4 位做掩码展示(list 用,免解密)。
//
// 短于 4 位时全部用 * 替换长度,避免泄漏长度敏感信息又不至于回显原文。
func maskTail(plaintext string) string {
	r := []rune(plaintext)
	if len(r) <= 4 {
		return strings.Repeat("*", len(r))
	}
	return "****" + string(r[len(r)-4:])
}
