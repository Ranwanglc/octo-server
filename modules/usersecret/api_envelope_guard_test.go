package usersecret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveNoRawErrorResponse 是源码守卫:本包所有非测试源文件的错误响应必须走
// 统一 i18n 错误信封(respondErr / respondErrWithDetails → httperr.ResponseErrorLWithStatus),
// 不得出现裸 c.JSON(http.Status…)/c.AbortWith* —— 与 modules/oidc 的同款守卫一致,
// 防止 resolve 歧义这类分支再回退到绕过信封的裸响应(R1/R2 blocker 的回归闸)。
//
// 扫描范围覆盖整包(*.go 去掉 *_test.go),不只 api.go:错误响应可能拆到 handler
// 辅助文件,只盯单文件会留盲点(lml2468 R3 非阻塞建议)。
func TestResolveNoRawErrorResponse(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package go files: %v", err)
	}

	bannedSubstr := []string{
		"c.AbortWithStatusJSON", "c.AbortWithStatus(",
		".ResponseError(", ".ResponseErrorf(", ".ResponseErrorWithStatus(",
	}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		cleaned := stripLineComments(string(data))

		for _, b := range bannedSubstr {
			if strings.Contains(cleaned, b) {
				t.Fatalf("modules/usersecret/%s must route errors through the i18n envelope, not legacy %s", f, b)
			}
		}
		// 裸 non-OK c.JSON(http.Status…) 同样绕过信封;成功响应走 c.Response/c.ResponseWithStatus。
		for _, line := range strings.Split(cleaned, "\n") {
			if strings.Contains(line, "c.JSON(http.Status") && !strings.Contains(line, "c.JSON(http.StatusOK") {
				t.Fatalf("modules/usersecret/%s must not emit raw non-OK c.JSON: %s", f, strings.TrimSpace(line))
			}
		}
	}
}

// stripLineComments 去掉行注释,避免注释里的示例文本误触发守卫。
func stripLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
