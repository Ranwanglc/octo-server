package file

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAllowedExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		want bool
	}{
		// 图片格式
		{"jpg allowed", ".jpg", true},
		{"jpeg allowed", ".jpeg", true},
		{"png allowed", ".png", true},
		{"gif allowed", ".gif", true},
		{"bmp allowed", ".bmp", true},
		{"webp allowed", ".webp", true},
		{"ico allowed", ".ico", true},

		// 文档格式
		{"pdf allowed", ".pdf", true},
		{"doc allowed", ".doc", true},
		{"docx allowed", ".docx", true},
		{"xls allowed", ".xls", true},
		{"xlsx allowed", ".xlsx", true},
		{"ppt allowed", ".ppt", true},
		{"pptx allowed", ".pptx", true},
		{"txt allowed", ".txt", true},
		{"csv allowed", ".csv", true},

		// 音频格式
		{"mp3 allowed", ".mp3", true},
		{"wav allowed", ".wav", true},
		{"aac allowed", ".aac", true},
		{"flac allowed", ".flac", true},
		{"amr allowed", ".amr", true},

		// 视频格式
		{"mp4 allowed", ".mp4", true},
		{"avi allowed", ".avi", true},
		{"mov allowed", ".mov", true},
		{"mkv allowed", ".mkv", true},

		// 压缩包
		{"zip allowed", ".zip", true},
		{"rar allowed", ".rar", true},
		{"7z allowed", ".7z", true},
		{"tar allowed", ".tar", true},

		// 其他允许
		{"json allowed", ".json", true},
		{"xml allowed", ".xml", true},
		{"apk allowed", ".apk", true},
		{"ipa allowed", ".ipa", true},

		// 被禁止的可执行文件 — IsAllowedExtension 应返回 false
		{"exe blocked", ".exe", false},
		{"bat blocked", ".bat", false},
		{"sh blocked", ".sh", false},
		{"cmd blocked", ".cmd", false},
		{"msi blocked", ".msi", false},
		{"dll blocked", ".dll", false},
		{"vbs blocked", ".vbs", false},
		{"ps1 blocked", ".ps1", false},
		{"php blocked", ".php", false},
		{"jsp blocked", ".jsp", false},
		{"py blocked", ".py", false},
		{"rb blocked", ".rb", false},
		{"js blocked", ".js", false},

		// 未知扩展名
		{"unknown ext", ".xyz", false},
		{"unknown ext2", ".abc", false},
		{"empty ext", "", false},
		{"dot only", ".", false},

		// 大小写不敏感
		{"JPG uppercase", ".JPG", true},
		{"Png mixed case", ".Png", true},
		{"PDF uppercase", ".PDF", true},
		{"EXE uppercase blocked", ".EXE", false},
		{"Bat mixed case blocked", ".Bat", false},
		{"PHP uppercase blocked", ".PHP", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAllowedExtension(tt.ext)
			assert.Equal(t, tt.want, got, "IsAllowedExtension(%q)", tt.ext)
		})
	}
}

func TestIsBlockedExtension(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		want bool
	}{
		// 可执行文件
		{"exe blocked", ".exe", true},
		{"bat blocked", ".bat", true},
		{"sh blocked", ".sh", true},
		{"cmd blocked", ".cmd", true},
		{"msi blocked", ".msi", true},
		{"dll blocked", ".dll", true},
		{"com blocked", ".com", true},
		{"scr blocked", ".scr", true},
		{"pif blocked", ".pif", true},

		// 脚本文件
		{"vbs blocked", ".vbs", true},
		{"vbe blocked", ".vbe", true},
		{"js blocked", ".js", true},
		{"jse blocked", ".jse", true},
		{"wsf blocked", ".wsf", true},
		{"wsh blocked", ".wsh", true},
		{"ps1 blocked", ".ps1", true},

		// 系统文件
		{"sys blocked", ".sys", true},
		{"cpl blocked", ".cpl", true},
		{"inf blocked", ".inf", true},
		{"reg blocked", ".reg", true},

		// 服务端脚本
		{"php blocked", ".php", true},
		{"jsp blocked", ".jsp", true},
		{"asp blocked", ".asp", true},
		{"aspx blocked", ".aspx", true},
		{"cgi blocked", ".cgi", true},
		{"py blocked", ".py", true},
		{"rb blocked", ".rb", true},
		{"pl blocked", ".pl", true},

		// 不在黑名单中的
		{"jpg not blocked", ".jpg", false},
		{"pdf not blocked", ".pdf", false},
		{"mp4 not blocked", ".mp4", false},
		{"zip not blocked", ".zip", false},
		{"unknown not blocked", ".xyz", false},
		{"empty not blocked", "", false},

		// 大小写不敏感
		{"EXE uppercase", ".EXE", true},
		{"Bat mixed", ".Bat", true},
		{"PHP uppercase", ".PHP", true},
		{"JPG uppercase not blocked", ".JPG", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsBlockedExtension(tt.ext)
			assert.Equal(t, tt.want, got, "IsBlockedExtension(%q)", tt.ext)
		})
	}
}

