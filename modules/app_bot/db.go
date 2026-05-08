package app_bot

import (
	"errors"
	"strings"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/db"
	"github.com/go-sql-driver/mysql"
	"github.com/gocraft/dbr/v2"
)

// ErrIDAlreadyInUse is returned when the bot ID/UID conflicts with an existing record.
var ErrIDAlreadyInUse = errors.New("id already in use")

type appBotDB struct {
	session *dbr.Session
	ctx     *config.Context
}

func newAppBotDB(ctx *config.Context) *appBotDB {
	return &appBotDB{
		ctx:     ctx,
		session: ctx.DB(),
	}
}

type appBotModel struct {
	ID          string `db:"id"`
	UID         string `db:"uid"`
	DisplayName string `db:"display_name"`
	Description string `db:"description"`
	Avatar      string `db:"avatar"`
	Scope       string `db:"scope"`
	SpaceID     string `db:"space_id"`
	Status      int    `db:"status"`
	Token       string `db:"token"`
	WelcomeMsg  string `db:"welcome_msg"`
	CreatedBy   string `db:"created_by"`
	db.BaseModel
}

// insertAppBot inserts a new App Bot record within a transaction.
func (d *appBotDB) insertAppBot(bot *appBotModel) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	// Best-effort cross-table UID uniqueness check.
	// Note: MySQL REPEATABLE READ does not prevent phantom reads from other tables,
	// so this is NOT a serializable guarantee. The true safety nets are:
	// 1. app_bot.uid UNIQUE constraint (same-table race → MySQL 1062)
	// 2. UpdateIMToken application-layer conflict on duplicate UID registration
	// This check covers the common case; edge-case concurrent cross-table collision
	// is acceptable given this is an admin-only operation.
	var count int
	err = tx.SelectBySql("SELECT COUNT(*) FROM user WHERE uid=?", bot.UID).LoadOne(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrIDAlreadyInUse
	}
	err = tx.SelectBySql("SELECT COUNT(*) FROM robot WHERE robot_id=?", bot.UID).LoadOne(&count)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrIDAlreadyInUse
	}

	_, err = tx.InsertInto("app_bot").Columns(
		"id", "uid", "display_name", "description", "avatar",
		"scope", "space_id", "status", "token", "welcome_msg", "created_by",
	).Values(
		bot.ID, bot.UID, bot.DisplayName, bot.Description, bot.Avatar,
		bot.Scope, bot.SpaceID, bot.Status, bot.Token, bot.WelcomeMsg, bot.CreatedBy,
	).Exec()
	if err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return ErrIDAlreadyInUse
		}
		return err
	}
	return tx.Commit()
}

// queryBotByID queries an App Bot by ID.
func (d *appBotDB) queryBotByID(id string) (*appBotModel, error) {
	var m *appBotModel
	_, err := d.session.Select("*").From("app_bot").Where("id=?", id).Load(&m)
	return m, err
}

// queryBotByUID queries an App Bot by UID.
func (d *appBotDB) queryBotByUID(uid string) (*appBotModel, error) {
	var m *appBotModel
	_, err := d.session.Select("*").From("app_bot").Where("uid=?", uid).Load(&m)
	return m, err
}

