package agent_memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

type fakeProfileCatalog struct {
	records map[string]AgentMemoryProfileRecord
}

func (catalog *fakeProfileCatalog) FindProfileRecord(agentUID string) (*AgentMemoryProfileRecord, error) {
	record, ok := catalog.records[agentUID]
	if !ok {
		return nil, ErrProfileNotFound
	}
	record.SpaceID = cloneStringPointer(record.SpaceID)
	return &record, nil
}

type synchronizingProfileCatalog struct {
	*fakeProfileCatalog
	store *fakeProfileStore
}

func (catalog *synchronizingProfileCatalog) FindProfileRecord(agentUID string) (*AgentMemoryProfileRecord, error) {
	record, err := catalog.fakeProfileCatalog.FindProfileRecord(agentUID)
	if err != nil {
		return nil, err
	}
	if catalog.store.synced != nil && catalog.store.synced.AgentUID == agentUID {
		record.AgentMemoryProfile = *catalog.store.synced
		record.Version = catalog.store.syncVersion + 1
	}
	return record, nil
}

func (catalog *fakeProfileCatalog) ListProfileRecords(scopeType ProfileScopeType, spaceID string) ([]AgentMemoryProfileRecord, error) {
	var records []AgentMemoryProfileRecord
	for _, record := range catalog.records {
		if record.ScopeType != scopeType {
			continue
		}
		if scopeType == ProfileScopeSpace && (record.SpaceID == nil || *record.SpaceID != spaceID) {
			continue
		}
		record.SpaceID = cloneStringPointer(record.SpaceID)
		records = append(records, record)
	}
	return records, nil
}

type fakeManagementAccess struct {
	spaceRoles map[string]map[string]int
}

func (access *fakeManagementAccess) AuthorizeProfile(ctx context.Context, caller ManagementCaller, profile AgentMemoryProfile) (ManagementDecision, error) {
	if caller.UID != "" && profile.OwnerUID == caller.UID {
		return ManagementDecision{Visible: true, CanManage: true}, nil
	}
	if profile.ScopeType == ProfileScopePlatform {
		canManage := isPlatformAdmin(caller)
		return ManagementDecision{Visible: canManage, CanManage: canManage}, nil
	}
	if profile.SpaceID == nil {
		return ManagementDecision{}, nil
	}
	return access.AuthorizeSpace(ctx, caller, *profile.SpaceID)
}

func (access *fakeManagementAccess) AuthorizeSpace(_ context.Context, caller ManagementCaller, spaceID string) (ManagementDecision, error) {
	role, ok := access.spaceRoles[spaceID][caller.UID]
	if !ok {
		return ManagementDecision{}, nil
	}
	return ManagementDecision{Visible: true, CanManage: role >= 1}, nil
}

func TestProfileAPIPlatformAdminListsAndGetsPlatformProfiles(t *testing.T) {
	runtime := testProfileRuntime()
	router := testProfileRouter(runtime, "platform-admin", "admin")

	list := performProfileRequest(t, router, http.MethodGet, "/v1/memory/agents", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}
	var listBody struct {
		Agents []agentMemoryProfileResponse `json:"agents"`
	}
	decodeProfileResponse(t, list, &listBody)
	if len(listBody.Agents) != 1 || listBody.Agents[0].AgentUID != testAgentA || !listBody.Agents[0].CanManage {
		t.Fatalf("platform list = %+v", listBody.Agents)
	}

	get := performProfileRequest(t, router, http.MethodGet, "/v1/memory/agents/"+testAgentA, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", get.Code, get.Body.String())
	}
	var profile agentMemoryProfileResponse
	decodeProfileResponse(t, get, &profile)
	if !profile.CanManage || profile.OwnerUID != testOwnerA || profile.TrustTier != "normal" {
		t.Fatalf("platform profile = %+v", profile)
	}
}

