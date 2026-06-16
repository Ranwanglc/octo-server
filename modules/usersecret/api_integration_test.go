package usersecret

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/util"
	"github.com/Mininglamp-OSS/octo-lib/testutil"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "github.com/Mininglamp-OSS/octo-server/modules/robot"
)

// newAPITestServer 起测试 server。usersecret 模块经 init() 自动注册,NewTestServer
// 的 module.Setup 已挂好 /v1/manager/secrets/* 与 /v1/bot/secrets/resolve 路由,
// 这里只补 i18n renderer(对齐 main.go),不再手动 Route(否则重复注册 panic)。
func newAPITestServer(t *testing.T) (http.Handler, *config.Context) {
	t.Helper()
	s, ctx := testutil.NewTestServer()
	require.NoError(t, testutil.CleanAllTables(ctx))
	s.GetRoute().SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	return s.GetRoute(), ctx
}

// seedSession 为 uid 注入一个会话 token,使 AuthMiddleware 认其为登录用户。
func seedSession(t *testing.T, ctx *config.Context, uid string) string {
	t.Helper()
	token := "tok_" + util.GenerUUID()[:12]
	require.NoError(t, ctx.Cache().Set(
		ctx.GetConfig().Cache.TokenCachePrefix+token, uid+"@"+uid))
	return token
}

func userReq(t *testing.T, method, path, sessionToken string, body interface{}) *http.Request {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("token", sessionToken)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func botReq(t *testing.T, path, botToken string, body interface{}) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	if botToken != "" {
		req.Header.Set("Authorization", "Bearer "+botToken)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestAPI_CRUDFlow_NoPlaintextLeak(t *testing.T) {
	route, ctx := newAPITestServer(t)
	uid := "u_" + util.GenerUUID()[:8]
	token := seedSession(t, ctx, uid)

	// create
	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
		"display_name": "Claude 密钥", "kind": "llm", "key": "sk-secret-abcd1234",
	}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "sk-secret-abcd1234", "create 响应不得含明文")
	var created secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.SecretID)
	assert.Equal(t, "****1234", created.Masked)

	// list 不漏明文/密文
	w = httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodGet, "/v1/manager/secrets", token, nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, "sk-secret-abcd1234")
	assert.NotContains(t, strings.ToLower(body), "cipher")

	// update (换 key)
	w = httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPut, "/v1/manager/secrets/"+created.SecretID, token, map[string]string{
		"key": "sk-rotated-wxyz9999",
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "sk-rotated-wxyz9999")

	// delete
	w = httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodDelete, "/v1/manager/secrets/"+created.SecretID, token, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestAPI_Create_DuplicateName(t *testing.T) {
	route, ctx := newAPITestServer(t)
	uid := "u_" + util.GenerUUID()[:8]
	token := seedSession(t, ctx, uid)
	body := map[string]string{"display_name": "My Key", "kind": "external", "key": "v1"}

	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, body))
	require.Equal(t, http.StatusCreated, w.Code)

	w = httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
		"display_name": "my  key", "kind": "external", "key": "v2",
	}))
	assert.Equal(t, http.StatusConflict, w.Code, w.Body.String())
}

func TestAPI_CRUD_RequiresAuth(t *testing.T) {
	route, _ := newAPITestServer(t)
	w := httptest.NewRecorder()
	route.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/manager/secrets", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAPI_Update_UnchangedNamePlusNewKey 回归 R3.1:前端整表提交「未改的
// display_name + 新 key」时,api.update 先 rename(同名,幂等)再 updateKey。
// 默认 MySQL DSN 下同名 UPDATE 报 0 changed rows,旧逻辑会在 rename 步误返 404,
// 根本走不到 key 轮换。修复后应 200,且新 key 经 resolve 生效。
func TestAPI_Update_UnchangedNamePlusNewKey(t *testing.T) {
	route, ctx := newAPITestServer(t)
	owner := "u_combo_" + util.GenerUUID()[:6]
	token := seedSession(t, ctx, owner)

	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
		"display_name": "combo key", "kind": "external", "key": "old-key-1111",
	}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// 整表提交:display_name 未变 + 新 key。
	w = httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPut, "/v1/manager/secrets/"+created.SecretID, token, map[string]string{
		"display_name": "combo key", "key": "new-key-2222",
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var updated secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updated))
	assert.Equal(t, "****2222", updated.Masked, "key 轮换必须生效")

	// resolve 应解出新 key。
	botToken := "bf_" + util.GenerUUID()[:16]
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, creator_uid, bot_token, status) VALUES (?, ?, ?, 1)",
		"botCombo_"+util.GenerUUID()[:6], owner, botToken,
	).Exec()
	require.NoError(t, err)

	w = httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", botToken, map[string]string{"query": created.SecretID}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "new-key-2222", resp.Value)
}

