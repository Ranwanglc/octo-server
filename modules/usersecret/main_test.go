package usersecret

import (
	"os"
	"testing"
)

// TestMain 确保集成测试启动前主密钥就位:
//   - OCTO_MASTER_KEY:common.Setup 加密 IM 私钥需要(testutil.NewTestServer 触发)。
//   - OCTO_USER_API_KEY_SECRET:本模块加密别名密文的主密钥(32 字节)。
//
// 已存在不覆盖,允许 CI 注入固定密钥。
func TestMain(m *testing.M) {
	if len(os.Getenv("OCTO_MASTER_KEY")) != 32 {
		os.Setenv("OCTO_MASTER_KEY", "0123456789abcdef0123456789abcdef") // 32 字节
	}
	if len(os.Getenv("OCTO_USER_API_KEY_SECRET")) != 32 {
		os.Setenv("OCTO_USER_API_KEY_SECRET", "0123456789abcdef0123456789abcdef")
	}
	os.Exit(m.Run())
}
