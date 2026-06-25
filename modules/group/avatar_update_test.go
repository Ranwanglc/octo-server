package group

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/require"
)

// putGroupUpdate 以 group 创建者身份 PUT /v1/groups/:group_no 改群信息。
func putGroupUpdate(t *testing.T, h http.Handler, groupNo string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req, err := http.NewRequest("PUT", "/v1/groups/"+groupNo, bytes.NewReader([]byte(util.ToJson(body))))
	require.NoError(t, err)
	req.Header.Set("token", testutil.Token)
	h.ServeHTTP(w, req)
	return w
}

// seedCreatorGroup 建一个由 testutil.UID（=token 对应用户）创建的群，并写入创建者
// 成员行，使其能通过 groupUpdate 的管理员校验。
func seedCreatorGroup(t *testing.T, g *Group, groupNo, name string) {
	t.Helper()
	require.NoError(t, g.db.Insert(&Model{GroupNo: groupNo, Name: name, Creator: testutil.UID, Status: 1}))
	require.NoError(t, g.db.InsertMember(&MemberModel{
		GroupNo: groupNo, UID: testutil.UID, Role: MemberRoleCreator, Status: 1,
	}))
}

// TestGroupUpdateAvatarCustom 覆盖改群接口设置/清除自定义头像文字与颜色的落库。
func TestGroupUpdateAvatarCustom(t *testing.T) {
	s, ctx := newTestServer(t)
	require.NoError(t, testutil.CleanAllTables(ctx))
	g := New(ctx)

	const groupNo = "avatar_upd_1"
	seedCreatorGroup(t, g, groupNo, "原始群名")

	// 设置自定义文字 + 颜色。
	w := putGroupUpdate(t, s.GetRoute(), groupNo, map[string]string{
		attrKeyAvatarText:  "研发",
		attrKeyAvatarColor: "5",
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	got, err := g.db.QueryWithGroupNo(groupNo)
	require.NoError(t, err)
	require.Equal(t, "研发", got.AvatarText)
	require.NotNil(t, got.AvatarColor)
	require.Equal(t, 5, *got.AvatarColor)

	// 只改颜色，文字保持现值。
	w = putGroupUpdate(t, s.GetRoute(), groupNo, map[string]string{attrKeyAvatarColor: "8"})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got, err = g.db.QueryWithGroupNo(groupNo)
	require.NoError(t, err)
	require.Equal(t, "研发", got.AvatarText, "未提供 avatar_text 时文字应保持")
	require.NotNil(t, got.AvatarColor)
	require.Equal(t, 8, *got.AvatarColor)

	// 清除自定义（文字空串、颜色 -1）→ 回退默认派生。
	w = putGroupUpdate(t, s.GetRoute(), groupNo, map[string]string{
		attrKeyAvatarText:  "",
		attrKeyAvatarColor: "-1",
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got, err = g.db.QueryWithGroupNo(groupNo)
	require.NoError(t, err)
	require.Equal(t, "", got.AvatarText)
	require.Nil(t, got.AvatarColor, "清除后颜色应为 NULL")
}

// TestGroupUpdateAvatarCustomValidation 覆盖改群接口对自定义头像参数的校验。
func TestGroupUpdateAvatarCustomValidation(t *testing.T) {
	s, ctx := newTestServer(t)
	require.NoError(t, testutil.CleanAllTables(ctx))
	g := New(ctx)

	const groupNo = "avatar_upd_val_1"
	seedCreatorGroup(t, g, groupNo, "校验群")

	cases := []struct {
		name string
		body map[string]string
	}{
		{"text over 4 visible runes", map[string]string{attrKeyAvatarText: "一二三四五"}},
		{"color out of range", map[string]string{attrKeyAvatarColor: "99"}},
		{"color non-numeric", map[string]string{attrKeyAvatarColor: "abc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := putGroupUpdate(t, s.GetRoute(), groupNo, tc.body)
			// D14 兼容：错误响应统一 wire 400。
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}

	// 非法请求不得落库。
	got, err := g.db.QueryWithGroupNo(groupNo)
	require.NoError(t, err)
	require.Equal(t, "", got.AvatarText)
	require.Nil(t, got.AvatarColor)
}