func TestProfileAPISpaceRolesAndCrossSpaceAntiEnumeration(t *testing.T) {
	tests := []struct {
		name       string
		uid        string
		wantStatus int
		wantManage bool
	}{
		{name: "owner", uid: "space-owner", wantStatus: http.StatusOK, wantManage: true},
		{name: "admin", uid: "space-admin", wantStatus: http.StatusOK, wantManage: true},
		{name: "member", uid: "space-member", wantStatus: http.StatusOK, wantManage: false},
		{name: "cross space", uid: "space-b-admin", wantStatus: http.StatusNotFound},
		{name: "non member", uid: "outsider", wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := testProfileRuntime()
			router := testProfileRouter(runtime, test.uid, "")
			response := performProfileRequest(t, router, http.MethodGet, "/v1/memory/agents/agent-space-a", nil)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantStatus == http.StatusOK {
				var profile agentMemoryProfileResponse
				decodeProfileResponse(t, response, &profile)
				if profile.CanManage != test.wantManage {
					t.Fatalf("can_manage = %t, want %t", profile.CanManage, test.wantManage)
				}
			}
		})
	}
}

func TestProfileAPIListRequiresRequestedScopeAuthorization(t *testing.T) {
	runtime := testProfileRuntime()

	memberRouter := testProfileRouter(runtime, "space-member", "")
	member := performProfileRequest(t, memberRouter, http.MethodGet, "/v1/memory/agents?space_id="+testSpaceA, nil)
	if member.Code != http.StatusOK {
		t.Fatalf("member list status = %d, body = %s", member.Code, member.Body.String())
	}
	var memberBody struct {
		Agents []agentMemoryProfileResponse `json:"agents"`
	}
	decodeProfileResponse(t, member, &memberBody)
	if len(memberBody.Agents) != 1 || memberBody.Agents[0].CanManage {
		t.Fatalf("member list = %+v", memberBody.Agents)
	}

	crossRouter := testProfileRouter(runtime, "space-b-admin", "")
	cross := performProfileRequest(t, crossRouter, http.MethodGet, "/v1/memory/agents?space_id="+testSpaceA, nil)
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-space list status = %d, body = %s", cross.Code, cross.Body.String())
	}
}

func TestProfileAPIUpdateUsesAuthoritativeProfileService(t *testing.T) {
	spaceID := testSpaceA
	existing := AgentMemoryProfile{
		AgentUID: testAgentA, RobotID: testAgentA, OwnerUID: testOwnerA,
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID, Enabled: false,
		TrustTier: "high", PolicyVersionID: "policy-1", CredentialEpoch: 4,
		Version: 2, Status: "disabled",
	}
	store := &fakeProfileStore{profile: &existing}
	authority := &fakeRobotAuthority{state: AuthoritativeRobotState{
		RobotID: testAgentA, AgentUID: testAgentA, OwnerUID: testOwnerA,
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID,
		Lifecycle: RobotLifecycleActive, CredentialEpoch: 5,
	}}
	baseCatalog := &fakeProfileCatalog{records: map[string]AgentMemoryProfileRecord{
		testAgentA: {AgentMemoryProfile: existing, DisplayName: "Agent A"},
	}}
	catalog := &synchronizingProfileCatalog{fakeProfileCatalog: baseCatalog, store: store}
	runtime := &Runtime{
		Profiles: NewProfileService(authority, store), Catalog: catalog,
		Access: &fakeManagementAccess{spaceRoles: map[string]map[string]int{testSpaceA: {"space-admin": 1}}},
	}
	router := testProfileRouter(runtime, "space-admin", "")
	body := []byte(`{"include_own_tentative":true,"expected_version":2}`)
	response := performProfileRequest(t, router, http.MethodPut, "/v1/memory/agents/"+testAgentA, body)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", response.Code, response.Body.String())
	}
	if store.synced == nil {
		t.Fatal("profile was not synchronized")
	}
	assertProfileAuthority(t, store.synced, authority.state)
	if store.synced.TrustTier != "high" || store.synced.CredentialEpoch != 5 {
		t.Fatalf("server-owned fields = trust:%q epoch:%d", store.synced.TrustTier, store.synced.CredentialEpoch)
	}
	if store.syncVersion != 2 {
		t.Fatalf("sync version = %d, want 2", store.syncVersion)
	}
}

