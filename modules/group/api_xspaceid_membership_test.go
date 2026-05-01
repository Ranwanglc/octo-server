package group

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// YUJ-201 / GH#1268 — X-Space-ID header membership 校验（纵深防御）。
//
// 背景：YUJ-199 让后端 groupScanJoin / addMembersTxWithSpace 读 X-Space-ID
// header 作为 source_space_id 的首选来源，但没有做 membership 校验。client
// 若伪造一个 scaner / operator 并非成员的 Space ID，后端会直接写进
// group_member.source_space_id。下游 SpaceFilter 会在可见性层拒绝这类
// 不可见的外部群，实际影响较小，但写脏数据是**纵深防御缺口**，本补丁补齐。
//
// 测试风格沿用 api_source_space_header_test.go 的源码 grep 断言：
//   - scanjoin / invite-sure handler 都会触达 ctx.EventBegin，而 testutil
//     目前不初始化 wkevent 子系统（Mininglamp-OSS/octo-server#1184 TODO），任何
//     HTTP 驱动测试会 nil-deref panic。
//   - 在 testutil 升级之前只能用源码 grep 锁住关键实现特征，防回归。
//   - 每个测试都带「不做 X 就回归」的语义说明，便于后续重构。

// Test 1: groupScanJoin 的 X-Space-ID header 非成员分支必须降级成空串并
// 通过 Warn 日志记录（不抛 5xx、不阻断主流程、不把脏数据写 DB）。
func TestGroupScanJoin_XSpaceIDHeaderNotMember_FallsBackAndLogs(t *testing.T) {
	body := mustReadFunc(t, "api.go", "func (g *Group) groupScanJoin(")

	// A. scanjoin 必须调用 spacepkg.CheckMembership(..., scaner) 校验 header
	//    指向的 Space 是否收 scaner。参数顺序与 package 其它调用一致
	//    (db, spaceID, uid)，避免悄悄反调走人。
	assert.Regexp(t,
		regexp.MustCompile(`spacepkg\.CheckMembership\(\s*g\.ctx\.DB\(\)\s*,\s*headerSpaceID\s*,\s*scaner\s*\)`),
		body,
		"groupScanJoin 必须用 spacepkg.CheckMembership 校验 X-Space-ID 是否收 scaner")

	// B. 校验失败（!inSpace）必须走 g.Warn 路径，并且在同一个块内把
	//    sourceSpaceID 清空，给下面的 home 兜底让位。
	//    日志 key 用固定文案 "scanjoin X-Space-ID not member"，方便线上 grep。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)!\s*inSpace\b[^{]*\{[^}]*g\.Warn\(\s*"scanjoin X-Space-ID not member[^"]*"`),
		body,
		"groupScanJoin 非成员分支必须 Warn 且文案以 'scanjoin X-Space-ID not member' 起头")

	// C. 非成员降级后必须**回落到 home Space**，与 header 空的历史行为等价。
	//    也就是说 Warn 之后的代码路径必须能落到 GetUserDefaultSpaceID(scaner)。
	warnIdx := strings.Index(body, `"scanjoin X-Space-ID not member, ignoring"`)
	homeIdx := strings.Index(body, "GetUserDefaultSpaceID(g.ctx, scaner)")
	assert.NotEqual(t, -1, warnIdx, "Warn 文案缺失 → 非成员分支回归")
	assert.NotEqual(t, -1, homeIdx, "home 兜底缺失 → 非成员降级后会写空串 source_space_id")
	assert.Less(t, warnIdx, homeIdx,
		"Warn 必须先于 home 兜底出现在函数体内，否则降级路径跑不到兜底")

	// D. CheckMembership 返回 error 时也必须降级（不阻断主流程），防止 DB
	//    瞬时抖动把整条 scanjoin 路径打挂。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)membershipErr\s*!=\s*nil[^}]*g\.Error\(\s*"扫码入群 X-Space-ID[^"]*"`),
		body,
		"groupScanJoin 必须在 CheckMembership 出错时 Error 记日志并降级，不抛 5xx")
}

