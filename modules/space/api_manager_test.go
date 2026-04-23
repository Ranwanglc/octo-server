package space

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	spacepkg "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
)

// adminToken 在 token 缓存中注入一个带 admin 角色的 token，供管理接口测试使用。
func adminToken(t *testing.T) string {
	t.Helper()
	token := "space-mgr-admin-token"
	cfg := testCtx.GetConfig()
	err := testCtx.Cache().Set(cfg.Cache.TokenCachePrefix+token, testutil.UID+"@admin@"+string(wkhttp.Admin))
	assert.NoError(t, err)
	return token
}

// readSpaceStatus 读取空间当前状态（测试辅助，不经过业务过滤）
func readSpaceStatus(t *testing.T, spaceId string) int {
	t.Helper()
	var status int
	_, err := testCtx.DB().SelectBySql("SELECT status FROM space WHERE space_id=?", spaceId).Load(&status)
	assert.NoError(t, err)
	return status
}

// seedSpace 插入一个测试空间 + owner，返回 spaceId。
func seedSpace(t *testing.T, spaceId, name, creator string, status int) {
	t.Helper()
	err := testSpaceDB.insertSpaceNoTx(&SpaceModel{
		SpaceId: spaceId,
		Name:    name,
		Creator: creator,
		Status:  status,
	})
	assert.NoError(t, err)
	err = testSpaceDB.insertMemberNoTx(&MemberModel{
		SpaceId: spaceId,
		UID:     creator,
		Role:    2,
		Status:  1,
	})
	assert.NoError(t, err)
}

func TestManager_SpaceList(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-space-001", "alpha team", "u-owner-1", 1)
	seedSpace(t, "mgr-space-002", "beta squad", "u-owner-2", 1)
	seedSpace(t, "mgr-space-disbanded", "gone space", "u-owner-3", 0)

	t.Run("full list excludes disbanded", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces?page_index=1&page_size=20", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Count int64 `json:"count"`
			List  []struct {
				SpaceId string `json:"space_id"`
				Name    string `json:"name"`
			} `json:"list"`
		}
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.EqualValues(t, 2, resp.Count)
		ids := map[string]bool{}
		for _, it := range resp.List {
			ids[it.SpaceId] = true
		}
		assert.True(t, ids["mgr-space-001"])
		assert.True(t, ids["mgr-space-002"])
		assert.False(t, ids["mgr-space-disbanded"])
	})

	t.Run("keyword filter", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces?keyword=alpha", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"space_id":"mgr-space-001"`)
		assert.NotContains(t, w.Body.String(), "mgr-space-002")
	})
}

func TestManager_DisableList(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-active", "live", "u-a", 1)
	seedSpace(t, "mgr-dead-1", "dead space 1", "u-b", 0)
	seedSpace(t, "mgr-dead-2", "dead space 2", "u-c", 0)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/manager/spaces/disabled?page_index=1&page_size=10", nil)
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count int64 `json:"count"`
		List  []struct {
			SpaceId string `json:"space_id"`
			Status  int    `json:"status"`
		} `json:"list"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 2, resp.Count)
	for _, it := range resp.List {
		assert.NotEqual(t, SpaceStatusNormal, it.Status, "disablelist should not include active spaces")
	}
}

func TestManager_SpaceDetail(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-detail", "detail space", "u-owner", 1)

	t.Run("active space returns detail", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces/mgr-detail", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, `"space_id":"mgr-detail"`)
		assert.Contains(t, body, `"name":"detail space"`)
		assert.Contains(t, body, `"member_count":1`)
	})

	t.Run("disbanded space still returns detail", func(t *testing.T) {
		seedSpace(t, "mgr-detail-dead", "dead one", "u-owner-x", 0)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces/mgr-detail-dead", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"status":0`)
	})

	t.Run("unknown space returns error", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces/does-not-exist", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusOK, w.Code)
	})
}