func TestProfileAPIUpdateCreatesMissingProfileAtVersionZero(t *testing.T) {
	spaceID := testSpaceA
	record := AgentMemoryProfileRecord{AgentMemoryProfile: AgentMemoryProfile{
		AgentUID: testAgentA, RobotID: testAgentA, OwnerUID: testOwnerA,
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID, Enabled: false,
		TrustTier: DefaultProfileTrustTier, PolicyVersionID: "policy-1", Version: 0, Status: "disabled",
	}, DisplayName: "Agent A"}
	store := &fakeProfileStore{}
	authority := &fakeRobotAuthority{state: AuthoritativeRobotState{
		RobotID: testAgentA, AgentUID: testAgentA, OwnerUID: testOwnerA,
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID,
		Lifecycle: RobotLifecycleActive, CredentialEpoch: 0,
	}}
	baseCatalog := &fakeProfileCatalog{records: map[string]AgentMemoryProfileRecord{testAgentA: record}}
	runtime := &Runtime{
		Profiles: NewProfileService(authority, store),
		Catalog:  baseCatalog,
		Access:   &fakeManagementAccess{spaceRoles: map[string]map[string]int{testSpaceA: {"space-owner": 2}}},
	}
	response := performProfileRequest(t, testProfileRouter(runtime, "space-owner", ""), http.MethodPut, "/v1/memory/agents/"+testAgentA, []byte(`{"include_own_tentative":true,"expected_version":0}`))
	if response.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	if store.created == nil || store.created.Enabled || !store.created.IncludeOwnTentative {
		t.Fatalf("created profile = %+v", store.created)
	}
}

func TestProfileAPIUpdateRejectsStaleVersion(t *testing.T) {
	spaceID := testSpaceA
	existing := AgentMemoryProfile{
		AgentUID: testAgentA, RobotID: testAgentA, OwnerUID: testOwnerA,
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID, PolicyVersionID: "policy-1",
		Version: 3, Status: "disabled", TrustTier: "normal",
	}
	store := &fakeProfileStore{profile: &existing}
	runtime := &Runtime{
		Profiles: NewProfileService(&fakeRobotAuthority{state: AuthoritativeRobotState{
			RobotID: testAgentA, AgentUID: testAgentA, OwnerUID: testOwnerA,
			ScopeType: ProfileScopeSpace, SpaceID: &spaceID,
			Lifecycle: RobotLifecycleActive,
		}}, store),
		Catalog: &fakeProfileCatalog{records: map[string]AgentMemoryProfileRecord{testAgentA: {AgentMemoryProfile: existing, DisplayName: "Agent A"}}},
		Access:  &fakeManagementAccess{spaceRoles: map[string]map[string]int{testSpaceA: {"space-admin": 1}}},
	}
	response := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/"+testAgentA, []byte(`{"include_own_tentative":true,"expected_version":2}`))
	if response.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestProfileAPIUpdateMemberForbiddenAndHiddenResourceNotFound(t *testing.T) {
	runtime := testProfileRuntime()
	body := []byte(`{"enabled":true,"expected_version":1}`)

	member := performProfileRequest(t, testProfileRouter(runtime, "space-member", ""), http.MethodPut, "/v1/memory/agents/agent-space-a", body)
	if member.Code != http.StatusForbidden {
		t.Fatalf("member update status = %d, body = %s", member.Code, member.Body.String())
	}
	cross := performProfileRequest(t, testProfileRouter(runtime, "space-b-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a", body)
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-space update status = %d, body = %s", cross.Code, cross.Body.String())
	}
	missing := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/missing", body)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing update status = %d, body = %s", missing.Code, missing.Body.String())
	}
}

