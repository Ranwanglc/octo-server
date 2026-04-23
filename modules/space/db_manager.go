package space

import (
	"errors"
	"time"

	"github.com/gocraft/dbr/v2"
)

// managerSpaceModel 管理侧空间列表模型（带成员数和创建者名称）
type managerSpaceModel struct {
	SpaceModel
	CreatorName string // 创建者名称
	MemberCount int    // 活跃成员数
}

// managerMemberModel 管理侧成员列表模型（带用户名）
type managerMemberModel struct {
	MemberModel
	Name string // 用户名
}

// managerDB 管理后台专用查询
type managerDB struct {
	session *dbr.Session
}

func newManagerDB(session *dbr.Session) *managerDB {
	return &managerDB{session: session}
}

// querySpaces 按关键字 + 状态分页查询空间。
// statuses 为空时不按状态过滤，非空时 WHERE s.status IN (statuses...)。
func (d *managerDB) querySpaces(keyword string, statuses []int, pageSize, pageIndex uint64) ([]*managerSpaceModel, error) {
	builder := d.session.Select(
		"s.*",
		"IFNULL(u.name, '') as creator_name",
		"(SELECT COUNT(*) FROM space_member WHERE space_id=s.space_id AND status=1) as member_count",
	).From(dbr.I("space").As("s")).
		LeftJoin(dbr.I("user").As("u"), "u.uid=s.creator")

	if len(statuses) > 0 {
		builder = builder.Where("s.status IN ?", statuses)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		builder = builder.Where("s.name LIKE ? OR s.space_id LIKE ? OR s.creator LIKE ?", like, like, like)
	}

	var list []*managerSpaceModel
	_, err := builder.
		OrderDir("s.created_at", false).
		Limit(pageSize).Offset((pageIndex - 1) * pageSize).
		Load(&list)
	return list, err
}

// countSpaces 空间总数（与 querySpaces 共用过滤，保持同样的表别名以便未来 JOIN 扩展）
func (d *managerDB) countSpaces(keyword string, statuses []int) (int64, error) {
	builder := d.session.Select("COUNT(*)").From(dbr.I("space").As("s"))

	if len(statuses) > 0 {
		builder = builder.Where("s.status IN ?", statuses)
	}
	if keyword != "" {
		like := "%" + keyword + "%"
		builder = builder.Where("s.name LIKE ? OR s.space_id LIKE ? OR s.creator LIKE ?", like, like, like)
	}

	var count int64
	_, err := builder.Load(&count)
	return count, err
}

// querySpaceIncludeDisbanded 查询空间（不过滤 status，后台可看已解散空间）。
// err 优先于"未找到"返回，调用方能区分"DB 错误"和"空间不存在"。
func (d *managerDB) querySpaceIncludeDisbanded(spaceId string) (*managerSpaceModel, error) {
	var m managerSpaceModel
	_, err := d.session.Select(
		"s.*",
		"IFNULL(u.name, '') as creator_name",
		"(SELECT COUNT(*) FROM space_member WHERE space_id=s.space_id AND status=1) as member_count",
	).From(dbr.I("space").As("s")).
		LeftJoin(dbr.I("user").As("u"), "u.uid=s.creator").
		Where("s.space_id=?", spaceId).
		Load(&m)
	if err != nil {
		return nil, err
	}
	if m.SpaceId == "" {
		return nil, nil
	}
	return &m, nil
}

// forceDisbandSpace 管理员强制解散：标记 space 状态为 0，同时将所有成员置为已移除
func (d *managerDB) forceDisbandSpace(spaceId string) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	now := time.Now()
	if _, err = tx.Update("space").Set("status", 0).Set("updated_at", now).
		Where("space_id=?", spaceId).Exec(); err != nil {
		return err
	}
	if _, err = tx.Update("space_member").Set("status", 0).Set("updated_at", now).
		Where("space_id=? AND status=1", spaceId).Exec(); err != nil {
		return err
	}
	return tx.Commit()
}

