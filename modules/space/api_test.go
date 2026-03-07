package space

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

func TestGetInvitePreview(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 创建测试 Space
	spaceId := "test-space-001"
	inviteCode := "abc12345"
	err = f.db.insertSpaceNoTx(&SpaceModel{
		SpaceId:     spaceId,
		Name:        "测试空间",
		Description: "这是一个测试空间描述",
		Logo:        "https://example.com/logo.png",
		Creator:     testutil.UID,
		Status:      1,
	})
	assert.NoError(t, err)

	// 添加空间成员
	err = f.db.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId,
		UID:     testutil.UID,
		Role:    2,
		Status:  1,
	})
	assert.NoError(t, err)

	// 创建邀请码
	err = f.db.insertInvitation(&InvitationModel{
		SpaceId:    spaceId,
		InviteCode: inviteCode,
		Creator:    testutil.UID,
		MaxUses:    10,
		UsedCount:  2,
		Status:     1,
	})
	assert.NoError(t, err)

	// 测试获取邀请预览（公开接口，无需 token）
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/space/invite/"+inviteCode+"/preview", nil)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"space_name":"测试空间"`)
	assert.Contains(t, body, `"description":"这是一个测试空间描述"`)
	assert.Contains(t, body, `"logo":"https://example.com/logo.png"`)
	assert.Contains(t, body, `"bots":`)
	assert.Contains(t, body, `"member_count":1`)
}

func TestGetInvitePreviewWithBots(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 创建测试 Space
	spaceId := "test-space-002"
	inviteCode := "xyz98765"
	err = f.db.insertSpaceNoTx(&SpaceModel{
		SpaceId:     spaceId,
		Name:        "带 Bot 的空间",
		Description: "测试 Bot 列表",
		Logo:        "",
		Creator:     testutil.UID,
		Status:      1,
	})
	assert.NoError(t, err)

	// 添加空间成员（人类用户）
	err = f.db.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId,
		UID:     testutil.UID,
		Role:    2,
		Status:  1,
	})
	assert.NoError(t, err)

	// 创建一个 Bot 用户
	botUID := "bot-001"
	_, err = ctx.DB().InsertInto("user").Columns("uid", "name", "avatar").
		Values(botUID, "AI 助手", "https://example.com/bot.png").Exec()
	assert.NoError(t, err)

	// 在 robot 表中注册 Bot
	_, err = ctx.DB().InsertInto("robot").Columns("robot_id", "token", "status").
		Values(botUID, "test-token", 1).Exec()
	assert.NoError(t, err)

	// 将 Bot 添加为空间成员
	err = f.db.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId,
		UID:     botUID,
		Role:    0,
		Status:  1,
	})
	assert.NoError(t, err)

	// 创建邀请码
	err = f.db.insertInvitation(&InvitationModel{
		SpaceId:    spaceId,
		InviteCode: inviteCode,
		Creator:    testutil.UID,
		Status:     1,
	})
	assert.NoError(t, err)

	// 测试获取邀请预览
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/space/invite/"+inviteCode+"/preview", nil)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"space_name":"带 Bot 的空间"`)
	assert.Contains(t, body, `"robot_id":"bot-001"`)
	assert.Contains(t, body, `"name":"AI 助手"`)
	assert.Contains(t, body, `"member_count":2`)
}

func TestGetInvitePreviewInvalidCode(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 测试无效邀请码
	w := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/v1/space/invite/invalid-code/preview", nil)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "邀请码无效")
}

func TestUpdateInvite(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 创建测试 Space
	spaceId := "test-space-003"
	inviteCode := "upd12345"
	err = f.db.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId,
		Name:    "更新邀请码测试",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	// 添加空间成员（管理员）
	err = f.db.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId,
		UID:     testutil.UID,
		Role:    1, // 管理员
		Status:  1,
	})
	assert.NoError(t, err)

	// 创建邀请码
	err = f.db.insertInvitation(&InvitationModel{
		SpaceId:    spaceId,
		InviteCode: inviteCode,
		Creator:    testutil.UID,
		MaxUses:    0,
		Status:     1,
	})
	assert.NoError(t, err)

	// 测试更新邀请码设置
	w := httptest.NewRecorder()
	req, err := http.NewRequest("PUT", "/v1/space/"+spaceId+"/invite/"+inviteCode,
		bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
			"max_uses":   100,
			"expires_at": "2026-12-31 23:59:59",
		}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// 验证更新生效
	invitation, err := f.db.queryInvitationByCode(inviteCode)
	assert.NoError(t, err)
	assert.NotNil(t, invitation)
	assert.Equal(t, 100, invitation.MaxUses)
	assert.NotNil(t, invitation.ExpiresAt)
	expiresAt := time.Time(*invitation.ExpiresAt)
	assert.Equal(t, 2026, expiresAt.Year())
	assert.Equal(t, time.December, expiresAt.Month())
	assert.Equal(t, 31, expiresAt.Day())
}

func TestUpdateInviteNoPermission(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 创建测试 Space
	spaceId := "test-space-004"
	inviteCode := "nop12345"
	err = f.db.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId,
		Name:    "权限测试",
		Creator: "other-user",
		Status:  1,
	})
	assert.NoError(t, err)

	// 添加空间成员（普通成员，Role=0）
	err = f.db.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId,
		UID:     testutil.UID,
		Role:    0,
		Status:  1,
	})
	assert.NoError(t, err)

	// 创建邀请码
	err = f.db.insertInvitation(&InvitationModel{
		SpaceId:    spaceId,
		InviteCode: inviteCode,
		Creator:    "other-user",
		Status:     1,
	})
	assert.NoError(t, err)

	// 测试普通成员尝试更新邀请码（应该失败）
	w := httptest.NewRecorder()
	req, err := http.NewRequest("PUT", "/v1/space/"+spaceId+"/invite/"+inviteCode,
		bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
			"max_uses": 50,
		}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "无权限")
}

func TestUpdateInviteInvalidCode(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	f := New(ctx)
	f.Route(s.GetRoute())

	// 清空旧数据
	err := testutil.CleanAllTables(ctx)
	assert.NoError(t, err)

	// 创建测试 Space
	spaceId := "test-space-005"
	err = f.db.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId,
		Name:    "无效邀请码测试",
		Creator: testutil.UID,
		Status:  1,
	})
	assert.NoError(t, err)

	// 添加空间成员（管理员）
	err = f.db.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId,
		UID:     testutil.UID,
		Role:    1,
		Status:  1,
	})
	assert.NoError(t, err)

	// 测试更新不存在的邀请码
	w := httptest.NewRecorder()
	req, err := http.NewRequest("PUT", "/v1/space/"+spaceId+"/invite/invalid-code",
		bytes.NewReader([]byte(util.ToJson(map[string]interface{}{
			"max_uses": 50,
		}))))
	req.Header.Set("token", testutil.Token)
	assert.NoError(t, err)
	s.GetRoute().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "邀请码不存在")
}
