package user

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/Mininglamp-OSS/octo-server/pkg/avatarrender"
	"github.com/Mininglamp-OSS/octo-server/pkg/displayname"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// issue #344 · 默认头像与成员列表展示名兜底的一致性
//
// 成员列表对空 user.name 兜底为 real_name / 占位名后，默认头像若仍用裸
// userInfo.Name 渲染（空名 → Renderable=false → uid ASCII 兜底图），同一个
// 用户会出现「列表名是张三、头像却是 uid 字符图」的两面不一致。
//
// 契约：UserAvatar 的昵称渲染分支必须经同一条 displayname.Resolve 链取展示名。
// 断言走 ETag —— name-v3 模式的 ETag 含展示文字因子，能精确证明头像内容
// 来源于兜底名，而无需解码 PNG。
// =============================================================================

// avatarGetRaw 直接打 GET /v1/users/:uid/avatar，返回 recorder。
func avatarGetRaw(t *testing.T, handler http.Handler, uid string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/users/"+uid+"/avatar", nil)
	require.NoError(t, err)
	req.Header.Set("token", testutil.Token)
	handler.ServeHTTP(w, req)
	return w
}

func seedAvatarUserVerification(t *testing.T, u *User, uid, realName string) {
	t.Helper()
	_, err := u.ctx.DB().InsertBySql(
		"INSERT INTO user_verification (user_id, real_name, source, source_sub, verified_at) "+
			"VALUES (?, ?, 'aegis', ?, ?)",
		uid, realName, "sub-"+uid, time.Date(2026, 5, 11, 10, 0, 0, 0, time.UTC),
	).Exec()
	require.NoError(t, err)
}

// TestUserAvatar_BlankNameVerified_RendersRealName：空名已实名用户的默认头像
// 必须按 real_name 渲染（ETag = name-v3 + real_name 后两字），与成员列表
// 显示的兜底名一致，而不是回退 uid ASCII 兜底图。
func TestUserAvatar_BlankNameVerified_RendersRealName(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx) // 路由已由 NewTestServer 挂载（user 模块 init 注册），重复 Route 会 panic

	const uid = "av_blank_verified_01"
	require.NoError(t, u.db.Insert(&Model{
		UID: uid, Name: "", Username: "av_bv_01", ShortNo: "av_bv_sn01", Status: 1,
	}))
	seedAvatarUserVerification(t, u, uid, "张三")

	w := avatarGetRaw(t, s.GetRoute(), uid)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))

	wantText := avatarrender.IndividualText("张三")
	assert.Equal(t, avatarETag("name-v3", uid, wantText), w.Header().Get("ETag"),
		"空名已实名用户的头像必须按 real_name 渲染（与列表兜底名一致）")
}

// TestUserAvatar_BlankNameUnverified_RendersPlaceholder：空名未实名用户的
// 默认头像必须按占位名渲染（与列表显示的「用户+uid 后 4 位」一致），
// 不再走 ascii-v1 的 uid 字符兜底图。
func TestUserAvatar_BlankNameUnverified_RendersPlaceholder(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx) // 路由已由 NewTestServer 挂载（user 模块 init 注册），重复 Route 会 panic

	const uid = "av_blank_plain_0007"
	require.NoError(t, u.db.Insert(&Model{
		UID: uid, Name: "", Username: "av_bp_07", ShortNo: "av_bp_sn07", Status: 1,
	}))

	w := avatarGetRaw(t, s.GetRoute(), uid)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))

	wantText := avatarrender.IndividualText(displayname.Resolve("", "", uid))
	assert.Equal(t, avatarETag("name-v3", uid, wantText), w.Header().Get("ETag"),
		"空名未实名用户的头像必须按占位名渲染（与列表兜底名一致）")
}

// TestUserAvatar_NormalName_NotOverriddenByRealName：name 非空时头像保持按
// 用户自取名渲染 —— real_name 只兜底，不覆盖（与列表语义一致）。
func TestUserAvatar_NormalName_NotOverriddenByRealName(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx) // 路由已由 NewTestServer 挂载（user 模块 init 注册），重复 Route 会 panic

	const uid = "av_named_keep_0008"
	require.NoError(t, u.db.Insert(&Model{
		UID: uid, Name: "网名小王", Username: "av_nk_08", ShortNo: "av_nk_sn08", Status: 1,
	}))
	seedAvatarUserVerification(t, u, uid, "王五")

	w := avatarGetRaw(t, s.GetRoute(), uid)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	wantText := avatarrender.IndividualText("网名小王")
	assert.Equal(t, avatarETag("name-v3", uid, wantText), w.Header().Get("ETag"),
		"name 非空时头像不允许被 real_name 覆盖")
}