// queryMembersAdmin 管理后台查询空间成员（含已移除，支持 keyword）
func (d *managerDB) queryMembersAdmin(spaceId, keyword string, pageSize, pageIndex uint64) ([]*managerMemberModel, error) {
	builder := d.session.Select("sm.*", "IFNULL(u.name,'') as name").
		From(dbr.I("space_member").As("sm")).
		LeftJoin(dbr.I("user").As("u"), "u.uid=sm.uid").
		Where("sm.space_id=?", spaceId)
	if keyword != "" {
		like := "%" + keyword + "%"
		builder = builder.Where("u.name LIKE ? OR sm.uid LIKE ?", like, like)
	}
	var list []*managerMemberModel
	_, err := builder.
		OrderDir("sm.role", false).
		OrderAsc("sm.created_at").
		Limit(pageSize).Offset((pageIndex - 1) * pageSize).
		Load(&list)
	return list, err
}

// countMembersAdmin 空间成员总数（含已移除，支持 keyword）
func (d *managerDB) countMembersAdmin(spaceId, keyword string) (int64, error) {
	builder := d.session.Select("COUNT(*)").
		From(dbr.I("space_member").As("sm")).
		LeftJoin(dbr.I("user").As("u"), "u.uid=sm.uid").
		Where("sm.space_id=?", spaceId)
	if keyword != "" {
		like := "%" + keyword + "%"
		builder = builder.Where("u.name LIKE ? OR sm.uid LIKE ?", like, like)
	}
	var count int64
	_, err := builder.Load(&count)
	return count, err
}

// updateSpaceStatus 更新空间状态
func (d *managerDB) updateSpaceStatus(spaceId string, status int) error {
	_, err := d.session.Update("space").
		Set("status", status).
		Set("updated_at", time.Now()).
		Where("space_id=?", spaceId).Exec()
	return err
}

