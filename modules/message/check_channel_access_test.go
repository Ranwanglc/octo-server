package message

// =============================================================================
// Issue #353（PR #345 review P1-test）— checkChannelAccess requireActive split
// 的 CI 内（非 integration tag）回归覆盖。
//
// AuthorizeThreadFollow（FollowThread，子区/CommunityTopic 分支）要求父群「活跃
// 成员」（ExistMemberActive，排除被拉黑）；AuthorizeChannelFollow（FollowChannel，
// GROUP 分支）保留 permissive ExistMember。此前该 split 仅由
// thread_follow_blacklist_e2e_test.go 覆盖，但该文件带 //go:build integration、
// CI 的 go test 永不编译——把 AuthorizeThreadFollow 翻回 permissive 的回归会
// 静默漏过。本文件不带 tag，直接吃 CI 已就绪的 MySQL service，跑生产同款
// newThreadAuthChecker（真实 group/group_member/thread 表）。
//
// 测试基建约定（PR #356 round-1 CI 红的教训）：本包非 integration-tag 测试一律
// 不跑 sql-migrate（既有 testutil.NewTestServer 用例全部 t.Skip；手建表用例与
// 迁移在 -shuffle 下互撞 Error 1050）。照搬 e2e helper 的做法：手建最小表 +
// 裸 INSERT 种子，ctx 显式 Migration=false。
// =============================================================================

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	convext "github.com/Mininglamp-OSS/octo-server/modules/conversation_ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ccaSpaceID = "s_cca"

// ccaNewCtx 构造指向测试 MySQL 的 *config.Context（config.New 默认 DSN 即
// root:demo@…/test，与 CI service 一致），显式 Migration=false——不经 module.Setup。
func ccaNewCtx(t *testing.T) *config.Context {
	t.Helper()
	cfg := config.New()
	cfg.Test = true
	cfg.DB.Migration = false
	return config.NewContext(cfg)
}