func TestManagementAccessAdapterUsesActiveSpaceRoleAndFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		rows    *sqlmock.Rows
		want    ManagementDecision
		wantErr error
	}{
		{name: "owner", rows: sqlmock.NewRows([]string{"role"}).AddRow(2), want: ManagementDecision{Visible: true, CanManage: true}},
		{name: "admin", rows: sqlmock.NewRows([]string{"role"}).AddRow(1), want: ManagementDecision{Visible: true, CanManage: true}},
		{name: "member", rows: sqlmock.NewRows([]string{"role"}).AddRow(0), want: ManagementDecision{Visible: true, CanManage: false}},
		{name: "missing", rows: sqlmock.NewRows([]string{"role"}), want: ManagementDecision{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rawDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer rawDB.Close()
			mock.ExpectQuery("SELECT role FROM space_member").WillReturnRows(test.rows)
			adapter := NewManagementAccessAdapter((&dbr.Connection{DB: rawDB, Dialect: dialect.MySQL, EventReceiver: &dbr.NullEventReceiver{}}).NewSession(nil))
			decision, err := adapter.AuthorizeSpace(context.Background(), ManagementCaller{UID: "caller"}, testSpaceA)
			if !errors.Is(err, test.wantErr) || decision != test.want {
				t.Fatalf("decision = %+v, err = %v, want %+v, %v", decision, err, test.want, test.wantErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}

	decision, err := NewManagementAccessAdapter(nil).AuthorizeSpace(context.Background(), ManagementCaller{UID: "caller"}, testSpaceA)
	if decision != (ManagementDecision{}) || !errors.Is(err, ErrManagementFactsUnavailable) {
		t.Fatalf("nil adapter decision = %+v, err = %v", decision, err)
	}
}

func TestManagementAccessProfileOwnerCanManageOwnRobot(t *testing.T) {
	adapter := NewManagementAccessAdapter(nil)
	decision, err := adapter.AuthorizeProfile(context.Background(), ManagementCaller{UID: "robot-owner"}, AgentMemoryProfile{
		RobotID: "robot-a", OwnerUID: "robot-owner", ScopeType: ProfileScopeSpace,
	})
	if err != nil || decision != (ManagementDecision{Visible: true, CanManage: true}) {
		t.Fatalf("decision = %+v, err = %v", decision, err)
	}
}

func testProfileRuntime() *Runtime {
	spaceA := testSpaceA
	spaceB := testSpaceB
	return &Runtime{
		Catalog: &fakeProfileCatalog{records: map[string]AgentMemoryProfileRecord{
			testAgentA:      {AgentMemoryProfile: AgentMemoryProfile{AgentUID: testAgentA, RobotID: "robot-platform", OwnerUID: testOwnerA, ScopeType: ProfileScopePlatform, Enabled: true, TrustTier: "normal", Status: "active", Version: 1}, DisplayName: "Platform Agent"},
			"agent-space-a": {AgentMemoryProfile: AgentMemoryProfile{AgentUID: "agent-space-a", RobotID: "robot-space-a", OwnerUID: "space-owner", ScopeType: ProfileScopeSpace, SpaceID: &spaceA, Enabled: true, TrustTier: "normal", Status: "active", Version: 1}, DisplayName: "Space A Agent"},
			"agent-space-b": {AgentMemoryProfile: AgentMemoryProfile{AgentUID: "agent-space-b", RobotID: "robot-space-b", OwnerUID: "space-b-owner", ScopeType: ProfileScopeSpace, SpaceID: &spaceB, Enabled: true, TrustTier: "normal", Status: "active", Version: 1}, DisplayName: "Space B Agent"},
		}},
		Access: &fakeManagementAccess{spaceRoles: map[string]map[string]int{
			testSpaceA: {"space-owner": 2, "space-admin": 1, "space-member": 0},
			testSpaceB: {"space-b-admin": 1},
		}},
	}
}

func testProfileRouter(runtime *Runtime, uid, role string) *wkhttp.WKHttp {
	router := wkhttp.New()
	router.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.SourceLanguage)))
	setCaller := func(c *wkhttp.Context) {
		c.Set("uid", uid)
		if role != "" {
			c.Set("role", role)
		}
		c.Next()
	}
	group := router.Group("/v1/memory", setCaller)
	group.GET("/agents", runtime.listProfiles)
	group.GET("/agents/:agent_uid", runtime.getProfile)
	group.PUT("/agents/:agent_uid", runtime.updateProfile)
	group.PUT("/agents/:agent_uid/grants", runtime.replaceGrants)
	return router
}

