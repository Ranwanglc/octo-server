package user

import (
	"database/sql"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/gocraft/dbr/v2"
)

// verificationModel 对应 user_verification 表。
//
// ⚠️ 此表是 Aegis identity_verification claims 的 **local read-through cache**,
// 不是 source of truth。权威源永远是 Aegis IdP,本表只缓存最近一次拉取到的快照,
// 供 OCTO profile 接口着色徽章用(避免每次 profile 查询都同步打 Aegis admin API)。
//
// 写入触发点(2026-05-10 起 Aegis OIDC 直切之后):
//  1. OIDC 登录 callback (modules/oidc/api.go) —— 用户走 IdP 登录时顺带同步
//  2. OIDC SyncWorker (modules/oidc/sync_worker.go) —— RT 轮转成功后用新 access_token
//     调一次 /userinfo,命中实名 claims 就 UpsertVerificationFromOIDC
//     (YUJ-405,覆盖所有 OIDC 登录过的用户,最多 Interval 延迟感知 Aegis 侧变化)
//
// (YUJ-398 基于 Aegis admin API + client_credentials 的 pull-from-aegis 方案已归档:
// Aegis 不提供 admin API + client_credentials grant,生产无法工作。见 YUJ-405。)
//
// 读取路径默认可能 stale,最大滞后取决于上面 2 个写入触发点的触发频率;
// 典型场景"用户刚在 Aegis 完成实名别人查他 profile"依赖对方下次 OIDC 登录或
// SyncWorker 下一轮 tick 才会刷新(延迟 ≤ SyncInterval,生产默认 15min)。
//
// 自 2026-05-10 起（YUJ-382 / Aegis OIDC Phase 1),OIDC callback(modules/oidc/api.go)
// 首次成为 user_verification 表的写入方,权威源从 dmwork-verify-service 迁移到 Aegis IdP。
// 历史:此前由 dmwork-verify-service 经 HMAC POST /v1/internal/verification/complete
//       写入,该链路已随 Aegis OIDC 直切方案废弃;api_verification.go 整个文件被删除。
//
// 表 schema 不变:迁移期 OCTO 侧继续基于本表给 profile 着色,前端协议无感知。
type verificationModel struct {
	UserID     string         `db:"user_id"`
	RealName   string         `db:"real_name"`
	Source     string         `db:"source"`
	SourceSub  string         `db:"source_sub"`
	EmpID      dbr.NullString `db:"emp_id"`
	Dept       dbr.NullString `db:"dept"`
	Email      dbr.NullString `db:"email"`
	Mobile     dbr.NullString `db:"mobile"`
	VerifiedAt time.Time      `db:"verified_at"`
	UpdatedAt  time.Time      `db:"updated_at"`
}

// verificationDB 封装 user_verification 表访问。
type verificationDB struct {
	session *dbr.Session
	ctx     *config.Context
}

func newVerificationDB(ctx *config.Context) *verificationDB {
	return &verificationDB{
		session: ctx.DB(),
		ctx:     ctx,
	}
}

// QueryByUID 查询单个用户的实名记录；无记录返回 (nil, nil)。
func (d *verificationDB) QueryByUID(uid string) (*verificationModel, error) {
	var m *verificationModel
	_, err := d.session.Select("*").From("user_verification").Where("user_id=?", uid).Load(&m)
	return m, err
}

// QueryByUIDs 批量查询实名记录，返回 uid → model 的映射。
// 用于批量详情接口避免 N+1。
func (d *verificationDB) QueryByUIDs(uids []string) (map[string]*verificationModel, error) {
	result := make(map[string]*verificationModel, len(uids))
	if len(uids) == 0 {
		return result, nil
	}
	var list []*verificationModel
	_, err := d.session.Select("*").From("user_verification").Where("user_id IN ?", uids).Load(&list)
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		result[m.UserID] = m
	}
	return result, nil
}