// ccaEnsureTables 手建 checkChannelAccess / AuthorizeThreadFollow 触达的最小表
// （DDL 与 modules/group、modules/thread 迁移中本测试触达的列对齐；写法照搬
// default_followed_group_guard_e2e_test.go / thread_follow_blacklist_e2e_test.go
// 的同名 helper）。DROP + CREATE 而非 IF NOT EXISTS：其它用例可能先以更窄的
// 列集建过 group_member；表只装每用例自种数据，破坏性 DDL 安全。
func ccaEnsureTables(t *testing.T, ctx *config.Context) {
	t.Helper()
	for _, tbl := range []string{"thread", "group_member", "`group`"} {
		_, err := ctx.DB().Exec("DROP TABLE IF EXISTS " + tbl)
		require.NoError(t, err, "drop %s", tbl)
	}
	stmts := []string{
		"CREATE TABLE `group` (" +
			"  `id` INT NOT NULL AUTO_INCREMENT PRIMARY KEY," +
			"  `group_no` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `name` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `creator` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `status` SMALLINT NOT NULL DEFAULT 0," +
			"  `version` BIGINT NOT NULL DEFAULT 0," +
			"  `group_type` SMALLINT NOT NULL DEFAULT 0," +
			"  `space_id` VARCHAR(40) DEFAULT ''," +
			"  `is_external_group` SMALLINT NOT NULL DEFAULT 0," +
			"  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
			"  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
			"  UNIQUE KEY `group_groupNo` (`group_no`)" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE `group_member` (" +
			"  `id` INT NOT NULL AUTO_INCREMENT PRIMARY KEY," +
			"  `group_no` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `uid` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `role` SMALLINT NOT NULL DEFAULT 0," +
			"  `version` BIGINT NOT NULL DEFAULT 0," +
			"  `is_deleted` SMALLINT NOT NULL DEFAULT 0," +
			"  `status` SMALLINT NOT NULL DEFAULT 1," +
			"  `vercode` VARCHAR(100) NOT NULL DEFAULT ''," +
			"  `robot` SMALLINT NOT NULL DEFAULT 0," +
			"  `is_external` SMALLINT NOT NULL DEFAULT 0," +
			"  `source_space_id` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
			"  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
			"  UNIQUE KEY `group_no_uid` (`group_no`, `uid`)" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
		"CREATE TABLE `thread` (" +
			"  `id` INT NOT NULL AUTO_INCREMENT PRIMARY KEY," +
			"  `group_no` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `short_id` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `name` VARCHAR(100) NOT NULL DEFAULT ''," +
			"  `creator_uid` VARCHAR(40) NOT NULL DEFAULT ''," +
			"  `status` INT NOT NULL DEFAULT 1," +
			// QueryActiveByGroupShortIDs（AuthorizeThreadFollow 路径）显式 SELECT
			// last_message_at，最小表也必须带上（与 ensureThreadE2ETable 一致）。
			"  `last_message_at` TIMESTAMP NULL DEFAULT NULL," +
			"  `created_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
			"  `updated_at` TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP," +
			"  UNIQUE KEY `uk_short` (`short_id`)" +
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
	}
	for _, s := range stmts {
		_, err := ctx.DB().Exec(s)
		require.NoError(t, err, "ccaEnsureTables: %s", s[:40])
	}
}

// setupCheckChannelAccessData 建父群（space_id 空 → legacy wildcard 可见）+
// 一个正常成员 + 一个 active 子区，返回生产同款 threadAuthChecker。
func setupCheckChannelAccessData(t *testing.T) (*config.Context, *threadAuthChecker, string, string, string) {
	t.Helper()
	ctx := ccaNewCtx(t)
	ccaEnsureTables(t, ctx)

	groupNo := strings.ReplaceAll(util.GenerUUID(), "-", "")
	memberUID := "u_cca_" + util.GenerUUID()[:8]
	shortID := "1489104291682713604"

	_, err := ctx.DB().Exec(
		"INSERT INTO `group` (group_no, name, creator, status, version, space_id) VALUES (?, '父群', ?, 1, 1, '')",
		groupNo, memberUID,
	)
	require.NoError(t, err, "seed group")
	_, err = ctx.DB().Exec(
		"INSERT INTO group_member (group_no, uid, vercode, is_deleted, status, version) VALUES (?, ?, ?, 0, ?, 1)",
		groupNo, memberUID, util.GenerUUID(), int(common.GroupMemberStatusNormal),
	)
	require.NoError(t, err, "seed member")
	_, err = ctx.DB().Exec(
		"INSERT INTO thread (group_no, short_id, name, creator_uid, status) VALUES (?, ?, 'topic', ?, 1)",
		groupNo, shortID, memberUID,
	)
	require.NoError(t, err, "seed thread")

	return ctx, newThreadAuthChecker(ctx), groupNo, memberUID, shortID
}

func ccaSetMemberStatus(t *testing.T, ctx *config.Context, groupNo, uid string, status common.GroupMemberStatus) {
	t.Helper()
	_, err := ctx.DB().Exec(
		"UPDATE group_member SET status=? WHERE group_no=? AND uid=?",
		int(status), groupNo, uid,
	)
	require.NoError(t, err)
}

// TestCheckChannelAccess_RequireActiveSplit 钉住 requireActive 两档语义：
// true（子区分支）排除被拉黑成员，false（GROUP 分支）保持 permissive。
func TestCheckChannelAccess_RequireActiveSplit(t *testing.T) {
	ctx, checker, groupNo, memberUID, _ := setupCheckChannelAccessData(t)

	t.Run("normal member passes both", func(t *testing.T) {
		assert.NoError(t, checker.checkChannelAccess(memberUID, ccaSpaceID, groupNo, true),
			"正常成员 requireActive=true 应放行")
		assert.NoError(t, checker.checkChannelAccess(memberUID, ccaSpaceID, groupNo, false),
			"正常成员 requireActive=false 应放行")
	})

	ccaSetMemberStatus(t, ctx, groupNo, memberUID, common.GroupMemberStatusBlacklist)

	t.Run("blacklisted member denied only when requireActive", func(t *testing.T) {
		err := checker.checkChannelAccess(memberUID, ccaSpaceID, groupNo, true)
		require.Error(t, err, "被拉黑成员 requireActive=true 必须被拒（#345 split）")
		assert.True(t, errors.Is(err, convext.ErrChannelForbidden),
			"应返回 ErrChannelForbidden，got %v", err)
		assert.NoError(t, checker.checkChannelAccess(memberUID, ccaSpaceID, groupNo, false),
			"GROUP 分支保持 permissive，不应 over-block 被拉黑成员")
	})

	t.Run("removed member denied on both", func(t *testing.T) {
		_, err := ctx.DB().Exec(
			"UPDATE group_member SET is_deleted=1 WHERE group_no=? AND uid=?", groupNo, memberUID)
		require.NoError(t, err)
		assert.Error(t, checker.checkChannelAccess(memberUID, ccaSpaceID, groupNo, true),
			"被移出成员 requireActive=true 必须被拒")
		assert.Error(t, checker.checkChannelAccess(memberUID, ccaSpaceID, groupNo, false),
			"被移出成员 requireActive=false 也被拒（is_deleted 在两档都生效）")
	})
}

// TestAuthorizeFollow_BlacklistSplit 钉住对外入口的接线：AuthorizeThreadFollow
// 必须走 requireActive=true（被拉黑 → ErrThreadForbidden），AuthorizeChannelFollow
// 走 false（被拉黑放行）。如果有人把 AuthorizeThreadFollow 翻回 permissive，
// 这里的 deny 断言会在 CI 直接红——这正是 #353 指出的静默回归缺口。
func TestAuthorizeFollow_BlacklistSplit(t *testing.T) {
	ctx, checker, groupNo, memberUID, shortID := setupCheckChannelAccessData(t)

	require.NoError(t, checker.AuthorizeThreadFollow(memberUID, ccaSpaceID, groupNo, shortID),
		"正常成员 follow 子区不应被拦")
	require.NoError(t, checker.AuthorizeChannelFollow(memberUID, ccaSpaceID, groupNo),
		"正常成员 follow GROUP 不应被拦")

	ccaSetMemberStatus(t, ctx, groupNo, memberUID, common.GroupMemberStatusBlacklist)

	err := checker.AuthorizeThreadFollow(memberUID, ccaSpaceID, groupNo, shortID)
	require.Error(t, err, "被拉黑父群成员 follow 子区必须被拒")
	assert.True(t, errors.Is(err, convext.ErrThreadForbidden),
		"应翻译为 ErrThreadForbidden（handler 走 403 路径），got %v", err)

	assert.NoError(t, checker.AuthorizeChannelFollow(memberUID, ccaSpaceID, groupNo),
		"GROUP 分支保留 permissive ExistMember，被拉黑成员 channel follow 不被 over-block")
}