// Test 2: groupScanJoin 的 X-Space-ID header 合法成员路径必须照常使用
// header 作为 source_space_id（不能因为加了校验就无脑兜底 home）。
func TestGroupScanJoin_XSpaceIDHeaderValidMember_UsesIt(t *testing.T) {
	body := mustReadFunc(t, "api.go", "func (g *Group) groupScanJoin(")

	// A. header 非空分支必须**先**把 headerSpaceID 赋给 sourceSpaceID
	//    （代表合法 case 的默认走向）；membership 校验失败时才清空。
	//    如果有人把 `sourceSpaceID = headerSpaceID` 挪到 CheckMembership
	//    之后，就会把 inSpace==true 的合法 case 也 fallback，回归 YUJ-199。
	assignIdx := strings.Index(body, "sourceSpaceID = headerSpaceID")
	checkIdx := strings.Index(body, "spacepkg.CheckMembership(g.ctx.DB(), headerSpaceID, scaner)")
	assert.NotEqual(t, -1, assignIdx, "header 合法 case 的 sourceSpaceID = headerSpaceID 赋值缺失 → YUJ-199 回归")
	assert.NotEqual(t, -1, checkIdx, "CheckMembership 缺失 → YUJ-201 回归")
	assert.Less(t, assignIdx, checkIdx,
		"sourceSpaceID = headerSpaceID 必须出现在 CheckMembership 之前，"+
			"否则 inSpace==true 的合法 case 会被跳过赋值、错误兜底到 home")

	// B. header 合法 case 不能在 !inSpace 分支中被清空之外再次覆盖，
	//    即 !inSpace 后必须退出赋值分支，让 home 兜底条件 `sourceSpaceID == ""`
	//    生效。断言「sourceSpaceID = "" // 降级」注释在 !inSpace 之后。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)!\s*inSpace\b[^}]*sourceSpaceID\s*=\s*""`),
		body,
		"非成员分支必须把 sourceSpaceID 清空（降级），否则 header 伪造依旧会落进 DB")

	// C. 合法成员 case 不走任何兜底；通过 `if sourceSpaceID == ""` 这个兜底
	//    入口的存在 + 它被摆在 header 块之后，来证明合法 case 不会被覆盖。
	fallbackGateIdx := strings.Index(body, `if sourceSpaceID == ""`)
	assert.NotEqual(t, -1, fallbackGateIdx,
		"home 兜底入口 `if sourceSpaceID == \"\"` 缺失 → header 为空 / 非成员降级后没法回落 home")
	assert.Less(t, assignIdx, fallbackGateIdx,
		"home 兜底判定必须在 header 赋值之后，保证合法 case 不会被兜底覆盖")
}