// Upsert 按 user_id 幂等写入。存在则更新,不存在则插入。
// OIDC callback(modules/oidc/api.go)是唯一写入方,对同一用户每次 OIDC 再登录都会被调用。
//
// 🚨 Phase 1 NULL overwrite 热修(Mininglamp-OSS/octo-server#1334 / YUJ-390,2026-05-10):
// 旧版 SQL 对所有列无条件 `col = VALUES(col)`,会把 OIDC claims 里未返回的
// emp_id / dept / mobile(NullString{}) 以及空 sub 全部冲掉历史值,造成再登录
// 一次原先由 verify-service 写入的工号/部门/手机号/来源 sub 全部变 NULL。
//
// 修复语义(与字段是否 NOT NULL 对齐):
//   - emp_id / dept / mobile(DEFAULT NULL):`COALESCE(VALUES(col), col)` —
//     新值为 NULL 时保留旧值,新值非 NULL 时正常覆盖。
//   - source_sub(NOT NULL VARCHAR,空串合法但表示"上游未提供"):
//     `IF(VALUES(source_sub)='', source_sub, VALUES(source_sub))` — 空串视为
//     "保留旧值"。COALESCE 在这里不适用(空串不是 NULL)。
//   - real_name / source / email / verified_at:继续 VALUES(col) 直接覆盖 —
//     这些都是每次 OIDC callback 明确给出的权威字段,允许再登录刷新。
//     email 目前不在保护列表,若未来 claims 允许"已注册但隐藏邮箱"再加保护。
func (d *verificationDB) Upsert(m *verificationModel) error {
	if m == nil || m.UserID == "" {
		return nil
	}
	// dbr 的 InsertStmt 不暴露 Suffix,这里用 InsertBySql + ON DUPLICATE KEY UPDATE 完成 upsert。
	// 列顺序与占位符对齐;updated_at 走列默认 ON UPDATE CURRENT_TIMESTAMP 自动更新。
	_, err := d.session.InsertBySql(
		"INSERT INTO user_verification "+
			"(user_id, real_name, source, source_sub, emp_id, dept, email, mobile, verified_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE "+
			"real_name=VALUES(real_name), "+
			"source=VALUES(source), "+
			"source_sub=IF(VALUES(source_sub)='', source_sub, VALUES(source_sub)), "+
			"emp_id=COALESCE(VALUES(emp_id), emp_id), "+
			"dept=COALESCE(VALUES(dept), dept), "+
			"email=VALUES(email), "+
			"mobile=COALESCE(VALUES(mobile), mobile), "+
			"verified_at=VALUES(verified_at)",
		m.UserID, m.RealName, m.Source, m.SourceSub,
		m.EmpID, m.Dept, m.Email, m.Mobile, m.VerifiedAt,
	).Exec()
	return err
}

// nullableVerificationString 封装 "" → SQL NULL 的惯用转换。
//
// 原先这个 helper 叫 nullableString、住在已删除的 api_verification.go 里;
// 随该文件删除后沿用相同语义(TrimSpace 后空 → NULL)搬到本文件,避免 OIDC
// 路径写库时把字面空串落到 emp_id / dept / email / mobile 等允许为 NULL 的列上。
func nullableVerificationString(s string) dbr.NullString {
	if strings.TrimSpace(s) == "" {
		return dbr.NullString{}
	}
	return dbr.NullString{NullString: sql.NullString{String: s, Valid: true}}
}

// DeleteByUID 删除单个用户的实名记录(YUJ-398 Round 1 Jerry-Xin Crit 2 + YUJ-399 Round 3 Crit 4)。
//
// 背景:user_verification 是 local read-through cache,Aegis 才是权威源。
// 当 Aegis 侧用户"取消实名" / "账号注销" / "is_verified=false" 权威态时,
// 如果不清 local row,service.go::GetUserDetail 只要查到任意行就标 RealnameVerified=true,
// 会造成**徽章永久假阳**(Aegis 说未实名,OCTO 徽章仍亮)。
//
// 当前调用方:目前没有调用方(YUJ-405:sync_worker 保守不在 /userinfo is_verified=false
// 时 Delete,避免 Aegis 抖动误清;OIDC callback 通过 Upsert 的 LegalName 非空 gate
// 拒绝写,而非 Delete 旧行)。保留本方法为未来 Aegis webhook 收到明确撤销事件时用。
//
// 调用合同(严格限定,任何一点错都会造成误删):
//   必须在 Aegis **权威确认**用户未实名时才调 —— 例如 Aegis 主动推送的撤销 webhook。
//   严禁在以下场景调:
//     - /userinfo 拉取异常 / 5xx / token 拿不到 → 保守保留旧 row
//     - JSON 解析失败 / 配置错 → 同上
//     - DB 查询错误 → 不触及
//   误删代价:某一次 Aegis 短暂抖动,所有 pulled 用户 cache 被清,下次 pull 又 upsert 回来 ——
//   但中间这段窗口 OCTO 徽章会误显示"未实名",用户发 support ticket。保守是这里的默认。
//
// 语义:
//   - uid 空串 → no-op + nil err(防御编程错误,不让 DELETE FROM ... WHERE user_id='' 误删)
//   - 行不存在 → 仍 nil err(幂等);调用方不依赖"是否删掉了"的返回值
//   - DB error → 原样返回给调用方记 warn
func (d *verificationDB) DeleteByUID(uid string) error {
	if strings.TrimSpace(uid) == "" {
		return nil
	}
	_, err := d.session.DeleteFrom("user_verification").Where("user_id=?", uid).Exec()
	return err
}
