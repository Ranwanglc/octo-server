package botfather

import (
	"errors"

	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"go.uber.org/zap"
)

// validateBotGroupAccess 验证 bot 对群的访问权限
// 返回 robotID, groupNo, ok；如果 ok=false，已向客户端返回错误响应
func (bf *BotFather) validateBotGroupAccess(c *wkhttp.Context) (robotID, groupNo string, ok bool) {
	robotID = getRobotIDFromContext(c)
	groupNo = c.Param("group_no")

	if !thread.IsValidGroupNo(groupNo) {
		c.ResponseError(errors.New("invalid group_no format"))
		return "", "", false
	}

	isMember, err := bf.groupService.ExistMember(groupNo, robotID)
	if err != nil {
		bf.Error("检查群成员失败", zap.Error(err))
		c.ResponseError(errors.New("check group membership failed"))
		return "", "", false
	}
	if !isMember {
		c.ResponseError(errors.New("bot is not a member of this group"))
		return "", "", false
	}

	return robotID, groupNo, true
}

// validateBotThreadAccess 验证 bot 对子区的访问权限
// 返回 robotID, groupNo, shortID, ok；如果 ok=false，已向客户端返回错误响应
func (bf *BotFather) validateBotThreadAccess(c *wkhttp.Context) (robotID, groupNo, shortID string, ok bool) {
	robotID, groupNo, ok = bf.validateBotGroupAccess(c)
	if !ok {
		return "", "", "", false
	}

	shortID = c.Param("short_id")
	if !thread.IsValidShortID(shortID) {
		c.ResponseError(errors.New("invalid short_id format"))
		return "", "", "", false
	}

	return robotID, groupNo, shortID, true
}

// botCreateThread 创建子区
// POST /v1/bot/groups/:group_no/threads
func (bf *BotFather) botCreateThread(c *wkhttp.Context) {
	robotID, groupNo, ok := bf.validateBotGroupAccess(c)
	if !ok {
		return
	}

	var req struct {
		Name            string `json:"name" binding:"required,max=100"`
		SourceMessageID *int64 `json:"source_message_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		bf.Error("参数错误", zap.Error(err))
		c.ResponseError(errors.New("invalid request: name is required"))
		return
	}

	// 获取 bot 的显示名称
	creatorName := robotID
	userResp, _ := bf.userService.GetUserWithUsername(robotID)
	if userResp != nil && userResp.Name != "" {
		creatorName = userResp.Name
	}

	resp, err := bf.threadService.CreateThread(&thread.CreateThreadReq{
		GroupNo:         groupNo,
		Name:            req.Name,
		CreatorUID:      robotID,
		CreatorName:     creatorName,
		SourceMessageID: req.SourceMessageID,
	})
	if err != nil {
		bf.Error("创建子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("robotID", robotID))
		c.ResponseError(err)
		return
	}
	c.Response(resp)
}

// botListThreads 列出群内所有子区
// GET /v1/bot/groups/:group_no/threads
func (bf *BotFather) botListThreads(c *wkhttp.Context) {
	_, groupNo, ok := bf.validateBotGroupAccess(c)
	if !ok {
		return
	}

	threads, err := bf.threadService.GetThreads(groupNo)
	if err != nil {
		bf.Error("获取子区列表失败", zap.Error(err), zap.String("groupNo", groupNo))
		c.ResponseError(err)
		return
	}
	c.Response(threads)
}

// botGetThread 获取子区详情
// GET /v1/bot/groups/:group_no/threads/:short_id
func (bf *BotFather) botGetThread(c *wkhttp.Context) {
	_, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	resp, err := bf.threadService.GetThread(groupNo, shortID)
	if err != nil {
		bf.Error("获取子区详情失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.Response(resp)
}

// botDeleteThread 删除子区
// DELETE /v1/bot/groups/:group_no/threads/:short_id
func (bf *BotFather) botDeleteThread(c *wkhttp.Context) {
	robotID, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	// DeleteThread 内部会检查是否为创建者或群管理员
	err := bf.threadService.DeleteThread(groupNo, shortID, robotID)
	if err != nil {
		bf.Error("删除子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

// botListThreadMembers 获取子区成员列表
// GET /v1/bot/groups/:group_no/threads/:short_id/members
func (bf *BotFather) botListThreadMembers(c *wkhttp.Context) {
	_, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	members, err := bf.threadService.GetMembers(groupNo, shortID)
	if err != nil {
		bf.Error("获取成员列表失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.Response(members)
}

// botJoinThread 加入子区
// POST /v1/bot/groups/:group_no/threads/:short_id/join
func (bf *BotFather) botJoinThread(c *wkhttp.Context) {
	robotID, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	err := bf.threadService.JoinThread(groupNo, shortID, robotID)
	if err != nil {
		bf.Error("加入子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

// botLeaveThread 离开子区
// POST /v1/bot/groups/:group_no/threads/:short_id/leave
func (bf *BotFather) botLeaveThread(c *wkhttp.Context) {
	robotID, groupNo, shortID, ok := bf.validateBotThreadAccess(c)
	if !ok {
		return
	}

	err := bf.threadService.LeaveThread(groupNo, shortID, robotID)
	if err != nil {
		bf.Error("离开子区失败", zap.Error(err), zap.String("groupNo", groupNo), zap.String("shortID", shortID))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}