func TestIsAllowedExtension_BlockedTakesPriority(t *testing.T) {
	// 确保被禁止的扩展名不会因为同时在允许列表中而被放行
	// （当前实现中 blocked 优先检查）
	for ext := range blockedExtensions {
		assert.False(t, IsAllowedExtension(ext),
			"blocked extension %q should not be allowed", ext)
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// 正常文件名
		{"normal filename", "test.jpg", "test.jpg"},
		{"filename with spaces", "my file.txt", "my file.txt"},
		{"chinese filename", "图片.png", "图片.png"},

		// 路径清理
		{"unix path", "/etc/passwd", "passwd"},
		{"windows path", "C:\\Users\\test.exe", "C:_Users_test.exe"}, // Linux: filepath.Base 不处理反斜杠，反斜杠后被替换为下划线
		{"relative path", "../../../etc/passwd", "passwd"},

		// 危险字符替换
		{"CRLF injection", "file\r\nname.jpg", "file__name.jpg"},
		{"CR only", "file\rname.jpg", "file_name.jpg"},
		{"LF only", "file\nname.jpg", "file_name.jpg"},
		{"null byte", "file\x00.jpg", "file_.jpg"},
		{"double quotes", "file\"name.jpg", "file_name.jpg"},
		{"control chars", "file\x01\x02.jpg", "file__.jpg"},
		{"tab char", "file\t.jpg", "file_.jpg"}, // \t (0x09) < 0x20, replaced by underscore
		{"bell char", "file\a.jpg", "file_.jpg"},

		// 空文件名
		{"empty string", "", "file"},
		{"dot only", ".", "file"},

		// 长文件名截断
		{"exactly 255 chars", strings.Repeat("a", 255), strings.Repeat("a", 255)},
		{"256 chars no ext", strings.Repeat("a", 256), strings.Repeat("a", 255)},
		{"long name with ext", strings.Repeat("a", 260) + ".jpg", strings.Repeat("a", 251) + ".jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			assert.Equal(t, tt.expected, got, "sanitizeFilename(%q)", tt.input)
		})
	}
}

func TestSanitizeFilename_PreservesExtension(t *testing.T) {
	// 验证长文件名截断时保留扩展名
	longName := strings.Repeat("x", 300) + ".pdf"
	result := sanitizeFilename(longName)
	assert.True(t, strings.HasSuffix(result, ".pdf"), "should preserve .pdf extension")
	assert.LessOrEqual(t, len([]rune(result)), 255, "should not exceed 255 runes")
}

func TestSanitizeFilename_ControlCharsReplacedWithUnderscore(t *testing.T) {
	// 所有 0x00-0x1F 的控制字符都应该被替换
	for c := rune(0); c < 0x20; c++ {
		input := "a" + string(c) + "b.txt"
		result := sanitizeFilename(input)
		assert.NotContains(t, result, string(c),
			"control char 0x%02x should be replaced", c)
	}
}

func TestFileTypeConstants(t *testing.T) {
	// 验证文件类型常量值
	assert.Equal(t, Type("chat"), TypeChat)
	assert.Equal(t, Type("moment"), TypeMoment)
	assert.Equal(t, Type("momentcover"), TypeMomentCover)
	assert.Equal(t, Type("sticker"), TypeSticker)
	assert.Equal(t, Type("report"), TypeReport)
	assert.Equal(t, Type("common"), TypeCommon)
	assert.Equal(t, Type("chatbg"), TypeChatBg)
	assert.Equal(t, Type("workplacebanner"), TypeWorkplaceBanner)
	assert.Equal(t, Type("workplaceappicon"), TypeWorkplaceAppIcon)
}

