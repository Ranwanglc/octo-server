package space

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// issue #344 · 空间成员列表展示名兜底 + 实名字段契约
//
// 根因：user.name 在 RegisterUserMustCompleteInfoOn==1 时允许为空（本地注册与
// OIDC 建号同走 createUserWithRespAndTx 的空名分支），queryMembers 直接渲染
// user.name，空名成员在 GET /v1/space/:space_id/members 显示为空字符串。
//
// 修复契约（对齐 modules/group YUJ-413 的既有口径）：
//  1. memberResp 下发 realname_verified / real_name / realname_verified_at
//     三字段，JSON tag 与 group memberDetailResp 逐字一致；
//  2. name 按 displayname.Resolve 兜底：name → real_name（仅已实名）→ 占位名；
//  3. 实名记录批量查询（零 N+1），失败仅 log 不阻断成员列表。
//
// 断言分两层（同 group/api_realname_test.go）：
//  1. 源码级 grep 锁定 —— 任何环境都能跑；
//  2. 真 HTTP 端到端 —— 需要 testutil 的 MySQL/Redis/WuKongIM 栈。
// =============================================================================

// --- 源码级锁定（无 DB 依赖） ---

// TestMemberResp_HasRealnameFields_Contract 锁住 memberResp 必须带实名三字段，
// JSON tag 与 modules/group memberDetailResp（YUJ-413）完全对齐。
func TestMemberResp_HasRealnameFields_Contract(t *testing.T) {
	src, err := os.ReadFile("model.go")
	require.NoError(t, err)
	body := string(src)

	startIdx := strings.Index(body, "type memberResp struct {")
	require.NotEqual(t, -1, startIdx, "memberResp struct 不见了？")
	endIdx := strings.Index(body[startIdx:], "\n}")
	require.NotEqual(t, -1, endIdx)
	block := body[startIdx : startIdx+endIdx]

	assert.Regexp(t,
		regexp.MustCompile("RealnameVerified\\s+bool\\s+`json:\"realname_verified\"`"),
		block, "memberResp 缺 realname_verified（tag 必须与 group memberDetailResp 对齐）")
	assert.Regexp(t,
		regexp.MustCompile("RealName\\s+string\\s+`json:\"real_name,omitempty\"`"),
		block, "memberResp 缺 real_name（tag 必须与 group memberDetailResp 对齐）")
	assert.Regexp(t,
		regexp.MustCompile("RealnameVerifiedAt\\s+int64\\s+`json:\"realname_verified_at,omitempty\"`"),
		block, "memberResp 缺 realname_verified_at（tag 必须与 group memberDetailResp 对齐）")
}

// TestListMembers_UsesDisplayNameFallback_Contract 锁住 listMembers 必须：
//   - 经 displayname.Resolve 兜底 name（issue #344 的展示层修复点）；
//   - 走批量 queryVerificationsByUIDs 取实名记录（零 N+1）；
//   - 不允许出现单查 QueryByUID（N+1 回归）。
func TestListMembers_UsesDisplayNameFallback_Contract(t *testing.T) {
	src, err := os.ReadFile("api.go")
	require.NoError(t, err)
	body := string(src)

	startIdx := strings.Index(body, "func (s *Space) listMembers(")
	require.NotEqual(t, -1, startIdx, "listMembers handler 不见了？")
	endIdx := strings.Index(body[startIdx:], "\nfunc ")
	require.NotEqual(t, -1, endIdx)
	block := body[startIdx : startIdx+endIdx]

	assert.Contains(t, block, "displayname.Resolve(",
		"listMembers 必须经 displayname.Resolve 兜底空 name（issue #344）")
	assert.Contains(t, block, "queryVerificationsByUIDs(",
		"listMembers 必须批量查实名记录（零 N+1，对齐 group fillRealnameFields）")
	assert.NotContains(t, block, "QueryByUID(",
		"listMembers 不允许逐成员单查实名记录（N+1 回归）")
}

// --- 真 HTTP 端到端（需要 MySQL） ---

// seedSpaceMemberUser 往裁剪版 user 表插一行（TestMain 建表，列见 api_test.go）。
func seedSpaceMemberUser(t *testing.T, uid, name string) {
	t.Helper()
	_, err := testCtx.DB().InsertBySql(
		"INSERT INTO `user` (uid, name) VALUES (?, ?)", uid, name,
	).Exec()
	require.NoError(t, err)
}

// seedSpaceUserVerification 往 user_verification 表写一条已实名记录
// （schema 对齐 modules/user/sql/20260505000003_user_legacy01.sql）。
func seedSpaceUserVerification(t *testing.T, uid, realName string, verifiedAt time.Time) {
	t.Helper()
	_, err := testCtx.DB().InsertBySql(
		"INSERT INTO user_verification (user_id, real_name, source, source_sub, verified_at) "+
			"VALUES (?, ?, 'aegis', ?, ?)",
		uid, realName, "sub-"+uid, verifiedAt,
	).Exec()
	require.NoError(t, err)
}