func performProfileRequest(t *testing.T, router *wkhttp.WKHttp, method, target string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func decodeProfileResponse(t *testing.T, response *httptest.ResponseRecorder, target interface{}) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func TestGrantReplacementAPIAllowsRobotOwners(t *testing.T) {
	tests := []struct {
		name     string
		agentUID string
		uid      string
		role     string
		bank     MemoryBank
	}{
		{name: "platform scoped owner", agentUID: testAgentA, uid: testOwnerA, bank: MemoryBank{ID: "bank-platform-agent", BankType: "private", ScopeType: BankScopeAgent, ScopeID: testAgentA, Status: "active"}},
		{name: "Space scoped owner", agentUID: "agent-space-a", uid: "space-owner", bank: MemoryBank{ID: "bank-agent-a", BankType: "tentative", ScopeType: BankScopeAgent, ScopeID: "agent-space-a", Status: "active"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := testProfileRuntime()
			record := runtime.Catalog.(*fakeProfileCatalog).records[test.agentUID]
			record.PolicyVersionID = "policy-1"
			runtime.Catalog.(*fakeProfileCatalog).records[test.agentUID] = record
			store := &fakeGrantStore{}
			runtime.Grants = NewGrantService(&fakeBankCatalog{banks: map[string]MemoryBank{test.bank.ID: test.bank}}, store)
			runtime.Grants.newID = func() string { return "grant-1" }
			body, _ := json.Marshal(map[string]interface{}{
				"expected_version": record.Version,
				"audit_reason":     "security review",
				"grants": []map[string]interface{}{{
					"principal_type": "agent", "principal_id": test.agentUID, "bank_id": test.bank.ID,
					"capability": "recall", "effect": "allow",
				}},
			})
			response := performProfileRequest(t, testProfileRouter(runtime, test.uid, test.role), http.MethodPut, "/v1/memory/agents/"+test.agentUID+"/grants", body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if len(store.grants) != 1 || store.grants[0].IssuedByUID != test.uid || store.grants[0].Reason != "security review" {
				t.Fatalf("stored grants = %+v", store.grants)
			}
		})
	}
}

func TestGrantReplacementAPIDeniesMembersAndHiddenProfiles(t *testing.T) {
	runtime := testProfileRuntime()
	runtime.Grants = NewGrantService(&fakeBankCatalog{}, &fakeGrantStore{})
	body := []byte(`{"expected_version":1,"audit_reason":"review","grants":[]}`)

	member := performProfileRequest(t, testProfileRouter(runtime, "space-member", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", body)
	if member.Code != http.StatusForbidden {
		t.Fatalf("member status = %d, body = %s", member.Code, member.Body.String())
	}
	cross := performProfileRequest(t, testProfileRouter(runtime, "space-b-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", body)
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-space status = %d, body = %s", cross.Code, cross.Body.String())
	}
}

func TestGrantReplacementAPIRejectsCrossScopeAndVersionConflict(t *testing.T) {
	runtime := testProfileRuntime()
	record := runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"]
	record.PolicyVersionID = "policy-1"
	runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"] = record
	store := &fakeGrantStore{}
	runtime.Grants = NewGrantService(&fakeBankCatalog{banks: map[string]MemoryBank{
		"bank-agent-b": {ID: "bank-agent-b", ScopeType: BankScopeAgent, ScopeID: testAgentB, Status: "active"},
	}}, store)

	crossBody := []byte(`{"expected_version":1,"audit_reason":"review","grants":[{"principal_type":"agent","principal_id":"agent-space-a","bank_id":"bank-agent-b","capability":"recall","effect":"allow"}]}`)
	cross := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", crossBody)
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-scope status = %d, body = %s", cross.Code, cross.Body.String())
	}

	conflictBody := []byte(`{"expected_version":0,"audit_reason":"review","grants":[]}`)
	conflict := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", conflictBody)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("version conflict status = %d, body = %s", conflict.Code, conflict.Body.String())
	}
}

func TestGrantReplacementAPIRejectsClientOwnershipClaims(t *testing.T) {
	runtime := testProfileRuntime()
	record := runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"]
	record.PolicyVersionID = "policy-1"
	runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"] = record
	runtime.Grants = NewGrantService(&fakeBankCatalog{}, &fakeGrantStore{})
	body := []byte(`{"expected_version":1,"audit_reason":"review","owner_uid":"attacker","grants":[]}`)
	response := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("ownership claim status = %d, body = %s", response.Code, response.Body.String())
	}
}

type fakeBankManagementStore struct {
	banks           map[string]MemoryBank
	updateErr       error
	updated         *MemoryBank
	expectedVersion int64
}

func (store *fakeBankManagementStore) FindBankByID(bankID string) (*MemoryBank, error) {
	bank, ok := store.banks[bankID]
	if !ok {
		return nil, ErrBankNotFound
	}
	return &bank, nil
}

func (store *fakeBankManagementStore) ListBanks() ([]MemoryBank, error) {
	banks := make([]MemoryBank, 0, len(store.banks))
	for _, bank := range store.banks {
		banks = append(banks, bank)
	}
	return banks, nil
}

func (store *fakeBankManagementStore) UpdateBank(bank *MemoryBank, expectedVersion int64) error {
	if store.updateErr != nil {
		return store.updateErr
	}
	cloned := *bank
	store.updated = &cloned
	store.expectedVersion = expectedVersion
	store.banks[bank.ID] = cloned
	return nil
}

type fakeLogicalBankCreator struct {
	input LogicalBankCreate
	bank  *MemoryBank
	err   error
}

func (creator *fakeLogicalBankCreator) CreateLogicalBank(_ context.Context, input LogicalBankCreate) (*MemoryBank, error) {
	creator.input = input
	if creator.err != nil {
		return nil, creator.err
	}
	if creator.bank != nil {
		bank := *creator.bank
		bank.ID = input.ID
		return &bank, nil
	}
	return &MemoryBank{
		ID: input.ID, DisplayName: input.DisplayName, BankType: input.BankType, ScopeType: input.ScopeType,
		ScopeID: input.ScopeID, OwnerUID: input.OwnerUID, Sensitivity: input.Sensitivity,
		RetentionClass: input.RetentionClass, Status: "disabled", Version: 0,
	}, nil
}

type fakeOrganizationFacts struct {
	exists bool
}

type fakeSpaceOwnershipFacts struct {
	owners map[string]string
}

func (facts fakeSpaceOwnershipFacts) SpaceOwnerUID(_ context.Context, spaceID string) (string, error) {
	ownerUID, ok := facts.owners[spaceID]
	if !ok {
		return "", ErrManagementFactsUnavailable
	}
	return ownerUID, nil
}

func (facts fakeOrganizationFacts) OrganizationExists(context.Context, string) (bool, error) {
	return facts.exists, nil
}

func testBankRuntime() (*Runtime, *fakeBankManagementStore) {
	runtime := testProfileRuntime()
	store := &fakeBankManagementStore{banks: map[string]MemoryBank{
		"bank-space-a": {
			ID: "bank-space-a", HindsightBankID: "hs-secret-space-a", DisplayName: "Space A Verified",
			BankType: "verified", ScopeType: BankScopeSpace, ScopeID: testSpaceA, OwnerUID: "space-owner",
			Sensitivity: "internal", RetentionClass: "standard", Status: "active", Version: 2,
		},
		"bank-space-b": {
			ID: "bank-space-b", HindsightBankID: "hs-secret-space-b", DisplayName: "Space B Verified",
			BankType: "verified", ScopeType: BankScopeSpace, ScopeID: testSpaceB, OwnerUID: "space-b-owner",
			Sensitivity: "internal", RetentionClass: "standard", Status: "active", Version: 1,
		},
		"bank-agent-a": {
			ID: "bank-agent-a", HindsightBankID: "hs-secret-agent-a", DisplayName: "Agent A Private",
			BankType: "private", ScopeType: BankScopeAgent, ScopeID: "agent-space-a", OwnerUID: "space-owner",
			Sensitivity: "confidential", RetentionClass: "personal", Status: "active", Version: 1,
		},
	}}
	runtime.Banks = store
	runtime.SpaceFacts = fakeSpaceOwnershipFacts{owners: map[string]string{testSpaceA: "space-owner", testSpaceB: "space-b-owner"}}
	return runtime, store
}

func testBankRouter(runtime *Runtime, uid, role string) *wkhttp.WKHttp {
	router := wkhttp.New()
	router.SetErrorRenderer(i18n.NewErrorRenderer(i18n.NewLocalizer(i18n.SourceLanguage)))
	setCaller := func(c *wkhttp.Context) {
		c.Set("uid", uid)
		if role != "" {
			c.Set("role", role)
		}
		c.Next()
	}
	group := router.Group("/v1/memory", setCaller)
	group.GET("/banks", runtime.listBanks)
	group.POST("/banks", runtime.createBank)
	group.RouterGroup.PATCH("/banks/:bank_id", router.WKHttpHandler(runtime.updateBank))
	return router
}

func TestBankAPIListAllowsRobotOwnerWithoutSpaceAdminRole(t *testing.T) {
	runtime, _ := testBankRuntime()
	response := performProfileRequest(t, testBankRouter(runtime, "space-owner", ""), http.MethodGet, "/v1/memory/banks?space_id="+testSpaceA, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestBankAPIListFiltersAuthorizedBanksAndKeepsPhysicalIDsOpaque(t *testing.T) {
	runtime, _ := testBankRuntime()
	response := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), http.MethodGet, "/v1/memory/banks?space_id="+testSpaceA, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if bytes.Contains(response.Body.Bytes(), []byte("hindsight")) || bytes.Contains(response.Body.Bytes(), []byte("hs-secret")) {
		t.Fatalf("physical bank identifier leaked: %s", response.Body.String())
	}
	var body struct {
		Banks []bankResponse `json:"banks"`
	}
	decodeProfileResponse(t, response, &body)
	if len(body.Banks) != 2 {
		t.Fatalf("banks = %+v", body.Banks)
	}
	for _, bank := range body.Banks {
		if bank.ScopeID == testSpaceB {
			t.Fatalf("cross-space bank leaked: %+v", bank)
		}
	}
}

func TestBankAPICreateUsesOpaqueServerIDAndAuthoritativeAgentOwner(t *testing.T) {
	runtime, _ := testBankRuntime()
	creator := &fakeLogicalBankCreator{}
	runtime.BankCreator = creator
	body := []byte(`{"display_name":"Private Notes","bank_type":"private","scope_type":"agent","scope_id":"agent-space-a","sensitivity":"confidential","retention_class":"personal"}`)
	response := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), http.MethodPost, "/v1/memory/banks", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if creator.input.ID == "" || creator.input.ID == creator.input.ScopeID || creator.input.OwnerUID != "space-owner" {
		t.Fatalf("create input = %+v", creator.input)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("owner_uid")) || bytes.Contains(response.Body.Bytes(), []byte("hindsight")) {
		t.Fatalf("server-only field leaked: %s", response.Body.String())
	}
}

func TestBankAPICreateUsesAuthoritativeSpaceOwner(t *testing.T) {
	runtime, _ := testBankRuntime()
	creator := &fakeLogicalBankCreator{}
	runtime.BankCreator = creator
	body := []byte(`{"display_name":"Shared Notes","bank_type":"shared","scope_type":"space","scope_id":"space-agent-memory-a","sensitivity":"internal","retention_class":"standard"}`)
	response := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), http.MethodPost, "/v1/memory/banks", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if creator.input.OwnerUID != "space-owner" {
		t.Fatalf("owner_uid = %q, want authoritative Space owner", creator.input.OwnerUID)
	}
}