func TestMaxFileSize(t *testing.T) {
	assert.Equal(t, int64(100*1024*1024), MaxFileSize, "MaxFileSize should be 100MB")
}

func TestAllowedExtensionsCompleteness(t *testing.T) {
	// 确保关键文件类型都在允许列表中
	criticalTypes := []string{
		".jpg", ".jpeg", ".png", ".gif",
		".pdf", ".doc", ".docx",
		".mp3", ".mp4",
		".zip",
	}
	for _, ext := range criticalTypes {
		assert.True(t, IsAllowedExtension(ext), "%s should be allowed", ext)
	}
}

func TestBlockedExtensionsCompleteness(t *testing.T) {
	// 确保所有危险的可执行文件类型都在禁止列表中
	dangerousTypes := []string{
		".exe", ".bat", ".sh", ".cmd", ".msi", ".dll",
		".vbs", ".ps1", ".php", ".jsp", ".asp",
	}
	for _, ext := range dangerousTypes {
		assert.True(t, IsBlockedExtension(ext), "%s should be blocked", ext)
	}
}

func TestIsAllowedExtension_DoubleExtensions(t *testing.T) {
	// 双扩展名 — filepath.Ext 只取最后一个扩展名，这里测试最终扩展名的判定
	tests := []struct {
		name string
		ext  string
		want bool
	}{
		{"final ext is jpg", ".jpg", true},
		{"final ext is pdf", ".pdf", true},
		{"final ext is exe", ".exe", false},
		{"final ext is php", ".php", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAllowedExtension(tt.ext)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsBlockedExtension_AllEntries(t *testing.T) {
	allBlocked := []string{
		".exe", ".bat", ".sh", ".cmd", ".msi", ".dll", ".com", ".scr", ".pif",
		".vbs", ".vbe", ".js", ".jse", ".wsf", ".wsh", ".ps1",
		".sys", ".cpl", ".inf", ".reg",
		".php", ".jsp", ".asp", ".aspx", ".cgi", ".py", ".rb", ".pl",
	}
	for _, ext := range allBlocked {
		assert.True(t, IsBlockedExtension(ext), "%s should be blocked", ext)
		assert.False(t, IsAllowedExtension(ext), "%s should not be allowed", ext)
	}
}

func TestIsAllowedExtension_AllEntries(t *testing.T) {
	allAllowed := []string{
		".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".ico",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".txt", ".csv", ".rtf", ".odt", ".ods",
		".mp3", ".wav", ".aac", ".flac", ".ogg", ".wma", ".m4a", ".amr",
		".mp4", ".avi", ".mov", ".wmv", ".flv", ".mkv", ".webm", ".m4v",
		".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz",
		".json", ".xml", ".yaml", ".yml", ".apk", ".ipa", ".log",
	}
	for _, ext := range allAllowed {
		assert.True(t, IsAllowedExtension(ext), "%s should be allowed", ext)
		assert.False(t, IsBlockedExtension(ext), "%s should not be blocked", ext)
	}
}

func TestSanitizeFilename_UTF8EdgeCases(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"emoji filename", "🎉🎊🎉.jpg"},
		{"mixed unicode", "résumé_文件.pdf"},
		{"just extension", ".jpg"},
		{"japanese", "画像ファイル.png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			assert.NotEmpty(t, result)
			assert.LessOrEqual(t, len([]rune(result)), 255)
		})
	}
}

func TestSanitizeFilename_BackslashHandling(t *testing.T) {
	result := sanitizeFilename("dir\\subdir\\file.txt")
	assert.NotContains(t, result, "\\")
	assert.Contains(t, result, "file.txt")
}

func TestMaxFileSize_Value(t *testing.T) {
	assert.Equal(t, int64(104857600), MaxFileSize)
	assert.Greater(t, MaxFileSize, int64(10*1024*1024))
	assert.LessOrEqual(t, MaxFileSize, int64(1024*1024*1024))
}

func TestSanitizeFilename_MultipleDotsInName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"multiple dots", "file.backup.2024.tar.gz", "file.backup.2024.tar.gz"},
		{"dot at start", ".hidden.txt", ".hidden.txt"},
		{"dots only", "...", "..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
