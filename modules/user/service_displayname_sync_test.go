package user

import (
	"context"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 名字统一(实名回写 user.name)的集成测试。
//
// 背景:统一认证(OIDC IdP)返回两个名字 —— `name` claim(写 user.name,群成员
// 列表 / @ / 个人信息读它)与 `legal_name` 实名 claim(写 user_verification.real_name,
// 会话某些位置读它)。二者由不同链路维护、互不同步,导致同一人在不同位置显示不同
// 名字。修复:实名为权威显示名,UpsertVerificationFromOIDC 在实名 upsert 后把
// real_name 回写到 user.name,使所有读 user.name 的路径(群/空间成员列表实时 join
// user.name)与读 real_name 的路径显示一致。
//
// 真 DB 集成测试,依赖 testutil.NewTestServer() 起本地 MySQL。

// TestUpsertVerificationFromOIDC_SyncsUserName 核心正例:实名 upsert 后,
// user.name 被回写成 real_name(消毒后)。
func TestUpsertVerificationFromOIDC_SyncsUserName(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	u := New(ctx)
	svc, ok := u.userService.(*Service)
	require.True(t, ok, "userService must be *Service")

	const uid = "u-name-sync-1"
	// IdP 的 name claim 落到 user.name —— 与实名不同(典型分叉场景)。
	require.NoError(t, u.db.Insert(&Model{
		UID:      uid,
		Username: "jingyifei01",
		Name:     "莫小苝",
		ShortNo:  uid + "_sn",
		Status:   1,
	}))

	claims := OIDCVerificationClaims{
		LegalName:        "景逸飞",
		Subject:          "cas-sub-1",
		VerifiedProvider: "cas.example.com",
		VerifiedAt:       time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC).Unix(),
	}
	require.NoError(t, svc.UpsertVerificationFromOIDC(context.Background(), uid, claims))

	got, err := u.db.QueryByUID(uid)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "景逸飞", got.Name, "user.name 应被回写为实名")

	// real_name 也应写入 user_verification(原有行为不回归)。
	vr, err := svc.verificationDB.QueryByUID(uid)
	require.NoError(t, err)
	require.NotNil(t, vr)
	assert.Equal(t, "景逸飞", vr.RealName)
}

// TestUpsertVerificationFromOIDC_SyncsUserName_Idempotent 幂等:user.name 已等于
// 实名时,再次 upsert 不应改变它(也不应报错)—— 守住"仅在不同才写"的幂等约定。
func TestUpsertVerificationFromOIDC_SyncsUserName_Idempotent(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	u := New(ctx)
	svc := u.userService.(*Service)

	const uid = "u-name-sync-idem"
	require.NoError(t, u.db.Insert(&Model{
		UID:      uid,
		Username: "idem01",
		Name:     "景逸飞", // 已与实名一致
		ShortNo:  uid + "_sn",
		Status:   1,
	}))

	claims := OIDCVerificationClaims{
		LegalName:        "景逸飞",
		Subject:          "cas-sub-idem",
		VerifiedProvider: "cas.example.com",
		VerifiedAt:       time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC).Unix(),
	}
	require.NoError(t, svc.UpsertVerificationFromOIDC(context.Background(), uid, claims))

	got, err := u.db.QueryByUID(uid)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "景逸飞", got.Name)
}

// TestExternalLoginExisting_VerifiedRealNameNotOverwritten 守住名字统一的关键不变量:
// 已有权威实名的用户再次 OIDC 登录时,IdP 的 name claim(可能仍是旧昵称)不得覆盖
// 已与实名对齐的 user.name —— 否则两条写入路径会在同一次登录里来回翻转。
//
// 未实名用户的 IdP name 同步(issue #1307)由
// TestExternalLoginExisting_NoRealNameStillSyncsIdPName 反向覆盖。
func TestExternalLoginExisting_VerifiedRealNameNotOverwritten(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	u := New(ctx)
	svc := u.userService.(*Service)

	const uid = "u-relogin-verified"
	require.NoError(t, u.db.Insert(&Model{
		UID:      uid,
		Username: "verified01",
		Name:     "景逸飞", // 已被实名回写对齐
		ShortNo:  uid + "_sn",
		Status:   1,
	}))
	// 已有权威实名记录。
	require.NoError(t, svc.UpsertVerificationFromOIDC(context.Background(), uid, OIDCVerificationClaims{
		LegalName:        "景逸飞",
		Subject:          "cas-sub-verified",
		VerifiedProvider: "cas.example.com",
		VerifiedAt:       time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC).Unix(),
	}))

	// IdP 再次登录,name claim 仍是旧昵称"莫小苝"。
	_, err := u.externalLoginExisting(context.Background(), ExternalLoginReq{
		ExistingUID: uid,
		Name:        "莫小苝",
		DeviceFlag:  0,
	})
	require.NoError(t, err)

	got, err := u.db.QueryByUID(uid)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "景逸飞", got.Name,
		"已有实名时 IdP name claim 不得覆盖 user.name")
}

// TestExternalLoginExisting_NoRealNameStillSyncsIdPName 未实名用户保持原 #1307
// 行为:IdP name claim 非空且与库中不同时,同步覆盖 user.name。
func TestExternalLoginExisting_NoRealNameStillSyncsIdPName(t *testing.T) {
	_, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))

	u := New(ctx)

	const uid = "u-relogin-noreal"
	require.NoError(t, u.db.Insert(&Model{
		UID:      uid,
		Username: "noreal01",
		Name:     "旧名字",
		ShortNo:  uid + "_sn",
		Status:   1,
	}))

	// 无 user_verification 记录 → hasVerifiedRealName=false → 走原 IdP name 同步。
	_, err := u.externalLoginExisting(context.Background(), ExternalLoginReq{
		ExistingUID: uid,
		Name:        "新名字",
		DeviceFlag:  0,
	})
	require.NoError(t, err)

	got, err := u.db.QueryByUID(uid)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "新名字", got.Name,
		"未实名用户应保持 #1307 的 IdP name 同步行为")
}