func TestBankAPICreateRejectsReservedArbitraryTypesAndScopeMismatch(t *testing.T) {
	tests := []string{
		`{"display_name":"Reserved","bank_type":"quarantine","scope_type":"space","scope_id":"space-a","sensitivity":"restricted","retention_class":"review"}`,
		`{"display_name":"Arbitrary","bank_type":"ares","scope_type":"space","scope_id":"space-a","sensitivity":"internal","retention_class":"standard"}`,
		`{"display_name":"Mismatch","bank_type":"private","scope_type":"space","scope_id":"space-a","sensitivity":"internal","retention_class":"standard"}`,
		`{"display_name":"Mismatch","bank_type":"shared","scope_type":"agent","scope_id":"agent-space-a","sensitivity":"internal","retention_class":"standard"}`,
	}
	for _, body := range tests {
		runtime, _ := testBankRuntime()
		runtime.BankCreator = &fakeLogicalBankCreator{}
		response := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), http.MethodPost, "/v1/memory/banks", []byte(body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, response = %s", body, response.Code, response.Body.String())
		}
	}
}

func TestBankAPICrossSpaceIDORAndMembersAreDenied(t *testing.T) {
	runtime, _ := testBankRuntime()
	body := []byte(`{"status":"disabled","expected_version":2}`)
	cross := performProfileRequest(t, testBankRouter(runtime, "space-b-admin", ""), http.MethodPatch, "/v1/memory/banks/bank-space-a", body)
	if cross.Code != http.StatusNotFound {
		t.Fatalf("cross-space status = %d, body = %s", cross.Code, cross.Body.String())
	}
	member := performProfileRequest(t, testBankRouter(runtime, "space-member", ""), http.MethodPatch, "/v1/memory/banks/bank-space-a", body)
	if member.Code != http.StatusForbidden {
		t.Fatalf("member status = %d, body = %s", member.Code, member.Body.String())
	}
}