// queryBotsByScope queries App Bots by scope (and optional space_id) with pagination.
func (d *appBotDB) queryBotsByScope(scope, spaceID string, pageIndex, pageSize int64, keyword string, status *int) ([]*appBotModel, int64, error) {
	var bots []*appBotModel
	var total int64

	baseQuery := d.session.Select("*").From("app_bot")
	countQuery := d.session.Select("count(*)").From("app_bot")

	if scope == "space" && spaceID != "" {
		baseQuery = baseQuery.Where("scope=? AND space_id=?", scope, spaceID)
		countQuery = countQuery.Where("scope=? AND space_id=?", scope, spaceID)
	} else {
		baseQuery = baseQuery.Where("scope=?", scope)
		countQuery = countQuery.Where("scope=?", scope)
	}

	if keyword != "" {
		escaped := strings.ReplaceAll(keyword, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "%", "\\%")
		escaped = strings.ReplaceAll(escaped, "_", "\\_")
		likePattern := "%" + escaped + "%"
		baseQuery = baseQuery.Where("(display_name LIKE ? OR uid LIKE ?)", likePattern, likePattern)
		countQuery = countQuery.Where("(display_name LIKE ? OR uid LIKE ?)", likePattern, likePattern)
	}

	if status != nil {
		baseQuery = baseQuery.Where("status=?", *status)
		countQuery = countQuery.Where("status=?", *status)
	}

	if err := countQuery.LoadOne(&total); err != nil {
		return nil, 0, err
	}

	offset := (pageIndex - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	_, err := baseQuery.OrderDir("created_at", false).
		Limit(uint64(pageSize)).
		Offset(uint64(offset)).
		Load(&bots)
	return bots, total, err
}

// queryPublishedBots queries all published App Bots.
func (d *appBotDB) queryPublishedBots() ([]*appBotModel, error) {
	var bots []*appBotModel
	_, err := d.session.Select("*").From("app_bot").Where("status=?", StatusPublished).Load(&bots)
	return bots, err
}


// queryAvailableBots returns bots available to a given user (each scope capped at 100).
func (d *appBotDB) queryAvailableBots(loginUID, spaceIDFilter string) ([]*appBotModel, error) {
	// Explicit column list excludes token to prevent accidental exposure
	const cols = "id, uid, display_name, description, avatar, scope, space_id, status, welcome_msg, created_by, created_at, updated_at"
	var bots []*appBotModel
	if spaceIDFilter != "" {
		_, err := d.session.SelectBySql(`
			(SELECT `+cols+` FROM app_bot WHERE scope='platform' AND status=1 LIMIT 100)
			UNION ALL
			(SELECT `+cols+` FROM app_bot ab
				INNER JOIN space_member sm ON ab.space_id = sm.space_id
				WHERE sm.uid = ? AND sm.status = 1
				AND ab.scope = 'space' AND ab.status = 1
				AND ab.space_id = ?
				LIMIT 100)
		`, loginUID, spaceIDFilter).Load(&bots)
		return bots, err
	}
	_, err := d.session.SelectBySql(`
		(SELECT `+cols+` FROM app_bot WHERE scope='platform' AND status=1 LIMIT 100)
		UNION ALL
		(SELECT `+cols+` FROM app_bot ab
			INNER JOIN space_member sm ON ab.space_id = sm.space_id
			WHERE sm.uid = ? AND sm.status = 1
			AND ab.scope = 'space' AND ab.status = 1
			LIMIT 100)
	`, loginUID).Load(&bots)
	return bots, err
}

// updateAppBot updates specific fields of an App Bot within a transaction.
func (d *appBotDB) updateAppBot(id string, updates map[string]interface{}) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	_, err = tx.Update("app_bot").SetMap(updates).Where("id=?", id).Exec()
	if err != nil {
		return err
	}
	return tx.Commit()
}

// rotateAppBotToken atomically updates token with optimistic lock (WHERE token=oldToken).
// Returns ErrTokenRotationConflict if another rotation beat us.
var ErrTokenRotationConflict = errors.New("token rotation conflict")

func (d *appBotDB) rotateAppBotToken(id, oldToken, newToken string) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	result, err := tx.UpdateBySql(
		"UPDATE app_bot SET token=?, updated_at=NOW() WHERE id=? AND token=?",
		newToken, id, oldToken,
	).Exec()
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrTokenRotationConflict
	}
	return tx.Commit()
}

// deleteAppBot physically deletes an App Bot within a transaction.
func (d *appBotDB) deleteAppBot(id string) error {
	tx, err := d.session.Begin()
	if err != nil {
		return err
	}
	defer tx.RollbackUnlessCommitted()

	_, err = tx.DeleteFrom("app_bot").Where("id=?", id).Exec()
	if err != nil {
		return err
	}
	return tx.Commit()
}