func TestManager_ForceDisband(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-force", "about to die", "u-owner", 1)
	err = testSpaceDB.insertMemberNoTx(&MemberModel{
		SpaceId: "mgr-force", UID: "u-member-1", Role: 0, Status: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/v1/manager/spaces/mgr-force", nil)
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	active, err := testSpaceDB.isSpaceActive("mgr-force")
	assert.NoError(t, err)
	assert.False(t, active, "space should be disbanded")

	count, err := testSpaceDB.countActiveMembers("mgr-force")
	assert.NoError(t, err)
	assert.Equal(t, 0, count, "all members should be removed")
}

func TestManager_MembersList(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-members", "members space", "u-owner", 1)
	for i := 0; i < 3; i++ {
		uid := fmt.Sprintf("u-m-%d", i)
		err = testSpaceDB.insertMemberNoTx(&MemberModel{
			SpaceId: "mgr-members", UID: uid, Role: 0, Status: 1,
		})
		assert.NoError(t, err)
	}
	// 已移除成员也应被管理后台看到
	err = testSpaceDB.insertMemberNoTx(&MemberModel{
		SpaceId: "mgr-members", UID: "u-m-removed", Role: 0, Status: 0,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/manager/spaces/mgr-members/members?page_index=1&page_size=20", nil)
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count int64 `json:"count"`
		List  []struct {
			UID    string `json:"uid"`
			Role   int    `json:"role"`
			Status int    `json:"status"`
		} `json:"list"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 5, resp.Count) // owner + 3 active + 1 removed
	assert.Len(t, resp.List, 5)
	// owner (role=2) 应排在最前
	assert.Equal(t, 2, resp.List[0].Role)
	assert.Equal(t, "u-owner", resp.List[0].UID)
}

// ==================== P1 tests ====================

func TestManager_LiftBan(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-ban-target", "to be banned", "u-owner", SpaceStatusNormal)

	t.Run("ban active space", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-ban-target/status/2", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		status := readSpaceStatus(t, "mgr-ban-target")
		assert.Equal(t, SpaceStatusBanned, status)
	})

	t.Run("unban banned space", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-ban-target/status/1", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		status := readSpaceStatus(t, "mgr-ban-target")
		assert.Equal(t, SpaceStatusNormal, status)
	})

	t.Run("reject invalid status", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-ban-target/status/7", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusOK, w.Code)
	})

	t.Run("reject ban on disbanded space", func(t *testing.T) {
		seedSpace(t, "mgr-ban-dead", "dead", "u-owner-d", SpaceStatusDisbanded)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-ban-dead/status/2", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "已解散")
	})
}

func TestManager_AddMembers(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-addmem", "add members", "u-owner", SpaceStatusNormal)

	body := util.ToJson(map[string]interface{}{
		"uids": []string{"new-u-1", "new-u-2"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/manager/spaces/mgr-addmem/members", bytes.NewReader([]byte(body)))
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	count, err := testSpaceDB.countActiveMembers("mgr-addmem")
	assert.NoError(t, err)
	assert.Equal(t, 3, count) // owner + 2 new

	t.Run("reactivate removed member", func(t *testing.T) {
		err := testSpaceDB.removeMember("mgr-addmem", "new-u-1")
		assert.NoError(t, err)

		body2 := util.ToJson(map[string]interface{}{"uids": []string{"new-u-1"}})
		w2 := httptest.NewRecorder()
		req2, _ := http.NewRequest("POST", "/v1/manager/spaces/mgr-addmem/members", bytes.NewReader([]byte(body2)))
		req2.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusOK, w2.Code)

		mem, err := testSpaceDB.queryMember("mgr-addmem", "new-u-1")
		assert.NoError(t, err)
		assert.NotNil(t, mem)
		assert.Equal(t, 1, mem.Status)
	})

	t.Run("bypass max_users cap", func(t *testing.T) {
		seedSpace(t, "mgr-capped", "tiny", "u-owner-c", SpaceStatusNormal)
		_, err := testCtx.DB().Update("space").Set("max_users", 2).Where("space_id=?", "mgr-capped").Exec()
		assert.NoError(t, err)
		// owner 已占 1，再加 3 个应超过 max=2，但管理员应绕过限制
		body3 := util.ToJson(map[string]interface{}{"uids": []string{"x1", "x2", "x3"}})
		w3 := httptest.NewRecorder()
		req3, _ := http.NewRequest("POST", "/v1/manager/spaces/mgr-capped/members", bytes.NewReader([]byte(body3)))
		req3.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w3, req3)
		assert.Equal(t, http.StatusOK, w3.Code)
		count, err := testSpaceDB.countActiveMembers("mgr-capped")
		assert.NoError(t, err)
		assert.Equal(t, 4, count)
	})
}

func TestManager_RemoveMembers(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-rm", "remove members", "u-owner", SpaceStatusNormal)
	for _, uid := range []string{"rm-1", "rm-2", "rm-3"} {
		err = testSpaceDB.insertMemberNoTx(&MemberModel{
			SpaceId: "mgr-rm", UID: uid, Role: 0, Status: 1,
		})
		assert.NoError(t, err)
	}

	body := util.ToJson(map[string]interface{}{"uids": []string{"rm-1", "rm-3"}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/v1/manager/spaces/mgr-rm/members", bytes.NewReader([]byte(body)))
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	count, err := testSpaceDB.countActiveMembers("mgr-rm")
	assert.NoError(t, err)
	assert.Equal(t, 2, count) // owner + rm-2

	mem, err := testSpaceDB.queryMember("mgr-rm", "rm-1")
	assert.NoError(t, err)
	assert.Nil(t, mem)

	t.Run("reject removing owner", func(t *testing.T) {
		body := util.ToJson(map[string]interface{}{"uids": []string{"u-owner"}})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("DELETE", "/v1/manager/spaces/mgr-rm/members", bytes.NewReader([]byte(body)))
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "拥有者")
		owner, err := testSpaceDB.queryMember("mgr-rm", "u-owner")
		assert.NoError(t, err)
		assert.NotNil(t, owner, "owner must remain active")
	})
}

func TestManager_UpdateMemberRole(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-role", "role ops", "u-owner", SpaceStatusNormal)
	err = testSpaceDB.insertMemberNoTx(&MemberModel{
		SpaceId: "mgr-role", UID: "m-target", Role: 0, Status: 1,
	})
	assert.NoError(t, err)

	t.Run("promote to admin", func(t *testing.T) {
		body := util.ToJson(map[string]interface{}{"role": 1})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-role/members/m-target/role", bytes.NewReader([]byte(body)))
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		mem, err := testSpaceDB.queryMember("mgr-role", "m-target")
		assert.NoError(t, err)
		assert.Equal(t, 1, mem.Role)
	})

	t.Run("reject demoting owner directly", func(t *testing.T) {
		// 此前已通过子测试 "promote to admin" 把 m-target 提到 admin
		// 先把 m-target 提成 owner 来构造"降级 owner"场景
		err := testSpaceDB.updateMemberRole("mgr-role", "m-target", 2)
		assert.NoError(t, err)

		body := util.ToJson(map[string]interface{}{"role": 0})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-role/members/m-target/role", bytes.NewReader([]byte(body)))
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "拥有者")

		mem, err := testSpaceDB.queryMember("mgr-role", "m-target")
		assert.NoError(t, err)
		assert.Equal(t, 2, mem.Role, "owner role must not be dropped")

		// 恢复：把 owner 转回 u-owner 以免影响后续子测试
		err = testSpaceDB.updateMemberRole("mgr-role", "m-target", 1)
		assert.NoError(t, err)
		err = testSpaceDB.updateMemberRole("mgr-role", "u-owner", 2)
		assert.NoError(t, err)
	})

	t.Run("transfer ownership demotes previous owner", func(t *testing.T) {
		body := util.ToJson(map[string]interface{}{"role": 2})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-role/members/m-target/role", bytes.NewReader([]byte(body)))
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		newOwner, err := testSpaceDB.queryMember("mgr-role", "m-target")
		assert.NoError(t, err)
		assert.Equal(t, 2, newOwner.Role)

		oldOwner, err := testSpaceDB.queryMember("mgr-role", "u-owner")
		assert.NoError(t, err)
		assert.Equal(t, 1, oldOwner.Role, "old owner demoted to admin")
	})
}

func TestManager_InvitesList(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-inv", "inv space", "u-owner", SpaceStatusNormal)
	for i, code := range []string{"inv-aaa111", "inv-bbb222", "inv-ccc333"} {
		status := 1
		if i == 2 {
			status = 0 // last one disabled
		}
		err = testSpaceDB.insertInvitation(&InvitationModel{
			SpaceId: "mgr-inv", InviteCode: code, Creator: "u-owner", Status: status,
		})
		assert.NoError(t, err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/manager/spaces/mgr-inv/invites", nil)
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count int64 `json:"count"`
		List  []struct {
			InviteCode string `json:"invite_code"`
			Status     int    `json:"status"`
		} `json:"list"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 3, resp.Count, "admin sees disabled invites too")
}

func TestManager_DisableInvite(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-inv-del", "inv delete", "u-owner", SpaceStatusNormal)
	err = testSpaceDB.insertInvitation(&InvitationModel{
		SpaceId: "mgr-inv-del", InviteCode: "inv-todel1", Creator: "u-owner", Status: 1,
	})
	assert.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/v1/manager/spaces/mgr-inv-del/invites/inv-todel1", nil)
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 业务端按 status=1 查找 → 应该找不到
	inv, err := testSpaceDB.queryInvitationByCode("inv-todel1")
	assert.NoError(t, err)
	assert.Nil(t, inv, "disabled invite should not be visible to business query")
}

func TestManager_JoinAppliesList(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-apply", "apply list", "u-owner", SpaceStatusNormal)
	for i, u := range []string{"app-u1", "app-u2", "app-u3"} {
		_, err = testSpaceDB.upsertJoinApply(&spaceJoinApplyModel{
			SpaceId: "mgr-apply", UID: u, InviteCode: "xyz",
		})
		assert.NoError(t, err)
		if i == 0 {
			// 把第一个置为 approved
			_, _ = testCtx.DB().Update("space_join_apply").
				Set("status", 1).
				Where("space_id=? AND uid=?", "mgr-apply", u).Exec()
		}
	}

	t.Run("default returns pending only", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces/mgr-apply/join-applies?status=0", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Count int64 `json:"count"`
		}
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.EqualValues(t, 2, resp.Count)
	})

	t.Run("all statuses", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces/mgr-apply/join-applies", nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		var resp struct {
			Count int64 `json:"count"`
		}
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.EqualValues(t, 3, resp.Count)
	})
}