func TestBankAPIUpdateLifecycleAndOptimisticConcurrency(t *testing.T) {
	runtime, store := testBankRuntime()
	response := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), http.MethodPatch, "/v1/memory/banks/bank-space-a", []byte(`{"status":"disabled","expected_version":2}`))
	if response.Code != http.StatusOK || store.updated == nil || store.updated.Status != "disabled" || store.updated.DisabledAt == nil || store.expectedVersion != 2 {
		t.Fatalf("status = %d, updated = %+v, body = %s", response.Code, store.updated, response.Body.String())
	}

	invalidRuntime, _ := testBankRuntime()
	invalid := performProfileRequest(t, testBankRouter(invalidRuntime, "space-admin", ""), http.MethodPatch, "/v1/memory/banks/bank-space-a", []byte(`{"status":"archived","expected_version":2}`))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid transition status = %d, body = %s", invalid.Code, invalid.Body.String())
	}

	staleRuntime, _ := testBankRuntime()
	stale := performProfileRequest(t, testBankRouter(staleRuntime, "space-admin", ""), http.MethodPatch, "/v1/memory/banks/bank-space-a", []byte(`{"display_name":"Renamed","expected_version":1}`))
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status = %d, body = %s", stale.Code, stale.Body.String())
	}
}

func TestBankAPIRejectsMalformedUnknownAndPhysicalFields(t *testing.T) {
	tests := []struct {
		method string
		target string
		body   string
	}{
		{http.MethodPost, "/v1/memory/banks", `{"display_name":`},
		{http.MethodPost, "/v1/memory/banks", `{"display_name":"Name","bank_type":"verified","scope_type":"space","scope_id":"space-a","sensitivity":"internal","retention_class":"standard","unknown":true}`},
		{http.MethodPost, "/v1/memory/banks", `{"display_name":"Name","bank_type":"verified","scope_type":"space","scope_id":"space-a","sensitivity":"internal","retention_class":"standard","hindsight_bank_id":"raw"}`},
		{http.MethodPatch, "/v1/memory/banks/bank-space-a", `{"expected_version":2,"bank_type":"shared"}`},
	}
	for _, test := range tests {
		runtime, _ := testBankRuntime()
		runtime.BankCreator = &fakeLogicalBankCreator{}
		response := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), test.method, test.target, []byte(test.body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s %s status = %d, body = %s", test.method, test.body, response.Code, response.Body.String())
		}
	}
}

