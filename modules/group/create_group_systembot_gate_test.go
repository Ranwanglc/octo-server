package group

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-server/modules/user"
	pkgspace "github.com/Mininglamp-OSS/octo-server/pkg/space"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateGroup_SystemBotForbidden_NoSpace 守卫 PR#483 (OCT-5) 能力门 B（顶层兜底）：
// 系统 bot（如 summary_notification）即使作为 BotUID 也不能建群，且不依赖 SpaceID。
func TestCreateGroup_SystemBotForbidden_NoSpace(t *testing.T) {
	svc, userDB := setupServiceTest(t)
	botUID := pkgspace.SummaryNotificationBotUID
	insertTestUsers(t, userDB, "gb_creator", "gb_m1", botUID)

	_, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: "gb_creator",
		Members: []string{"gb_m1"},
		Name:    "sysbot group",
		BotUID:  botUID,
	})
	require.Error(t, err, "system bot must not be allowed to create a group")
	assert.Contains(t, err.Error(), "system bot is not allowed to create groups")
}

// TestCreateGroup_SystemBotForbidden_EvenWithSpaceMemberRow 是真正闭环 reviewer P1 的负向
// 守卫：哪怕系统 bot 在 Space 里有 space_member 行（历史遗留 / 误操作），CheckMembership
// 也不能让它据此建群——IsSystemBot 排除在 CheckMembership 之前生效。
func TestCreateGroup_SystemBotForbidden_EvenWithSpaceMemberRow(t *testing.T) {
	svc, userDB, ctx := setupServiceTestWithCtx(t)
	botUID := pkgspace.SummaryNotificationBotUID
	insertTestUsers(t, userDB, "gb2_creator", "gb2_m1", botUID)
	// 给 bot 也插一条 space_member 行（模拟历史遗留），证明门不靠成员关系放行。
	seedSpaceWithMembers(t, ctx, "space_gb2", "gb2_creator", "gb2_m1", botUID)

	_, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: "gb2_creator",
		Members: []string{"gb2_m1"},
		Name:    "sysbot group with member row",
		SpaceID: "space_gb2",
		BotUID:  botUID,
	})
	require.Error(t, err, "system bot with a space_member row must STILL be rejected from creating a group")
	assert.Contains(t, err.Error(), "system bot is not allowed to create groups")
}

// TestCreateGroup_NonSystemBotAllowed sanity：普通 bot 作为 BotUID 在其 Space 内仍可建群
// （证明门只针对系统 bot，不误伤普通 User Bot）。
func TestCreateGroup_NonSystemBotAllowed(t *testing.T) {
	svc, userDB, ctx := setupServiceTestWithCtx(t)
	botUID := "gb3_user_bot"
	insertTestUsers(t, userDB, "gb3_creator", "gb3_m1", botUID)
	seedSpaceWithMembers(t, ctx, "space_gb3", "gb3_creator", "gb3_m1", botUID)

	resp, err := svc.CreateGroup(&CreateGroupServiceReq{
		Creator: "gb3_creator",
		Members: []string{"gb3_m1"},
		Name:    "normal bot group",
		SpaceID: "space_gb3",
		BotUID:  botUID,
	})
	require.NoError(t, err, "a non-system bot that IS a space member must be allowed to create a group")
	assert.NotEmpty(t, resp.GroupNo)

	_ = user.Model{} // keep user import referenced if helpers change
}