// setupDisplayNameSpace 建一个空间：登录用户 + 三类目标成员
//   - m-named-0001：user.name 正常
//   - m-verified-0002：user.name 为空 + user_verification.real_name="张三"
//   - m-blank-0003：user.name 为空且未实名
func setupDisplayNameSpace(t *testing.T) (f *Space, spaceId string) {
	_, f, err := setup(t)
	require.NoError(t, err)

	spaceId = "sp-displayname-344"
	require.NoError(t, f.db.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId,
		Name:    "展示名兜底测试",
		Creator: testutil.UID,
		Status:  1,
	}))

	seedSpaceMemberUser(t, testutil.UID, "operator")
	seedSpaceMemberUser(t, "m-named-0001", "正常用户")
	seedSpaceMemberUser(t, "m-verified-0002", "")
	seedSpaceMemberUser(t, "m-blank-0003", "")
	seedSpaceUserVerification(t, "m-verified-0002", "张三",
		time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC))

	for i, uid := range []string{testutil.UID, "m-named-0001", "m-verified-0002", "m-blank-0003"} {
		role := 0
		if i == 0 {
			role = 2
		}
		require.NoError(t, f.db.insertMemberNoTx(&MemberModel{
			SpaceId: spaceId,
			UID:     uid,
			Role:    role,
			Status:  1,
		}))
	}
	return f, spaceId
}

func getMembers(t *testing.T, spaceId string) []map[string]interface{} {
	t.Helper()
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/space/"+spaceId+"/members?limit=100", nil)
	require.NoError(t, err)
	req.Header.Set("token", testutil.Token)
	testSrv.GetRoute().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var members []map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &members))
	return members
}

// TestListMembers_DisplayNameFallback：GET /v1/space/:space_id/members
// 任何成员的 name 都不允许为空（issue #344 的用户可见症状）：
//   - name 正常成员原样返回，realname_verified=false；
//   - 空名已实名成员回退 real_name，且带实名三字段；
//   - 空名未实名成员回退占位名（"用户"+uid 后 4 位）。
func TestListMembers_DisplayNameFallback(t *testing.T) {
	_, spaceId := setupDisplayNameSpace(t)
	members := getMembers(t, spaceId)
	require.Len(t, members, 4, "成员数不对")

	byUID := map[string]map[string]interface{}{}
	for _, m := range members {
		uid, _ := m["uid"].(string)
		byUID[uid] = m
		name, _ := m["name"].(string)
		assert.NotEqual(t, "", strings.TrimSpace(name),
			"uid=%s 的 name 不允许为空（issue #344）", uid)
	}

	named := byUID["m-named-0001"]
	require.NotNil(t, named)
	assert.Equal(t, "正常用户", named["name"])
	assert.Equal(t, false, named["realname_verified"])
	if v, ok := named["real_name"]; ok {
		assert.Equal(t, "", v, "未实名成员不应下发 real_name")
	}

	verified := byUID["m-verified-0002"]
	require.NotNil(t, verified)
	assert.Equal(t, "张三", verified["name"], "空名已实名成员的 name 必须回退 real_name")
	assert.Equal(t, true, verified["realname_verified"])
	assert.Equal(t, "张三", verified["real_name"])
	ts, ok := verified["realname_verified_at"].(float64)
	require.True(t, ok, "已实名成员必须带 realname_verified_at; body=%v", verified)
	assert.Greater(t, ts, float64(0))

	blank := byUID["m-blank-0003"]
	require.NotNil(t, blank)
	assert.Equal(t, "用户0003", blank["name"],
		"空名未实名成员的 name 必须回退占位名（用户+uid 后 4 位）")
	assert.Equal(t, false, blank["realname_verified"])
}

// TestListMembers_NormalNameUnchanged：实名记录存在但 user.name 非空时，
// name 保持用户自取名 —— real_name 只兜底，不覆盖。
func TestListMembers_NormalNameUnchanged(t *testing.T) {
	_, f, err := setup(t)
	require.NoError(t, err)

	spaceId := "sp-displayname-keep"
	require.NoError(t, f.db.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId,
		Name:    "自取名优先测试",
		Creator: testutil.UID,
		Status:  1,
	}))
	seedSpaceMemberUser(t, testutil.UID, "operator")
	seedSpaceMemberUser(t, "m-both-0009", "网名小王")
	seedSpaceUserVerification(t, "m-both-0009", "王五",
		time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC))
	for i, uid := range []string{testutil.UID, "m-both-0009"} {
		role := 0
		if i == 0 {
			role = 2
		}
		require.NoError(t, f.db.insertMemberNoTx(&MemberModel{
			SpaceId: spaceId, UID: uid, Role: role, Status: 1,
		}))
	}

	members := getMembers(t, spaceId)
	var both map[string]interface{}
	for _, m := range members {
		if m["uid"] == "m-both-0009" {
			both = m
		}
	}
	require.NotNil(t, both)
	assert.Equal(t, "网名小王", both["name"], "name 非空时不允许被 real_name 覆盖")
	assert.Equal(t, true, both["realname_verified"])
	assert.Equal(t, "王五", both["real_name"])
}
