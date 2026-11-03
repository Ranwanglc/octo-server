package agent_memory

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

type agentContextTestStore struct {
	profile               *AgentMemoryProfile
	profileErr            error
	profileCalls          int
	mutateProfileOnSecond func(*AgentMemoryProfile)
	policy                *MemoryPolicyVersion
	policyErr             error
	banks                 []MemoryBank
	bankErr               error
	grants                []MemoryGrant
	grantsErr             error
}

func (store *agentContextTestStore) FindProfileByAgentUID(string) (*AgentMemoryProfile, error) {
	store.profileCalls++
	if store.profileCalls == 2 && store.mutateProfileOnSecond != nil {
		store.mutateProfileOnSecond(store.profile)
	}
	return store.profile, store.profileErr
}

func (store *agentContextTestStore) FindBankByID(bankID string) (*MemoryBank, error) {
	if store.bankErr != nil {
		return nil, store.bankErr
	}
	for i := range store.banks {
		if store.banks[i].ID == bankID {
			return &store.banks[i], nil
		}
	}
	return nil, ErrBankNotFound
}

func (store *agentContextTestStore) ListBanks() ([]MemoryBank, error) {
	return store.banks, store.bankErr
}

func (store *agentContextTestStore) ListAgentGrants(string) ([]MemoryGrant, error) {
	return store.grants, store.grantsErr
}

func (store *agentContextTestStore) FindPolicyVersionByID(string) (*MemoryPolicyVersion, error) {
	return store.policy, store.policyErr
}

func TestValidServiceSecretFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name          string
		authorization string
		configured    string
		want          bool
	}{
		{name: "missing configured secret", authorization: "Bearer service-secret", want: false},
		{name: "missing header", configured: "service-secret", want: false},
		{name: "wrong secret", authorization: "Bearer wrong", configured: "service-secret", want: false},
		{name: "valid secret", authorization: "Bearer service-secret", configured: "service-secret", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validServiceSecret(test.authorization, test.configured); got != test.want {
				t.Fatalf("validServiceSecret() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestAgentContextUsesAuthoritativeDBWhenRegistryUnavailable(t *testing.T) {
	service, mock, cleanup := newAgentContextTestService(t, nil)
	defer cleanup()
	store := validAgentContextStore()
	service.store = store
	expectSuccessfulRobotResolution(mock, "agent-token")

	context, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall})
	if err != nil {
		t.Fatal(err)
	}
	if context.AgentUID != "agent" || context.RobotID != "agent" || context.OwnerUID != "owner" {
		t.Fatalf("authenticated identity = %+v", context)
	}
	if context.ScopeType != ProfileScopeSpace || context.SpaceID == nil || *context.SpaceID != "space-a" || context.ProfileStatus != "active" || context.TrustTier != "normal" {
		t.Fatalf("authoritative profile facts = %+v", context)
	}
	if context.CredentialEpoch != 9 || context.Operation != CapabilityRecall || context.PolicyVersionID != "policy-v1" || len(context.AllowedBanks) != 1 {
		t.Fatalf("context = %+v", context)
	}
	if !context.IssuedAt.Equal(service.now()) || !context.ExpiresAt.Equal(service.now().Add(agentContextTTL)) {
		t.Fatalf("context lifetime = %s..%s, want %s..%s", context.IssuedAt, context.ExpiresAt, service.now(), service.now().Add(agentContextTTL))
	}
	payload, err := json.Marshal(context)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("agent-token")) || bytes.Contains(payload, []byte("bearer_token")) {
		t.Fatalf("response leaked bearer token: %s", payload)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	wantFields := []string{
		"agent_uid", "robot_id", "owner_uid", "scope_type", "space_id", "profile_status", "robot_status", "trust_tier",
		"credential_epoch", "authorization_epoch", "operation", "policy_version_id", "allowed_banks", "issued_at", "expires_at",
	}
	if len(wire) != len(wantFields) {
		t.Fatalf("response fields = %v, want exactly %v", sortedJSONKeys(wire), wantFields)
	}
	for _, field := range wantFields {
		if _, ok := wire[field]; !ok {
			t.Fatalf("response missing %q: %s", field, payload)
		}
	}
}

func TestAgentContextRejectsRotatedOrRevokedToken(t *testing.T) {
	service, mock, cleanup := newAgentContextTestService(t, nil)
	defer cleanup()
	service.store = validAgentContextStore()
	expectMissingRobot(mock, "old-token")
	if _, err := service.Resolve(agentContextRequest{BearerToken: "old-token", Operation: CapabilityRecall}); !errors.Is(err, ErrAgentContextAuthentication) {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestAgentContextFailsClosedOnMissingFactsAndDisabledProfile(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*agentContextTestStore)
		operation Capability
		requested string
		wantErr   error
	}{
		{name: "missing profile facts", mutate: func(store *agentContextTestStore) { store.profileErr = errors.New("db unavailable") }, operation: CapabilityRecall, wantErr: ErrAgentContextAuthentication},
		{name: "disabled profile", mutate: func(store *agentContextTestStore) { store.profile.Enabled = false; store.profile.Status = "disabled" }, operation: CapabilityRecall, wantErr: ErrAgentContextAuthentication},
		{name: "missing active policy", mutate: func(store *agentContextTestStore) { store.policyErr = ErrPolicyNotFound }, operation: CapabilityRecall, wantErr: ErrAgentContextAuthentication},
		{name: "retired policy", mutate: func(store *agentContextTestStore) { store.policy.Status = "retired" }, operation: CapabilityRecall, wantErr: ErrAgentContextAuthentication},
		{name: "bank policy mismatch", mutate: func(store *agentContextTestStore) { store.banks[0].PolicyVersionID = "policy-old" }, operation: CapabilityRecall, wantErr: ErrAgentContextForbidden},
		{name: "grant policy mismatch", mutate: func(store *agentContextTestStore) { store.grants[0].PolicyVersionID = "policy-old" }, operation: CapabilityRecall, wantErr: ErrAgentContextForbidden},
		{name: "unknown operation", operation: Capability("unknown"), wantErr: ErrAgentContextForbidden},
		{name: "unknown bank", operation: CapabilityRecall, requested: "opaque-unknown", wantErr: ErrAgentContextForbidden},
		{name: "bank outside scope", mutate: func(store *agentContextTestStore) { store.banks[0].ScopeID = "other-agent" }, operation: CapabilityRecall, requested: "bank-agent", wantErr: ErrAgentContextForbidden},
		{name: "operation outside grant", operation: CapabilityAdmin, requested: "bank-agent", wantErr: ErrAgentContextForbidden},
		{name: "grant facts unavailable", mutate: func(store *agentContextTestStore) { store.grantsErr = errors.New("db unavailable") }, operation: CapabilityRecall, wantErr: ErrAgentContextAuthentication},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, mock, cleanup := newAgentContextTestService(t, nil)
			defer cleanup()
			store := validAgentContextStore()
			if test.mutate != nil {
				test.mutate(store)
			}
			service.store = store
			expectPublishedRobot(mock, "agent-token")
			_, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: test.operation, RequestedBankID: test.requested})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestAgentContextRevocationAndFreshnessAreImmediate(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		mutate func(*agentContextTestStore)
	}{
		{name: "profile disable", mutate: func(store *agentContextTestStore) { store.profile.Enabled = false; store.profile.Status = "disabled" }},
		{name: "grant revoke", mutate: func(store *agentContextTestStore) { revokedAt := now; store.grants[0].RevokedAt = &revokedAt }},
		{name: "grant deny", mutate: func(store *agentContextTestStore) {
			store.grants = append(store.grants, MemoryGrant{PrincipalType: string(PrincipalTypeAgent), PrincipalID: "agent", BankID: "bank-agent", Capability: string(CapabilityRecall), Effect: string(GrantEffectDeny), PolicyVersionID: "policy-v1"})
		}},
		{name: "policy version change", mutate: func(store *agentContextTestStore) {
			store.profile.PolicyVersionID = "policy-v2"
			store.policy = &MemoryPolicyVersion{ID: "policy-v2", Status: "active", DocumentJSON: []byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["recall","propose","curate","admin"]}`)}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, mock, cleanup := newAgentContextTestService(t, nil)
			defer cleanup()
			store := validAgentContextStore()
			service.store = store
			expectSuccessfulRobotResolution(mock, "agent-token")
			issued, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall})
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(store)
			expectPublishedRobot(mock, "agent-token")
			fresh, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall})
			if test.name == "policy version change" {
				if err == nil || fresh != nil {
					t.Fatalf("Resolve() after policy change = %+v, %v; stale grant/bank facts must deny", fresh, err)
				}
			} else if !errors.Is(err, ErrAgentContextAuthentication) && !errors.Is(err, ErrAgentContextForbidden) {
				t.Fatalf("Resolve() after %s error = %v", test.name, err)
			}
			if issued.ExpiresAt.Sub(issued.IssuedAt) != agentContextTTL {
				t.Fatalf("issued lifetime = %s", issued.ExpiresAt.Sub(issued.IssuedAt))
			}
		})
	}
}

func TestAgentContextRevalidationRejectsTokenRevokedDuringResolution(t *testing.T) {
	service, mock, cleanup := newAgentContextTestService(t, nil)
	defer cleanup()
	service.store = validAgentContextStore()
	expectPublishedRobot(mock, "agent-token")
	expectMissingRobot(mock, "agent-token")
	if _, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall}); !errors.Is(err, ErrAgentContextAuthentication) {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestAgentContextRevalidationRejectsTrustTierChangeDuringResolution(t *testing.T) {
	service, mock, cleanup := newAgentContextTestService(t, nil)
	defer cleanup()
	store := validAgentContextStore()
	store.mutateProfileOnSecond = func(profile *AgentMemoryProfile) { profile.TrustTier = "high" }
	service.store = store
	expectSuccessfulRobotResolution(mock, "agent-token")
	if _, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall}); !errors.Is(err, ErrAgentContextAuthentication) {
		t.Fatalf("Resolve() error = %v, want authentication failure", err)
	}
}

func TestAgentContextCredentialEpochAndTokenLifecycleRefreshImmediately(t *testing.T) {
	service, mock, cleanup := newAgentContextTestService(t, nil)
	defer cleanup()
	store := validAgentContextStore()
	service.store = store

	expectSuccessfulRobotResolution(mock, "old-token")
	issued, err := service.Resolve(agentContextRequest{BearerToken: "old-token", Operation: CapabilityRecall})
	if err != nil {
		t.Fatal(err)
	}
	store.profile.CredentialEpoch++
	expectMissingRobot(mock, "old-token")
	if _, err := service.Resolve(agentContextRequest{BearerToken: "old-token", Operation: CapabilityRecall}); !errors.Is(err, ErrAgentContextAuthentication) {
		t.Fatalf("old token after rotate error = %v", err)
	}
	for range 2 {
		mock.ExpectQuery("SELECT .*credential_epoch.*").
			WillReturnRows(sqlmock.NewRows([]string{"robot_id", "creator_uid", "space_id", "status", "credential_epoch"}).
				AddRow("agent", "owner", "space-a", 1, 10))
	}
	rotated, err := service.Resolve(agentContextRequest{BearerToken: "new-token", Operation: CapabilityRecall})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.CredentialEpoch != issued.CredentialEpoch+1 {
		t.Fatalf("rotated epoch = %d, want %d", rotated.CredentialEpoch, issued.CredentialEpoch+1)
	}
	for _, token := range []string{"unpublished-token", "deleted-token"} {
		expectMissingRobot(mock, ""+token+"")
		if _, err := service.Resolve(agentContextRequest{BearerToken: token, Operation: CapabilityRecall}); !errors.Is(err, ErrAgentContextAuthentication) {
			t.Fatalf("%s error = %v", token, err)
		}
	}
}

func TestAgentContextExpiryBoundary(t *testing.T) {
	issuedAt := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	service, mock, cleanup := newAgentContextTestService(t, nil)
	defer cleanup()
	service.store = validAgentContextStore()
	service.now = func() time.Time { return issuedAt }
	expectSuccessfulRobotResolution(mock, "agent-token")
	context, err := service.Resolve(agentContextRequest{BearerToken: "agent-token", Operation: CapabilityRecall})
	if err != nil {
		t.Fatal(err)
	}
	if !context.validAt(issuedAt.Add(agentContextTTL - time.Nanosecond)) {
		t.Fatal("context expired before the exact boundary")
	}
	if context.validAt(issuedAt.Add(agentContextTTL)) {
		t.Fatal("context remained valid at the exact boundary")
	}
}

func TestAgentContextHandlerRequiresIndependentServiceCredential(t *testing.T) {
	runtime := &Runtime{gatewayServiceSecret: "service-secret", Contexts: &AgentContextService{}}
	router := wkhttp.New()
	router.POST("/v1/internal/memory/agent-context", runtime.agentContext)
	for _, authorization := range []string{"", "Bearer wrong"} {
		request := httptest.NewRequest(http.MethodPost, "/v1/internal/memory/agent-context", strings.NewReader(`{"bearer_token":"service-secret","operation":"recall"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status = %d, want 401", authorization, response.Code)
		}
	}
}

func TestAgentContextHandlerRejectsUnknownFieldsWithoutCredentialOracle(t *testing.T) {
	runtime := &Runtime{gatewayServiceSecret: "service-secret", Contexts: &AgentContextService{}}
	router := wkhttp.New()
	router.POST("/v1/internal/memory/agent-context", runtime.agentContext)

	var baseline []byte
	for _, token := range []string{"agent-token", "missing-token"} {
		body := `{"bearer_token":"` + token + `","operation":"recall","owner_uid":"caller-claim"}`
		request := httptest.NewRequest(http.MethodPost, "/v1/internal/memory/agent-context", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer service-secret")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("token %q status = %d, want 401", token, response.Code)
		}
		if baseline == nil {
			baseline = append([]byte(nil), response.Body.Bytes()...)
			continue
		}
		if !bytes.Equal(response.Body.Bytes(), baseline) {
			t.Fatalf("unknown-field response varies by token: first=%s second=%s", baseline, response.Body.Bytes())
		}
	}
}

func sortedJSONKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func newAgentContextTestService(t *testing.T, store agentContextStore) (*AgentContextService, sqlmock.Sqlmock, func()) {
	t.Helper()
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	session := (&dbr.Connection{DB: rawDB, Dialect: dialect.MySQL, EventReceiver: &dbr.NullEventReceiver{}}).NewSession(nil)
	service := NewAgentContextService(session, store)
	service.now = func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) }
	return service, mock, func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = rawDB.Close()
	}
}