// upsertMembers 批量添加/重新激活成员（单一事务，部分失败则全部回滚）
func (d *managerDB) upsertMembers(spaceId string, uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()
	for _, uid := range uids {
		if _, err := tx.InsertBySql(
			"INSERT INTO space_member (space_id, uid, role, status, created_at, updated_at) VALUES (?, ?, 0, 1, NOW(), NOW()) "+
				"ON DUPLICATE KEY UPDATE status=1, updated_at=NOW()",
			spaceId, uid,
		).Exec(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrCannotRemoveOwner 拦截删除 owner 的请求，调用方需先转让所有权
var ErrCannotRemoveOwner = errors.New("cannot remove space owner; transfer ownership first")

// removeMembersForce 在单一事务中强制移除成员。
//
// 用 SELECT ... FOR UPDATE 锁定目标行并原子校验 role，若任一 uid 当前是 owner 则整体回滚。
// 这封住了「handler 查询 owner 状态 → DB 更新」之间的 TOCTOU：
// 如果并发的 transferOwnerAdmin 想把 [uids] 中某个成员提升为 owner，它的 UPDATE 会阻塞到本事务结束。
//
// 反向窗口（先本事务删除 → 再被并发 transfer 提升为 owner）由 transferOwnerAdmin 内部的
// `AND status=1` 守卫关掉：本事务 commit 后该 uid 的 status=0，后续 transfer 的 UPDATE 影响 0 行。
func (d *managerDB) removeMembersForce(spaceId string, uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	var ownerCount int
	if _, err = tx.SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid IN ? AND role=2 AND status=1 FOR UPDATE",
		spaceId, uids,
	).Load(&ownerCount); err != nil {
		return err
	}
	if ownerCount > 0 {
		return ErrCannotRemoveOwner
	}

	now := time.Now()
	for _, uid := range uids {
		if _, err := tx.Update("space_member").
			Set("status", 0).
			Set("updated_at", now).
			Where("space_id=? AND uid=?", spaceId, uid).Exec(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrTransferTargetMissing 目标成员不存在或已被移除，不能成为新 owner
var ErrTransferTargetMissing = errors.New("transfer target not found or already removed")

// transferOwnerAdmin 将 newOwner 置为 owner(2)，将当前所有 owner 降为 admin(1)。
//
// 事务开始时先用 SELECT ... FOR UPDATE 锁定目标行并确认其 status=1，
// 避免「先降老 owner → 目标被并发 remove → 后续 UPDATE 影响 0 行」导致空间无主。
// 若目标不存在 / 已被移除，整个事务回滚并返回 ErrTransferTargetMissing。
func (d *managerDB) transferOwnerAdmin(spaceId, newOwnerUID string) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	var targetCount int
	if _, err = tx.SelectBySql(
		"SELECT COUNT(*) FROM space_member WHERE space_id=? AND uid=? AND status=1 FOR UPDATE",
		spaceId, newOwnerUID,
	).Load(&targetCount); err != nil {
		return err
	}
	if targetCount == 0 {
		return ErrTransferTargetMissing
	}

	now := time.Now()
	// 先把现有所有 owner 降为 admin（通常只有一个，但防御式写法）
	if _, err = tx.Update("space_member").
		Set("role", 1).Set("updated_at", now).
		Where("space_id=? AND role=2 AND status=1", spaceId).Exec(); err != nil {
		return err
	}
	// 再把目标用户提升为 owner
	if _, err = tx.Update("space_member").
		Set("role", 2).Set("updated_at", now).
		Where("space_id=? AND uid=? AND status=1", spaceId, newOwnerUID).Exec(); err != nil {
		return err
	}
	return tx.Commit()
}

// queryInvitesAdmin 分页查询空间所有邀请码（含已禁用）
func (d *managerDB) queryInvitesAdmin(spaceId string, pageSize, pageIndex uint64) ([]*InvitationModel, error) {
	var list []*InvitationModel
	_, err := d.session.Select("*").From("space_invitation").
		Where("space_id=?", spaceId).
		OrderDir("created_at", false).
		Limit(pageSize).Offset((pageIndex - 1) * pageSize).
		Load(&list)
	return list, err
}

// countInvitesAdmin 空间邀请码总数
func (d *managerDB) countInvitesAdmin(spaceId string) (int64, error) {
	var count int64
	_, err := d.session.Select("COUNT(*)").From("space_invitation").
		Where("space_id=?", spaceId).Load(&count)
	return count, err
}

// disableInvitation 将邀请码置为无效
func (d *managerDB) disableInvitation(spaceId, code string) (int64, error) {
	result, err := d.session.Update("space_invitation").
		Set("status", 0).Set("updated_at", time.Now()).
		Where("space_id=? AND invite_code=?", spaceId, code).Exec()
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// queryJoinAppliesAdmin 管理后台查询申请列表，status<0 表示不过滤
func (d *managerDB) queryJoinAppliesAdmin(spaceId string, status int, pageSize, pageIndex uint64) ([]*spaceJoinApplyDetailModel, error) {
	builder := d.session.Select("a.*", "IFNULL(u.name,'') as applicant_name").
		From(dbr.I("space_join_apply").As("a")).
		LeftJoin(dbr.I("user").As("u"), "u.uid=a.uid").
		Where("a.space_id=?", spaceId)
	if status >= 0 {
		builder = builder.Where("a.status=?", status)
	}
	var list []*spaceJoinApplyDetailModel
	_, err := builder.
		OrderDir("a.created_at", false).
		Limit(pageSize).Offset((pageIndex - 1) * pageSize).
		Load(&list)
	return list, err
}

// countJoinAppliesAdmin 申请总数
func (d *managerDB) countJoinAppliesAdmin(spaceId string, status int) (int64, error) {
	builder := d.session.Select("COUNT(*)").From("space_join_apply").Where("space_id=?", spaceId)
	if status >= 0 {
		builder = builder.Where("status=?", status)
	}
	var count int64
	_, err := builder.Load(&count)
	return count, err
}