func TestAPI_Resolve_ByBotToken(t *testing.T) {
	route, ctx := newAPITestServer(t)
	owner := "u_" + util.GenerUUID()[:8]
	token := seedSession(t, ctx, owner)

	// owner 创建一个 key
	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
		"display_name": "Claude 密钥", "kind": "llm", "key": "sk-resolve-me-7777",
	}))
	require.Equal(t, http.StatusCreated, w.Code)
	var created secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	// owner 的 bot
	botToken := "bf_" + util.GenerUUID()[:16]
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, creator_uid, bot_token, status) VALUES (?, ?, ?, 1)",
		"bot_"+util.GenerUUID()[:8], owner, botToken,
	).Exec()
	require.NoError(t, err)

	// resolve by display_name → 返明文
	w = httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", botToken, map[string]string{"query": "claude密钥"}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		SecretID string `json:"secret_id"`
		Value    string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, created.SecretID, resp.SecretID)
	assert.Equal(t, "sk-resolve-me-7777", resp.Value)
}

func TestAPI_Resolve_RejectsBadToken(t *testing.T) {
	route, ctx := newAPITestServer(t)
	// 无 token
	w := httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", "", map[string]string{"query": "x"}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 未知 bot token
	w = httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", "bf_unknown_xyz", map[string]string{"query": "x"}))
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 鉴权失败也必须留审计(安全模块的越权探测线索)。两次坏 token → 两条 unauthorized。
	var n int
	_, err := ctx.DB().Select("count(*)").From("user_secret_resolve_audit").
		Where("result=?", resultUnauthorized).Load(&n)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "鉴权失败的 resolve 必须留痕")
}

// TestAPI_Resolve_MaxSizeKey_RoundTripThroughDB 边界测试:最大尺寸明文经
// AES-GCM 加密(+前缀/nonce/tag 开销)落 DB 后仍能完整 round-trip,验证
// cipher_text 列宽足够,不会被严格 MySQL 拒插或非严格模式静默截断成脏行。
func TestAPI_Resolve_MaxSizeKey_RoundTripThroughDB(t *testing.T) {
	route, ctx := newAPITestServer(t)
	owner := "u_max_" + util.GenerUUID()[:6]
	token := seedSession(t, ctx, owner)

	maxKey := strings.Repeat("k", maxPlaintext) // 8192 字节明文上限

	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
		"display_name": "max size key", "kind": "external", "key": maxKey,
	}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))

	botToken := "bf_" + util.GenerUUID()[:16]
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, creator_uid, bot_token, status) VALUES (?, ?, ?, 1)",
		"botMax_"+util.GenerUUID()[:6], owner, botToken,
	).Exec()
	require.NoError(t, err)

	w = httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", botToken, map[string]string{"query": created.SecretID}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Value string `json:"value"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, maxKey, resp.Value, "最大尺寸 key 经 DB round-trip 后必须完整无截断")
}

func TestAPI_Resolve_OwnerIsolation(t *testing.T) {
	route, ctx := newAPITestServer(t)
	ownerA := "u_a_" + util.GenerUUID()[:6]
	tokenA := seedSession(t, ctx, ownerA)

	// A 建 key
	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", tokenA, map[string]string{
		"display_name": "A secret", "kind": "external", "key": "a-only-value",
	}))
	require.Equal(t, http.StatusCreated, w.Code)
	var a secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &a))

	// B 的 bot 拿 A 的 secret_id → not_found(owner 隔离)
	ownerB := "u_b_" + util.GenerUUID()[:6]
	botB := "bf_" + util.GenerUUID()[:16]
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, creator_uid, bot_token, status) VALUES (?, ?, ?, 1)",
		"botB_"+util.GenerUUID()[:6], ownerB, botB,
	).Exec()
	require.NoError(t, err)

	w = httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", botB, map[string]string{"query": a.SecretID}))
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
}

