package usersecret

import (
	"strings"
	"sync"

	"github.com/longbridgeapp/opencc"
	pinyin "github.com/mozillazg/go-pinyin"
)

// 归一化 / 模糊匹配说明(YUJ-3538 §1、§4):
//
// 两层用途:
//   1. normalizeName —— display_name 的唯一性判重键(去空格 + 折叠大小写 + 简繁归一)。
//      存表时落 display_name_norm,撞了报错让用户换名。
//   2. pinyinKey —— resolve 的语音/拼音模糊匹配键。用户说「克劳德密钥」要能命中
//      存的「Claude 密钥」:对每个候选 display_name 取拼音首串比对,multica 已有
//      pinyin 搜索先例(packages/views/editor/extensions/pinyin-match),这里在
//      服务端用 mozillazg/go-pinyin 做等价能力。
//
// 简繁归一统一走 t2s(繁→简),保证「克勞德」与「克劳德」判重/匹配一致。

var (
	t2sOnce sync.Once
	t2s     *opencc.OpenCC
	t2sErr  error
)

func traditionalToSimplified(s string) string {
	t2sOnce.Do(func() {
		t2s, t2sErr = opencc.New("t2s")
	})
	if t2sErr != nil || t2s == nil {
		return s // 转换器不可用时降级为原文,不阻断功能
	}
	out, err := t2s.Convert(s)
	if err != nil {
		return s
	}
	return out
}

// normalizeName 计算 display_name 的判重键。
//
// 步骤:Trim → 内部连续空白折叠为单空格再全部去除 → 小写折叠 → 简繁归一(t2s)。
// 去空格让「Claude 密钥」「Claude密钥」「 claude  密钥 」判为同一别名。
func normalizeName(name string) string {
	s := strings.TrimSpace(name)
	// 去除所有空白(含中英文之间的空格),避免「Claude 密钥」vs「Claude密钥」漏判。
	s = strings.Join(strings.Fields(s), "")
	s = strings.ToLower(s)
	s = traditionalToSimplified(s)
	return s
}

// pinyinKey 计算用于拼音模糊匹配的键:简繁归一 + 去空格 + 小写,再把汉字转成
// 无声调拼音串拼接;非汉字字符原样保留(已小写)。
//
// 例:「Claude 密钥」→ "claudemiyao";「克劳德密钥」→ "kelaodemiyao"。
// 注意两者拼音串不同(一个含英文 claude,一个是音译克劳德的拼音),所以拼音键
// 用于「同一写法的语音变体」消歧,英译 vs 音译的跨形态匹配由调用方按需扩展。
func pinyinKey(name string) string {
	s := normalizeName(name) // 已去空格 + 小写 + 简繁归一
	a := pinyin.NewArgs()
	a.Style = pinyin.Normal
	// Fallback 保证非汉字 rune 原样进入结果(go-pinyin 默认丢弃非汉字)。
	a.Fallback = func(r rune, _ pinyin.Args) []string {
		return []string{string(r)}
	}
	var b strings.Builder
	for _, py := range pinyin.Pinyin(s, a) {
		if len(py) > 0 {
			b.WriteString(py[0])
		}
	}
	return b.String()
}

// matchScore 评估查询串 q 对候选 display_name 的匹配强度,返回:
//
//	2 = 精确(归一化后完全相等)
//	1 = 模糊(拼音键相等,或一方拼音键是另一方的前缀/包含 —— 语音场景宽松命中)
//	0 = 不匹配
//
// 分级让 resolve 优先采信精确命中:存在任一精确命中时只在精确集合里判唯一性,
// 避免模糊命中稀释「用户报了准确别名」的确定性。
func matchScore(q, displayName string) int {
	nq := normalizeName(q)
	nn := normalizeName(displayName)
	if nq == nn {
		return 2
	}
	pq := pinyinKey(q)
	pn := pinyinKey(displayName)
	if pq == "" || pn == "" {
		return 0
	}
	if pq == pn {
		return 1
	}
	if strings.Contains(pn, pq) || strings.Contains(pq, pn) {
		return 1
	}
	return 0
}
