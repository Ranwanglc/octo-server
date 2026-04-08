package category

import (
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/gocraft/dbr/v2"
)

type categoryDB struct {
	ctx     *config.Context
	session *dbr.Session
}

func newCategoryDB(ctx *config.Context) *categoryDB {
	return &categoryDB{
		ctx:     ctx,
		session: ctx.DB(),
	}
}

func (d *categoryDB) insertCategory(m *CategoryModel) error {
	_, err := d.session.InsertInto("group_category").Columns(util.AttrToUnderscore(m)...).Record(m).Exec()
	return err
}

func (d *categoryDB) queryCategoriesByUIDAndSpaceID(uid, spaceID string) ([]*CategoryModel, error) {
	var models []*CategoryModel
	_, err := d.session.Select("*").From("group_category").
		Where("uid=? and space_id=? and status=1", uid, spaceID).
		OrderAsc("sort").
		Load(&models)
	return models, err
}

func (d *categoryDB) queryCategoryByID(categoryID string) (*CategoryModel, error) {
	var model *CategoryModel
	_, err := d.session.Select("*").From("group_category").
		Where("category_id=? and status=1", categoryID).
		Load(&model)
	return model, err
}

func (d *categoryDB) countCategoriesByUIDAndSpaceID(uid, spaceID string) (int, error) {
	var count int
	_, err := d.session.Select("count(*)").From("group_category").
		Where("uid=? and space_id=? and status=1", uid, spaceID).
		Load(&count)
	return count, err
}

func (d *categoryDB) updateCategoryName(categoryID, name string) error {
	_, err := d.session.Update("group_category").
		Set("name", name).
		Where("category_id=?", categoryID).
		Exec()
	return err
}

func (d *categoryDB) deleteCategory(categoryID string) error {
	_, err := d.session.Update("group_category").
		Set("status", 2).
		Where("category_id=?", categoryID).
		Exec()
	return err
}

func (d *categoryDB) updateCategorySort(categoryID string, sort int) error {
	_, err := d.session.Update("group_category").
		Set("sort", sort).
		Where("category_id=?", categoryID).
		Exec()
	return err
}