func TestAPI_Resolve_Ambiguous(t *testing.T) {
	route, ctx := newAPITestServer(t)
	owner := "u_amb_" + util.GenerUUID()[:6]
	token := seedSession(t, ctx, owner)

	for _, n := range []string{"我的密钥", "我的米要"} {
		w := httptest.NewRecorder()
		route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
			"display_name": n, "kind": "external", "key": "v-" + n,
		}))
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	}

	botToken := "bf_" + util.GenerUUID()[:16]
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, creator_uid, bot_token, status) VALUES (?, ?, ?, 1)",
		"botAmb_"+util.GenerUUID()[:6], owner, botToken,
	).Exec()
	require.NoError(t, err)

	w := httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", botToken, map[string]string{"query": "我的miyao"}))
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "v-我的密钥", "歧义响应不得含明文")

	// 歧义走统一 i18n 错误信封:候选列表在 error.details.candidates,
	// 且带本地化 message 与 error.http_status(422)。
	var resp struct {
		Status int `json:"status"`
		Error  struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			HTTPStatus int    `json:"http_status"`
			Details    struct {
				Candidates []secretView `json:"candidates"`
			} `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.Equal(t, "err.server.usersecret.ambiguous", resp.Error.Code)
	assert.NotEmpty(t, resp.Error.Message, "歧义错误必须带本地化 message")
	assert.Equal(t, http.StatusUnprocessableEntity, resp.Error.HTTPStatus)
	assert.Len(t, resp.Error.Details.Candidates, 2)
}

// TestAPI_Resolve_AuditQueryTruncatedOnRuneBoundary 回归 P1.4:超长 CJK query
// 必须按 rune 边界截断后落审计,不能按字节切断多字节码点(否则 VARCHAR(128) 插入
// 失败或存入非法 UTF-8)。审计行应可读、长度 ≤128 字符、且是合法 UTF-8。
func TestAPI_Resolve_AuditQueryTruncatedOnRuneBoundary(t *testing.T) {
	route, ctx := newAPITestServer(t)
	owner := "u_trunc_" + util.GenerUUID()[:6]
	botToken := "bf_" + util.GenerUUID()[:16]
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, creator_uid, bot_token, status) VALUES (?, ?, ?, 1)",
		"botTrunc_"+util.GenerUUID()[:6], owner, botToken,
	).Exec()
	require.NoError(t, err)

	// 200 个中文字符(UTF-8 下 600 字节),远超 128。无匹配 → not_found,但审计仍落。
	longQuery := strings.Repeat("密", 200)
	w := httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", botToken, map[string]string{"query": longQuery}))
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())

	var row struct {
		Query string
	}
	found, err := ctx.DB().Select("query").From("user_secret_resolve_audit").
		Where("owner_uid=?", owner).Load(&row)
	require.NoError(t, err)
	require.Equal(t, 1, found)
	assert.True(t, utf8.ValidString(row.Query), "审计 query 必须是合法 UTF-8(不能切断码点)")
	assert.Equal(t, 128, len([]rune(row.Query)), "审计 query 必须按 rune 边界截断到 128 字符")
	assert.Equal(t, strings.Repeat("密", 128), row.Query)
}

// TestAPI_Resolve_CreateReturnsTimestamps 回归 Jerry-Xin/lml2468:create 201 响应
// 必须带真实 created_at/updated_at,而非 DB 默认值在内存模型上留下的零值
// ("0001-01-01T00:00:00Z")。
func TestAPI_Resolve_CreateReturnsTimestamps(t *testing.T) {
	route, ctx := newAPITestServer(t)
	uid := "u_ts_" + util.GenerUUID()[:6]
	token := seedSession(t, ctx, uid)

	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
		"display_name": "ts key", "kind": "external", "key": "sk-ts-0001",
	}))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.NotEmpty(t, created.CreatedAt)
	assert.NotEmpty(t, created.UpdatedAt)
	assert.NotContains(t, created.CreatedAt, "0001-01-01", "create 响应不得返回零值时间戳")
	assert.NotContains(t, created.UpdatedAt, "0001-01-01", "create 响应不得返回零值时间戳")
}

func TestAPI_Resolve_WritesAudit(t *testing.T) {
	route, ctx := newAPITestServer(t)
	owner := "u_aud_" + util.GenerUUID()[:6]
	token := seedSession(t, ctx, owner)

	w := httptest.NewRecorder()
	route.ServeHTTP(w, userReq(t, http.MethodPost, "/v1/manager/secrets", token, map[string]string{
		"display_name": "audit key", "kind": "external", "key": "sk-audit-1234",
	}))
	require.Equal(t, http.StatusCreated, w.Code)
	var v secretView
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &v))

	botToken := "bf_" + util.GenerUUID()[:16]
	robotID := "botAud_" + util.GenerUUID()[:6]
	_, err := ctx.DB().InsertBySql(
		"INSERT INTO robot (robot_id, creator_uid, bot_token, status) VALUES (?, ?, ?, 1)",
		robotID, owner, botToken,
	).Exec()
	require.NoError(t, err)

	w = httptest.NewRecorder()
	route.ServeHTTP(w, botReq(t, "/v1/bot/secrets/resolve", botToken, map[string]string{"query": v.SecretID}))
	require.Equal(t, http.StatusOK, w.Code)

	// 审计落了一条 ok 记录,且不含明文。
	var row struct {
		CallerID string
		SecretID string
		Result   string
		Query    string
	}
	found, err := ctx.DB().Select("caller_id", "secret_id", "result", "query").
		From("user_secret_resolve_audit").
		Where("owner_uid=?", owner).Load(&row)
	require.NoError(t, err)
	require.Equal(t, 1, found)
	assert.Equal(t, robotID, row.CallerID)
	assert.Equal(t, v.SecretID, row.SecretID)
	assert.Equal(t, resultOK, row.Result)
	assert.NotContains(t, row.Query, "sk-audit-1234")
}