// Test 3: addMembersTxWithSpace 的邀请确认路径必须校验 operator 是否是
// inviterSpaceID 的成员；不是则降级清空，让后面 switch 走 operator/home 兜底。
func TestAddMembersTxWithSpace_OperatorNotInClaimedSpace_FallsBack(t *testing.T) {
	body := mustReadFunc(t, "api.go", "func (g *Group) addMembersTxWithSpace(")

	// A. 必须用 spacepkg.CheckMembership(..., operator) 校验 operator 是否
	//    是 inviterSpaceID 的成员。参数顺序与其它调用一致，避免反调。
	assert.Regexp(t,
		regexp.MustCompile(`spacepkg\.CheckMembership\(\s*g\.ctx\.DB\(\)\s*,\s*inviterSpaceID\s*,\s*operator\s*\)`),
		body,
		"addMembersTxWithSpace 必须用 spacepkg.CheckMembership 校验 operator 在 inviterSpaceID 内")

	// B. 校验必须发生在使用 inviterSpaceID 的 switch 之**前**，而且必须提到
	//    使用 inviterSpaceID 的 members 循环之**外**（否则随成员数线性放大
	//    查询次数、或循环里用脏的 inviterSpaceID 写了 sourceSpaceMap）。
	//    使用 inviterSpaceID 的 members 循环是**紧邻 switch 的那个**，
	//    用 LastIndex 精确定位，避免误匹配到 allow_external=0 检查的第一个循环。
	checkIdx := strings.Index(body, "spacepkg.CheckMembership(g.ctx.DB(), inviterSpaceID, operator)")
	switchIdx := strings.Index(body, "case inviterSpaceID != \"\":")
	// 使用 inviterSpaceID 的循环必定出现在 switch 之前，且是最靠近 switch 的循环。
	inviterLoopIdx := -1
	if switchIdx != -1 {
		inviterLoopIdx = strings.LastIndex(body[:switchIdx], "for _, uid := range members {")
	}
	assert.NotEqual(t, -1, checkIdx, "CheckMembership 缺失 → YUJ-201 回归")
	assert.NotEqual(t, -1, switchIdx, "三段 switch 缺失 → YUJ-199 回归")
	assert.NotEqual(t, -1, inviterLoopIdx, "使用 inviterSpaceID 的 members 遍历循环消失 → 更大范围回归")
	assert.Less(t, checkIdx, inviterLoopIdx,
		"CheckMembership 必须在使用 inviterSpaceID 的 for 循环之前完成，避免 N 次重复 space_member 查询")
	assert.Less(t, checkIdx, switchIdx,
		"CheckMembership 必须在 switch 分支之前，保证降级后的空串能被 switch 捕获走兜底")

	// C. 非成员分支必须把 inviterSpaceID 清空（同一作用域的变量）+ Warn，
	//    且 Warn 文案以 "addmembers X-Space-ID not member" 起头，方便运维 grep。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)!\s*inSpace\b[^}]*g\.Warn\(\s*"addmembers X-Space-ID not member[^"]*"[^}]*inviterSpaceID\s*=\s*""`),
		body,
		"addMembersTxWithSpace 非成员分支必须 Warn 并把 inviterSpaceID 清空")

	// D. DB 错误也必须降级（不 return err 阻断邀请），否则 DB 抖动会把整条
	//    群邀请打挂，线上 P0。
	assert.Regexp(t,
		regexp.MustCompile(`(?s)membershipErr\s*!=\s*nil[^}]*g\.Error\(\s*"邀请确认 X-Space-ID[^"]*"[^}]*inviterSpaceID\s*=\s*""`),
		body,
		"addMembersTxWithSpace 的 CheckMembership 出错必须 Error + 降级，不抛 err")

	// E. 这些新断言不能推翻 YUJ-199 已保住的三段 switch 优先级顺序：
	//    inviterSpaceID > operatorMemberForSpace.SourceSpaceID > home。
	priIdx := strings.Index(body, "sourceSpaceMap[uid] = inviterSpaceID")
	opIdx := strings.Index(body, "operatorMemberForSpace.SourceSpaceID")
	homeIdx := strings.Index(body, "GetUserDefaultSpaceID(g.ctx, uid)")
	assert.NotEqual(t, -1, priIdx, "YUJ-199 inviterSpaceID 优先写入分支缺失 → 合并冲突")
	assert.NotEqual(t, -1, opIdx, "YUJ-58 operator 外部兜底分支缺失")
	assert.NotEqual(t, -1, homeIdx, "被邀请者 home 兜底缺失")
	assert.Less(t, priIdx, opIdx, "inviterSpaceID 优先级必须高于 operator 外部兜底")
	assert.Less(t, opIdx, homeIdx, "operator 外部兜底必须高于被邀请者 home")
}

// 防御性正交断言：api.go 必须仍 import spacepkg。grep 级。若未来有人
// 无意中把校验搬到别处、导致 spacepkg 只剩测试引用（甚至被 goimports 清理），
// 该断言会先失败，给 reviewer 一个醒目警报。
func TestAPI_StillImportsSpacepkg_YUJ201(t *testing.T) {
	src, err := os.ReadFile("api.go")
	assert.NoError(t, err)
	assert.Contains(t, string(src), `spacepkg "github.com/Mininglamp-OSS/octo-server/pkg/space"`,
		"api.go 必须保留 spacepkg import；CheckMembership 的核心路径依赖它")
}
