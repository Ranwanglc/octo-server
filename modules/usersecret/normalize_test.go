package usersecret

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeName_SpacesAndCase(t *testing.T) {
	// 去空格 + 小写折叠:三种写法判为同一别名。
	n1 := normalizeName("Claude 密钥")
	n2 := normalizeName("claude密钥")
	n3 := normalizeName("  CLAUDE   密钥 ")
	assert.Equal(t, n1, n2)
	assert.Equal(t, n1, n3)
}

func TestNormalizeName_TraditionalSimplified(t *testing.T) {
	// 简繁归一:克勞德密鑰 == 克劳德密钥。
	assert.Equal(t, normalizeName("克劳德密钥"), normalizeName("克勞德密鑰"))
}

func TestMatchScore_Exact(t *testing.T) {
	assert.Equal(t, 2, matchScore("Claude密钥", "claude 密钥"))
}

func TestMatchScore_PinyinVoice(t *testing.T) {
	// 语音场景:用户说「克劳德密钥」命中存的同写法变体。
	assert.Equal(t, 2, matchScore("克劳德密钥", "克劳德密钥"))
	// 繁体说法命中简体存储。
	assert.GreaterOrEqual(t, matchScore("克勞德密鑰", "克劳德密钥"), 1)
}

func TestMatchScore_PinyinFuzzy(t *testing.T) {
	// 「米要」与「密钥」拼音同为 miyao,模糊命中。
	assert.GreaterOrEqual(t, matchScore("我的米要", "我的密钥"), 1)
}

func TestMatchScore_NoMatch(t *testing.T) {
	assert.Equal(t, 0, matchScore("OpenAI key", "Claude 密钥"))
}

func TestPinyinKey_KeepsNonHan(t *testing.T) {
	// 英文原样保留(小写),汉字转拼音。
	assert.Equal(t, "claudemiyao", pinyinKey("Claude 密钥"))
}
