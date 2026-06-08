package usersecret

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeStore 是一个可注入故障的 secretStore 实现,用于在不连 DB 的前提下强制
// 某次查询报错,验证 resolve 的错误分类(R3.2)。审计写入捕获到内存里供断言。
type fakeStore struct {
	queryBySecretIDFn func(ownerUID, secretID string) (*aliasModel, error)
	listByOwnerFn     func(ownerUID string) ([]*aliasModel, error)
	queryBotByTokenFn func(botToken string) (*botIdentity, error)

	audits []*resolveAuditModel
}

func (f *fakeStore) insertAlias(*aliasModel) error { return nil }

func (f *fakeStore) queryBySecretID(ownerUID, secretID string) (*aliasModel, error) {
	if f.queryBySecretIDFn != nil {
		return f.queryBySecretIDFn(ownerUID, secretID)
	}
	return nil, nil
}

func (f *fakeStore) listByOwner(ownerUID string) ([]*aliasModel, error) {
	if f.listByOwnerFn != nil {
		return f.listByOwnerFn(ownerUID)
	}
	return nil, nil
}

func (f *fakeStore) updateSecret(string, string, []byte, string) (int64, error) { return 0, nil }
func (f *fakeStore) renameAlias(string, string, string, string) (int64, error)  { return 0, nil }
func (f *fakeStore) deleteAlias(string, string) (int64, error)                  { return 0, nil }
func (f *fakeStore) touchLastUsed(string, string) error                         { return nil }

func (f *fakeStore) insertResolveAudit(m *resolveAuditModel) error {
	f.audits = append(f.audits, m)
	return nil
}

func (f *fakeStore) queryBotByToken(botToken string) (*botIdentity, error) {
	if f.queryBotByTokenFn != nil {
		return f.queryBotByTokenFn(botToken)
	}
	return nil, nil
}

func newFaultEncryptor(t *testing.T) *encryptor {
	t.Helper()
	enc, err := newEncryptor()
	require.NoError(t, err)
	return enc
}

// TestService_Resolve_StoreError_ClassifiesInternal 强制 store 在 resolve 链路里报错,
// 断言结果分类是 internal_error(而非 not_found / decrypt_fail)。这是 R3.2 要求的
// 真回归守卫:若有人把 service.resolve 的 DB 错误分支改回 bare resolveOutcome{}
// (原 P1.5 bug),本测试会失败。覆盖 secret_id 直查 与 名称列表 两条 store 错误路径。
func TestService_Resolve_StoreError_ClassifiesInternal(t *testing.T) {
	enc := newFaultEncryptor(t)

	t.Run("queryBySecretID error", func(t *testing.T) {
		fs := &fakeStore{
			queryBySecretIDFn: func(string, string) (*aliasModel, error) {
				return nil, errors.New("db down")
			},
		}
		svc := newService(fs, enc)
		out, err := svc.resolve("owner-x", "some-query")
		require.Error(t, err)
		assert.Equal(t, resultInternalError, out.result,
			"secret_id 直查 DB 报错必须分类为 internal_error,不能误记 not_found")
		assert.NotErrorIs(t, err, errNotFound)
	})

	t.Run("listByOwner error", func(t *testing.T) {
		fs := &fakeStore{
			// secret_id 直查未命中(nil,nil),进入名称匹配再让 listByOwner 报错。
			queryBySecretIDFn: func(string, string) (*aliasModel, error) { return nil, nil },
			listByOwnerFn: func(string) ([]*aliasModel, error) {
				return nil, errors.New("db down")
			},
		}
		svc := newService(fs, enc)
		out, err := svc.resolve("owner-x", "克劳德密钥")
		require.Error(t, err)
		assert.Equal(t, resultInternalError, out.result,
			"名称匹配 listByOwner DB 报错必须分类为 internal_error")
		assert.NotErrorIs(t, err, errNotFound)
	})
}

// newFaultAPI 构造一个不连 DB、store 注入故障的 API + 仅挂 resolve 的路由,
// 用于断言 API 层 resolve 在各故障分支写入审计的 result 分类正确。
func newFaultAPI(t *testing.T, fs *fakeStore) (http.Handler, *fakeStore) {
	t.Helper()
	enc := newFaultEncryptor(t)
	a := &API{
		Log:     log.NewTLog("UserSecretTest"),
		store:   fs,
		enc:     enc,
		enabled: true,
	}
	a.svc = newService(fs, enc)

	r := wkhttp.New()
	r.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.DefaultLanguage)))
	r.POST("/v1/bot/secrets/resolve", a.resolve)
	return r, fs
}

func faultResolveReq(query string) *http.Request {
	body, _ := json.Marshal(map[string]string{"query": query})
	req := httptest.NewRequest(http.MethodPost, "/v1/bot/secrets/resolve",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer bf_fault_token")
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestAPI_Resolve_AuthQueryError_AuditsInternal 强制 queryBotByToken(鉴权查询)报错,
// 断言:返回 5xx,且审计行 result=internal_error(而非 decrypt_fail/unauthorized)。
// R3.2:覆盖 api.go 的 auth-query DB 错误路径,防回退到误分类。
func TestAPI_Resolve_AuthQueryError_AuditsInternal(t *testing.T) {
	fs := &fakeStore{
		queryBotByTokenFn: func(string) (*botIdentity, error) {
			return nil, errors.New("db down")
		},
	}
	route, captured := newFaultAPI(t, fs)

	w := httptest.NewRecorder()
	route.ServeHTTP(w, faultResolveReq("anything"))
	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())

	require.Len(t, captured.audits, 1, "鉴权查询报错必须留一条审计")
	assert.Equal(t, resultInternalError, captured.audits[0].Result,
		"auth-query DB 报错必须分类为 internal_error,不能误记 decrypt_fail/unauthorized")
}

// TestAPI_Resolve_ServiceStoreError_AuditsInternal 鉴权通过但 service 层 store 报错,
// 断言审计 result=internal_error。串起 API→service→store 的故障分类。
func TestAPI_Resolve_ServiceStoreError_AuditsInternal(t *testing.T) {
	fs := &fakeStore{
		queryBotByTokenFn: func(string) (*botIdentity, error) {
			return &botIdentity{RobotID: "bot-1", OwnerUID: "owner-1"}, nil
		},
		queryBySecretIDFn: func(string, string) (*aliasModel, error) {
			return nil, errors.New("db down")
		},
	}
	route, captured := newFaultAPI(t, fs)

	w := httptest.NewRecorder()
	route.ServeHTTP(w, faultResolveReq("克劳德密钥"))
	assert.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())

	require.Len(t, captured.audits, 1)
	assert.Equal(t, resultInternalError, captured.audits[0].Result,
		"service 层 store 报错必须分类为 internal_error")
	assert.Equal(t, "owner-1", captured.audits[0].OwnerUID, "已鉴权,审计应带 owner")
}