func TestManager_ApproveAndReject(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-approve", "approve space", "u-owner", SpaceStatusNormal)

	t.Run("approve adds member", func(t *testing.T) {
		applyID, err := testSpaceDB.upsertJoinApply(&spaceJoinApplyModel{
			SpaceId: "mgr-approve", UID: "apply-u1", InviteCode: "c1",
		})
		assert.NoError(t, err)

		w := httptest.NewRecorder()
		url := fmt.Sprintf("/v1/manager/spaces/mgr-approve/join-applies/%d/approve", applyID)
		req, _ := http.NewRequest("POST", url, nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		mem, err := testSpaceDB.queryMember("mgr-approve", "apply-u1")
		assert.NoError(t, err)
		assert.NotNil(t, mem, "applicant should be added as member")

		apply, err := testSpaceDB.queryJoinApplyByID(applyID)
		assert.NoError(t, err)
		assert.Equal(t, 1, apply.Status)
	})

	t.Run("approve is idempotent on already-processed apply", func(t *testing.T) {
		applyID, err := testSpaceDB.upsertJoinApply(&spaceJoinApplyModel{
			SpaceId: "mgr-approve", UID: "apply-u1x", InviteCode: "c1x",
		})
		assert.NoError(t, err)

		// first approve
		w1 := httptest.NewRecorder()
		url := fmt.Sprintf("/v1/manager/spaces/mgr-approve/join-applies/%d/approve", applyID)
		req1, _ := http.NewRequest("POST", url, nil)
		req1.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w1, req1)
		assert.Equal(t, http.StatusOK, w1.Code)

		// second approve should fail (already processed)
		w2 := httptest.NewRecorder()
		req2, _ := http.NewRequest("POST", url, nil)
		req2.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w2, req2)
		assert.NotEqual(t, http.StatusOK, w2.Code)
	})

	t.Run("reject marks apply as rejected without adding member", func(t *testing.T) {
		applyID, err := testSpaceDB.upsertJoinApply(&spaceJoinApplyModel{
			SpaceId: "mgr-approve", UID: "apply-u2", InviteCode: "c2",
		})
		assert.NoError(t, err)

		w := httptest.NewRecorder()
		url := fmt.Sprintf("/v1/manager/spaces/mgr-approve/join-applies/%d/reject", applyID)
		req, _ := http.NewRequest("POST", url, nil)
		req.Header.Set("token", token)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		mem, err := testSpaceDB.queryMember("mgr-approve", "apply-u2")
		assert.NoError(t, err)
		assert.Nil(t, mem)

		apply, err := testSpaceDB.queryJoinApplyByID(applyID)
		assert.NoError(t, err)
		assert.Equal(t, 2, apply.Status)
	})
}