func expectPublishedRobot(mock sqlmock.Sqlmock, token string) {
	mock.ExpectQuery("SELECT .*credential_epoch.*").
		WillReturnRows(sqlmock.NewRows([]string{"robot_id", "creator_uid", "space_id", "status", "credential_epoch"}).
			AddRow("agent", "owner", "space-a", 1, 9))
}

func expectMissingRobot(mock sqlmock.Sqlmock, token string) {
	mock.ExpectQuery("SELECT .*credential_epoch.*").WillReturnError(sql.ErrNoRows)
}

func expectSuccessfulRobotResolution(mock sqlmock.Sqlmock, token string) {
	expectPublishedRobot(mock, token)
	expectPublishedRobot(mock, token)
}

func validAgentContextStore() *agentContextTestStore {
	return &agentContextTestStore{
		profile: &AgentMemoryProfile{AgentUID: "agent", RobotID: "agent", OwnerUID: "owner", ScopeType: ProfileScopeSpace, SpaceID: ptr("space-a"), Enabled: true, TrustTier: "normal", Status: "active", PolicyVersionID: "policy-v1", CredentialEpoch: 9},
		policy:  &MemoryPolicyVersion{ID: "policy-v1", Status: "active", DocumentJSON: []byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["recall","propose","curate","admin"]}`)},
		banks:   []MemoryBank{{ID: "bank-agent", HindsightBankID: "internal-bank-7", BankType: "private", ScopeType: BankScopeAgent, ScopeID: "agent", PolicyVersionID: "policy-v1", Status: "active", Sensitivity: "confidential"}},
		grants:  []MemoryGrant{{PrincipalType: string(PrincipalTypeAgent), PrincipalID: "agent", BankID: "bank-agent", Capability: string(CapabilityRecall), Effect: string(GrantEffectAllow), PolicyVersionID: "policy-v1"}},
	}
}

func TestPersistenceHandlerUsesDualAuthAndStrictJSONWithoutOracle(t *testing.T) {
	runtime := &Runtime{
		Persistence:          &PersistenceService{},
		gatewayServiceSecret: "service-secret",
	}
	router := wkhttp.New()
	router.POST("/v1/internal/memory/persistence/prepare", runtime.preparePersistence)

	requests := []struct {
		serviceCredential string
		body              string
	}{
		{"", `{"bearer_token":"app-secret","bank_id":"bank-1"}`},
		{"Bearer service-secret", `{"bearer_token":"app-secret","bank_id":"bank-1","plaintext":"forbidden"}`},
	}
	for _, item := range requests {
		request := httptest.NewRequest(http.MethodPost, "/v1/internal/memory/persistence/prepare", strings.NewReader(item.body))
		request.Header.Set("Content-Type", "application/json")
		if item.serviceCredential != "" {
			request.Header.Set("Authorization", item.serviceCredential)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "plaintext") || strings.Contains(response.Body.String(), "app-secret") {
			t.Fatalf("response leaked request fields: %s", response.Body.String())
		}
	}
}
