package displayname

import "testing"

// TestResolve 锁定展示名解析链（issue #344）：
//
//	name（用户自取名）→ real_name（仅已实名）→ "用户"+uid 后 4 位（占位名）
func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		userName string
		realName string
		uid      string
		want     string
	}{
		{
			name:     "name 非空时原样返回，real_name 不参与",
			userName: "张小明",
			realName: "张三",
			uid:      "uid-1234",
			want:     "张小明",
		},
		{
			name:     "name 非空时不做 trim 改写",
			userName: " 张小明 ",
			realName: "",
			uid:      "uid-1234",
			want:     " 张小明 ",
		},
		{
			name:     "name 为空回退 real_name",
			userName: "",
			realName: "张三",
			uid:      "uid-1234",
			want:     "张三",
		},
		{
			name:     "name 纯空白视为空，回退 real_name",
			userName: "   ",
			realName: "张三",
			uid:      "uid-1234",
			want:     "张三",
		},
		{
			name:     "name 与 real_name 均为空时生成占位名（uid 后 4 位）",
			userName: "",
			realName: "",
			uid:      "1234567890abcdef",
			want:     "用户cdef",
		},
		{
			name:     "real_name 纯空白同样视为空",
			userName: "",
			realName: " \t",
			uid:      "1234567890abcdef",
			want:     "用户cdef",
		},
		{
			name:     "uid 不足 4 位时整个 uid 作为后缀",
			userName: "",
			realName: "",
			uid:      "ab",
			want:     "用户ab",
		},
		{
			name:     "全部为空时仅返回占位前缀",
			userName: "",
			realName: "",
			uid:      "",
			want:     "用户",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Resolve(tt.userName, tt.realName, tt.uid); got != tt.want {
				t.Errorf("Resolve(%q, %q, %q) = %q, want %q",
					tt.userName, tt.realName, tt.uid, got, tt.want)
			}
		})
	}
}