func TestManager_AuthBoundary(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)

	t.Run("no token returns 401", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces", nil)
		s.GetRoute().ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("non-admin token rejected", func(t *testing.T) {
		// testutil.Token 只有 uid@name，role 为空 → CheckLoginRole 应拒绝
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/v1/manager/spaces", nil)
		req.Header.Set("token", testutil.Token)
		s.GetRoute().ServeHTTP(w, req)
		assert.NotEqual(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "角色")
	})
}

func TestManager_DisableListIncludesBanned(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-dl-active", "a", "u1", SpaceStatusNormal)
	seedSpace(t, "mgr-dl-disbanded", "d", "u2", SpaceStatusDisbanded)
	seedSpace(t, "mgr-dl-banned", "b", "u3", SpaceStatusBanned)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/manager/spaces/disabled", nil)
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Count int64 `json:"count"`
		List  []struct {
			SpaceId string `json:"space_id"`
		} `json:"list"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.EqualValues(t, 2, resp.Count)
	ids := map[string]bool{}
	for _, it := range resp.List {
		ids[it.SpaceId] = true
	}
	assert.True(t, ids["mgr-dl-disbanded"])
	assert.True(t, ids["mgr-dl-banned"])
	assert.False(t, ids["mgr-dl-active"])
}

func TestManager_RemoveOwnerBlockedInTx(t *testing.T) {
	// 验证 DB 层的 SELECT ... FOR UPDATE 守卫：即便 handler 不做 pre-check，
	// removeMembersForce 直接传 owner uid 也会返回 ErrCannotRemoveOwner。
	_, _, err := setup(t)
	assert.NoError(t, err)
	seedSpace(t, "mgr-rm-tx", "tx guard", "u-owner-tx", SpaceStatusNormal)

	mgrDB := newManagerDB(testCtx.DB())
	err = mgrDB.removeMembersForce("mgr-rm-tx", []string{"u-owner-tx"})
	assert.ErrorIs(t, err, ErrCannotRemoveOwner)

	owner, err := testSpaceDB.queryMember("mgr-rm-tx", "u-owner-tx")
	assert.NoError(t, err)
	assert.NotNil(t, owner, "owner must remain after guarded rollback")
	assert.Equal(t, 2, owner.Role)
}

func TestManager_NormalizeUIDsDedup(t *testing.T) {
	got := normalizeUIDs([]string{"a", "", "b", "a", "c", "", "b"})
	assert.Equal(t, []string{"a", "b", "c"}, got)
	assert.Empty(t, normalizeUIDs(nil))
	assert.Empty(t, normalizeUIDs([]string{"", ""}))
}

func TestManager_BatchSizeCap(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)
	seedSpace(t, "mgr-cap", "cap space", "u-owner", SpaceStatusNormal)

	// 构造超限请求（201 个 uid）
	big := make([]string, managerMaxBatchUIDs+1)
	for i := range big {
		big[i] = fmt.Sprintf("u%d", i)
	}
	body := util.ToJson(map[string]interface{}{"uids": big})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/manager/spaces/mgr-cap/members", bytes.NewReader([]byte(body)))
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "200")
}

func TestManager_TransferOwnerTargetMissing(t *testing.T) {
	// 验证 transferOwnerAdmin 对 status=0 目标的原子守卫：
	// 不会发生「降老 owner → 目标已被移除 → 新 owner 提升失败 → 空间无主」
	_, _, err := setup(t)
	assert.NoError(t, err)
	seedSpace(t, "mgr-xfer-miss", "transfer guard", "u-owner-x", SpaceStatusNormal)
	err = testSpaceDB.insertMemberNoTx(&MemberModel{
		SpaceId: "mgr-xfer-miss", UID: "u-ghost", Role: 0, Status: 0, // 已移除
	})
	assert.NoError(t, err)

	err = newManagerDB(testCtx.DB()).transferOwnerAdmin("mgr-xfer-miss", "u-ghost")
	assert.ErrorIs(t, err, ErrTransferTargetMissing)

	// 原 owner 依然是 owner（未被事务的 step 1 降级）
	owner, err := testSpaceDB.queryMember("mgr-xfer-miss", "u-owner-x")
	assert.NoError(t, err)
	assert.NotNil(t, owner)
	assert.Equal(t, 2, owner.Role, "original owner must stay owner when transfer aborts")
}

func TestManager_LiftBanRefreshesCache(t *testing.T) {
	// 验证 liftBan 成功后会异步调用 loadKnownSpaceIDs 刷新 pkg/space 缓存
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-ban-cache", "cache", "u-owner-c", SpaceStatusBanned)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/v1/manager/spaces/mgr-ban-cache/status/1", nil)
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 等待异步 loadKnownSpaceIDs 完成（ParseChannelID 要求 "s" 前缀）
	assert.Eventually(t, func() bool {
		sid, _ := spacepkg.ParseChannelID("smgr-ban-cache_peer1")
		return sid == "mgr-ban-cache"
	}, 2*time.Second, 50*time.Millisecond, "解禁后 spaceId 应出现在 ParseChannelID 缓存里")
}

func TestManager_AddMembersOnDisbandedSpace(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	seedSpace(t, "mgr-add-dead", "dead", "u-owner-dd", SpaceStatusDisbanded)

	body := util.ToJson(map[string]interface{}{"uids": []string{"new-u"}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/manager/spaces/mgr-add-dead/members", bytes.NewReader([]byte(body)))
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "已解散")
}

func TestManager_RemoveMembersOnNonExistentSpace(t *testing.T) {
	s, _, err := setup(t)
	assert.NoError(t, err)
	token := adminToken(t)

	body := util.ToJson(map[string]interface{}{"uids": []string{"any"}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/v1/manager/spaces/does-not-exist/members", bytes.NewReader([]byte(body)))
	req.Header.Set("token", token)
	s.GetRoute().ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "不存在")
}