func TestBankAPIOrganizationCreateRequiresPlatformAdminAndAuthoritativeFact(t *testing.T) {
	runtime, _ := testBankRuntime()
	creator := &fakeLogicalBankCreator{}
	runtime.BankCreator = creator
	runtime.OrganizationFacts = fakeOrganizationFacts{exists: true}
	body := []byte(`{"display_name":"Org Verified","bank_type":"verified","scope_type":"organization","scope_id":"org-a","sensitivity":"internal","retention_class":"standard"}`)
	response := performProfileRequest(t, testBankRouter(runtime, "platform-admin", "admin"), http.MethodPost, "/v1/memory/banks", body)
	if response.Code != http.StatusCreated || creator.input.ScopeID != "org-a" {
		t.Fatalf("status = %d, input = %+v, body = %s", response.Code, creator.input, response.Body.String())
	}

	runtime, _ = testBankRuntime()
	runtime.BankCreator = &fakeLogicalBankCreator{}
	runtime.OrganizationFacts = fakeOrganizationFacts{exists: false}
	missing := performProfileRequest(t, testBankRouter(runtime, "platform-admin", "admin"), http.MethodPost, "/v1/memory/banks", body)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown organization status = %d, body = %s", missing.Code, missing.Body.String())
	}
}

func TestManagementAccessAdapterLoadsAuthoritativeSpaceOwner(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	mock.ExpectQuery("SELECT creator FROM space WHERE space_id='space-agent-memory-a'").
		WillReturnRows(sqlmock.NewRows([]string{"creator"}).AddRow("space-owner"))
	adapter := NewManagementAccessAdapter((&dbr.Connection{DB: rawDB, Dialect: dialect.MySQL, EventReceiver: &dbr.NullEventReceiver{}}).NewSession(nil))
	ownerUID, err := adapter.SpaceOwnerUID(context.Background(), testSpaceA)
	if err != nil || ownerUID != "space-owner" {
		t.Fatalf("owner_uid = %q, err = %v", ownerUID, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
