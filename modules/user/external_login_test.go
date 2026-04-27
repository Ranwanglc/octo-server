package user

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 验证 *Service 在没有注入 handler 时返回 sentinel error。
// 不依赖测试服务器,纯 in-memory 验证保护 IService 契约不被悄悄破坏。
func TestService_LoginByExternalIdentity_NotConfigured(t *testing.T) {
	svc := &Service{}

	resp, err := svc.LoginByExternalIdentity(context.Background(), ExternalLoginReq{
		ExistingUID: "any",
	})
	assert.Nil(t, resp)
	assert.True(t, errors.Is(err, ErrExternalLoginNotConfigured), "expect ErrExternalLoginNotConfigured, got %v", err)
}

// user.New 完整初始化后 Service 应已注入 handler,委托链能贯通。
func TestService_LoginByExternalIdentity_ExistingUser(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx)
	u.Route(s.GetRoute())

	uid := "ext-existing-uid-1"
	require.NoError(t, u.db.Insert(&Model{
		UID:      uid,
		Username: "ext_existing",
		Name:     "ExtExisting",
		ShortNo:  "extshort1",
		Vercode:  uid + "@1",
		Status:   int(common.UserAvailable),
	}))

	resp, err := u.userService.LoginByExternalIdentity(context.Background(), ExternalLoginReq{
		ExistingUID: uid,
		DeviceFlag:  config.APP,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uid, resp.UID)
	assert.False(t, resp.IsNewUser)
	// LoginRespJSON 应是 loginUserDetailResp 的合法 JSON,含 token/uid 字段
	assert.True(t, strings.Contains(resp.LoginRespJSON, `"token":`), "json=%s", resp.LoginRespJSON)
	assert.True(t, strings.Contains(resp.LoginRespJSON, `"uid":"`+uid+`"`), "json=%s", resp.LoginRespJSON)
}

// ExistingUID 指向已注销的用户应拒绝。
func TestService_LoginByExternalIdentity_ExistingUserDestroyed(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx)
	u.Route(s.GetRoute())

	uid := "ext-destroyed-uid"
	require.NoError(t, u.db.Insert(&Model{
		UID:       uid,
		Username:  "ext_destroyed",
		Name:      "Gone",
		ShortNo:   "extshort2",
		Vercode:   uid + "@1",
		Status:    int(common.UserAvailable),
		IsDestroy: IsDestroyDone, // 终态注销,必须拒
	}))

	resp, err := u.userService.LoginByExternalIdentity(context.Background(), ExternalLoginReq{
		ExistingUID: uid,
		DeviceFlag:  config.APP,
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
}

// 冷静期(IsDestroy=1)用户必须能登录:登录动作即撤销注销,符合 PR #1192 的产品规则。
func TestService_LoginByExternalIdentity_ExistingUserCoolingOff(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx)
	u.Route(s.GetRoute())

	uid := "ext-cooling-uid"
	require.NoError(t, u.db.Insert(&Model{
		UID:       uid,
		Username:  "ext_cooling",
		Name:      "Cooling",
		ShortNo:   "extshort3",
		Vercode:   uid + "@1",
		Status:    int(common.UserAvailable),
		IsDestroy: IsDestroyApplying, // 冷静期可登录
	}))

	resp, err := u.userService.LoginByExternalIdentity(context.Background(), ExternalLoginReq{
		ExistingUID: uid,
		DeviceFlag:  config.APP,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uid, resp.UID)
}

// ExistingUID 不存在应报错(避免悄悄落库)。
func TestService_LoginByExternalIdentity_ExistingUserMissing(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx)
	u.Route(s.GetRoute())

	resp, err := u.userService.LoginByExternalIdentity(context.Background(), ExternalLoginReq{
		ExistingUID: "no-such-uid",
		DeviceFlag:  config.APP,
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
}

// 空 UID 走 createUserWithRespAndTx 路径,应新建用户并返回 IsNewUser=true。
func TestService_LoginByExternalIdentity_CreateNewUser(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx)
	u.Route(s.GetRoute())

	uid := util.GenerUUID()
	resp, err := u.userService.LoginByExternalIdentity(context.Background(), ExternalLoginReq{
		UID:        uid,
		Name:       "ExtNew",
		Email:      "ext.new@example.com",
		DeviceFlag: config.APP,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, uid, resp.UID)
	assert.True(t, resp.IsNewUser)
	assert.True(t, strings.Contains(resp.LoginRespJSON, `"token":`), "json=%s", resp.LoginRespJSON)

	// 用户应已落库
	got, err := u.db.QueryByUID(uid)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "ExtNew", got.Name)
	assert.Equal(t, "ext.new@example.com", got.Email)
}

// 新建用户但未传 UID 应明确报错(避免依赖隐式 UUID 生成导致绑定错乱)。
func TestService_LoginByExternalIdentity_CreateRequiresUID(t *testing.T) {
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	u := New(ctx)
	u.Route(s.GetRoute())

	resp, err := u.userService.LoginByExternalIdentity(context.Background(), ExternalLoginReq{
		Name:       "MissingUID",
		DeviceFlag: config.APP,
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "UID is required")
}
