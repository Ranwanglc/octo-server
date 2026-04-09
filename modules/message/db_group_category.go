package message

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/gocraft/dbr/v2"
)

// groupCategoryDB 群组分类数据库操作
type groupCategoryDB struct {
	ctx     *config.Context
	session *dbr.Session
}

func newGroupCategoryDB(ctx *config.Context) *groupCategoryDB {
	return &groupCategoryDB{
		ctx:     ctx,
		session: ctx.DB(),
	}
}

// GroupCategorySetting 群组分类设置（从 group_setting 表查询）
type GroupCategorySetting struct {
	GroupNo      string
	CategoryID   *string
	CategorySort int
}

// QueryCategorySettingsByGroupNos 批量查询群组的分类设置
func (d *groupCategoryDB) QueryCategorySettingsByGroupNos(groupNos []string, uid string) ([]*GroupCategorySetting, error) {
	if len(groupNos) == 0 {
		return nil, nil
	}
	var results []*GroupCategorySetting
	_, err := d.session.Select("group_no", "category_id", "IFNULL(category_sort, 0) as category_sort").
		From("group_setting").
		Where("group_no IN ? AND uid = ?", groupNos, uid).
		Load(&results)
	return results, err
}
