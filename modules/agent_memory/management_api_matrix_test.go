package agent_memory

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
)

var managementOperationCoverage = []struct {
	method  string
	path    string
	success string
	error   string
}{
	{http.MethodGet, "/memory/agents", "platform or Space-scoped list", "unauthorized requested scope"},
	{http.MethodGet, "/memory/agents/{agent_uid}", "visible profile", "hidden cross-Space profile"},
	{http.MethodPut, "/memory/agents/{agent_uid}", "authorized update", "member, malformed, or stale update"},
	{http.MethodGet, "/memory/banks", "authorized logical Bank list", "unauthorized requested scope"},
	{http.MethodPost, "/memory/banks", "authorized logical Bank create", "invalid type, scope, or ownership"},
	{http.MethodPatch, "/memory/banks/{bank_id}", "authorized lifecycle update", "hidden, malformed, or stale update"},
	{http.MethodPost, "/memory/agents/{agent_uid}/banks", "owner Agent Bank create", "non-owner or invalid Bank type"},
	{http.MethodPost, "/memory/spaces/{space_id}/banks", "Space admin Bank create", "non-admin or invalid Bank type"},
	{http.MethodGet, "/memory/banks/{bank_id}/offers", "Space admin offer list", "hidden or non-admin Bank"},
	{http.MethodPost, "/memory/banks/{bank_id}/offers", "same-Space per-Robot offer", "cross-Space or inactive Robot"},
	{http.MethodPatch, "/memory/offers/{offer_id}", "optimistic offer revision", "stale or unauthorized revision"},
	{http.MethodGet, "/memory/agents/{agent_uid}/offers", "owner offer list", "non-owner Robot"},
	{http.MethodPut, "/memory/offers/{offer_id}/decision", "exact-revision owner decision", "forged owner or stale revision"},
	{http.MethodPost, "/memory/acceptances/{acceptance_id}/withdraw", "owner acceptance withdrawal", "forged owner or stale acceptance"},
	{http.MethodGet, "/memory/agents/{agent_uid}/effective-permissions", "server-derived permission explanation", "hidden Robot"},
	{http.MethodPost, "/memory/agents/{agent_uid}/enablement-attempts", "version-bound activation start", "non-owner or ineligible Robot"},
	{http.MethodGet, "/memory/enablement-attempts/{attempt_id}", "owner-visible attempt", "hidden attempt"},
	{http.MethodPost, "/memory/enablement-attempts/{attempt_id}/probe", "non-mutating readiness probe", "stale snapshot or hidden attempt"},
	{http.MethodPost, "/memory/enablement-attempts/{attempt_id}/commit", "fresh ready attempt commit", "expired or stale attempt"},
	{http.MethodPost, "/memory/enablement-attempts/{attempt_id}/cancel", "owner cancellation", "terminal or stale attempt"},
	{http.MethodGet, "/memory/governance/runs", "authorized scoped run list", "unauthorized or missing scope"},
	{http.MethodPost, "/memory/governance/runs", "authorized dry-run or apply", "unauthorized scope, malformed, or conflict"},
	{http.MethodGet, "/memory/governance/runs/{run_id}", "authorized run detail", "hidden or unknown run"},
	{http.MethodPost, "/memory/governance/runs/{run_id}/rollback", "current-scope reauthorized rollback", "scope drift or stale item"},
}

func TestManagementOpenAPIOperationCoverageGuard(t *testing.T) {
	seen := make(map[string]struct{}, len(managementOperationCoverage))
	for _, operation := range managementOperationCoverage {
		key := operation.method + " " + operation.path
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate management operation %s", key)
		}
		seen[key] = struct{}{}
		if operation.success == "" || operation.error == "" {
			t.Fatalf("operation %s must name success and denial/error coverage", key)
		}
	}
	if len(seen) != 24 {
		t.Fatalf("management operation count = %d, want 24", len(seen))
	}
	source, err := os.ReadFile("1module.go")
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]struct{})
	routePattern := regexp.MustCompile(`memory(?:\.RouterGroup)?\.(GET|POST|PUT|PATCH)\("([^"]+)"`)
	parameterPattern := regexp.MustCompile(`:([a-z_]+)`)
	for _, match := range routePattern.FindAllStringSubmatch(string(source), -1) {
		path := "/memory" + parameterPattern.ReplaceAllString(match[2], `{$1}`)
		registered[match[1]+" "+path] = struct{}{}
	}
	for key := range seen {
		if _, ok := registered[strings.TrimSpace(key)]; !ok {
			t.Fatalf("covered OpenAPI operation is not registered: %s", key)
		}
	}
	for key := range registered {
		if _, ok := seen[key]; !ok {
			t.Fatalf("registered management operation lacks matrix coverage: %s", key)
		}
	}
}

func TestProfileAPIRejectsMalformedUnknownAndServerOwnedFields(t *testing.T) {
	for _, body := range []string{
		`{"enabled":`,
		`{"enabled":true,"expected_version":1,"unknown":true}`,
		`{"enabled":true,"expected_version":1,"owner_uid":"attacker"}`,
		`{"enabled":true,"expected_version":1,"robot_id":"spoofed"}`,
		`{"enabled":true,"expected_version":1,"scope_type":"platform"}`,
		`{"enabled":true,"expected_version":1,"trust_tier":"low"}`,
		`{"enabled":true,"expected_version":1,"credential_epoch":999}`,
	} {
		runtime := testProfileRuntime()
		response := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a", []byte(body))
		assertManagementError(t, response, http.StatusBadRequest, errcode.ErrSharedParamInvalid.ID)
	}
}

func TestGrantReplacementAPIRoleMatrixAndHiddenAntiEnumeration(t *testing.T) {
	body := []byte(`{"expected_version":1,"audit_reason":"review","grants":[]}`)
	tests := []struct {
		name     string
		uid      string
		role     string
		agentUID string
		want     int
		wantCode string
	}{
		{name: "platform admin", uid: "platform-admin", role: "admin", agentUID: testAgentA, want: http.StatusOK},
		{name: "Space owner", uid: "space-owner", agentUID: "agent-space-a", want: http.StatusOK},
		{name: "Space admin", uid: "space-admin", agentUID: "agent-space-a", want: http.StatusOK},
		{name: "ordinary member", uid: "space-member", agentUID: "agent-space-a", want: http.StatusForbidden, wantCode: errcode.ErrSharedForbidden.ID},
		{name: "non-member", uid: "outsider", agentUID: "agent-space-a", want: http.StatusNotFound, wantCode: errcode.ErrSharedNotFound.ID},
		{name: "cross-Space admin", uid: "space-b-admin", agentUID: "agent-space-a", want: http.StatusNotFound, wantCode: errcode.ErrSharedNotFound.ID},
		{name: "unknown Agent", uid: "platform-admin", role: "admin", agentUID: "agent-missing", want: http.StatusNotFound, wantCode: errcode.ErrSharedNotFound.ID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := testProfileRuntime()
			if record, ok := runtime.Catalog.(*fakeProfileCatalog).records[test.agentUID]; ok {
				record.PolicyVersionID = "policy-1"
				runtime.Catalog.(*fakeProfileCatalog).records[test.agentUID] = record
			}
			runtime.Grants = NewGrantService(&fakeBankCatalog{}, &fakeGrantStore{})
			response := performProfileRequest(t, testProfileRouter(runtime, test.uid, test.role), http.MethodPut, "/v1/memory/agents/"+test.agentUID+"/grants", body)
			if test.want == http.StatusOK {
				if response.Code != test.want {
					t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
				}
				assertManagementResponseRedacted(t, response, "hindsight_bank_id", "owner_uid", "policy_version_id")
				return
			}
			assertManagementError(t, response, test.want, test.wantCode)
		})
	}
}

func TestGrantReplacementAPIRejectsMalformedUnknownAndCredentialFields(t *testing.T) {
	for _, body := range []string{
		`{"expected_version":1,"audit_reason":"review","grants":`,
		`{"expected_version":1,"audit_reason":"review","grants":[],"unknown":true}`,
		`{"expected_version":1,"audit_reason":"review","grants":[],"bearer_token":"secret"}`,
		`{"expected_version":1,"audit_reason":"review","grants":[{"bank_id":"bank-space-a","capability":"recall","effect":"allow","owner_uid":"attacker"}]}`,
	} {
		runtime := testProfileRuntime()
		record := runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"]
		record.PolicyVersionID = "policy-1"
		runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"] = record
		runtime.Grants = NewGrantService(&fakeBankCatalog{}, &fakeGrantStore{})
		response := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", []byte(body))
		assertManagementError(t, response, http.StatusBadRequest, errcode.ErrSharedParamInvalid.ID)
	}
}

func TestGrantReplacementAPIRejectsInactiveBanksAndPreservesDenyPrecedence(t *testing.T) {
	for _, status := range []string{BankStatusDisabled, BankStatusProvisioning, "archived"} {
		t.Run(status, func(t *testing.T) {
			runtime := testProfileRuntime()
			record := runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"]
			record.PolicyVersionID = "policy-1"
			runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"] = record
			runtime.Grants = NewGrantService(&fakeBankCatalog{banks: map[string]MemoryBank{
				"bank-inactive": {ID: "bank-inactive", ScopeType: BankScopeSpace, ScopeID: testSpaceA, Status: status},
			}}, &fakeGrantStore{})
			body := []byte(`{"expected_version":1,"audit_reason":"review","grants":[{"principal_type":"agent","principal_id":"agent-space-a","bank_id":"bank-inactive","capability":"recall","effect":"allow"}]}`)
			response := performProfileRequest(t, testProfileRouter(runtime, "space-admin", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", body)
			assertManagementError(t, response, http.StatusForbidden, errcode.ErrSharedForbidden.ID)
		})
	}

	runtime := testProfileRuntime()
	record := runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"]
	record.PolicyVersionID = "policy-1"
	runtime.Catalog.(*fakeProfileCatalog).records["agent-space-a"] = record
	bank := MemoryBank{ID: "bank-agent-a", BankType: "private", ScopeType: BankScopeAgent, ScopeID: "agent-space-a", Status: BankStatusActive}
	store := &fakeGrantStore{}
	runtime.Grants = NewGrantService(&fakeBankCatalog{banks: map[string]MemoryBank{bank.ID: bank}}, store)
	body := []byte(`{"expected_version":1,"audit_reason":"containment","grants":[{"principal_type":"agent","principal_id":"agent-space-a","bank_id":"bank-agent-a","capability":"recall","effect":"allow"},{"principal_type":"agent","principal_id":"agent-space-a","bank_id":"bank-agent-a","capability":"recall","effect":"deny"}]}`)
	response := performProfileRequest(t, testProfileRouter(runtime, "space-owner", ""), http.MethodPut, "/v1/memory/agents/agent-space-a/grants", body)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	decision := ResolveCapability(CapabilityRequest{
		Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: "agent-space-a"},
		Profile:   record.AgentMemoryProfile, Bank: bank, Capability: CapabilityRecall, Grants: store.grants, Now: time.Now().UTC(),
	})
	if decision.Allowed || decision.ReasonCode != PolicyReasonForbidden {
		t.Fatalf("deny precedence decision = %+v", decision)
	}
}

func TestBankAPIManagementRoleMatrix(t *testing.T) {
	tests := []struct {
		name     string
		uid      string
		role     string
		method   string
		target   string
		body     string
		want     int
		wantCode string
	}{
		{name: "platform admin list", uid: "platform-admin", role: "admin", method: http.MethodGet, target: "/v1/memory/banks", want: http.StatusOK},
		{name: "Space owner list", uid: "space-owner", method: http.MethodGet, target: "/v1/memory/banks?space_id=" + testSpaceA, want: http.StatusOK},
		{name: "Space admin list", uid: "space-admin", method: http.MethodGet, target: "/v1/memory/banks?space_id=" + testSpaceA, want: http.StatusOK},
		{name: "ordinary member list", uid: "space-member", method: http.MethodGet, target: "/v1/memory/banks?space_id=" + testSpaceA, want: http.StatusForbidden, wantCode: errcode.ErrSharedForbidden.ID},
		{name: "non-member list", uid: "outsider", method: http.MethodGet, target: "/v1/memory/banks?space_id=" + testSpaceA, want: http.StatusForbidden, wantCode: errcode.ErrSharedForbidden.ID},
		{name: "cross-Space list", uid: "space-b-admin", method: http.MethodGet, target: "/v1/memory/banks?space_id=" + testSpaceA, want: http.StatusForbidden, wantCode: errcode.ErrSharedForbidden.ID},
		{name: "owner create", uid: "space-owner", method: http.MethodPost, target: "/v1/memory/banks", body: `{"display_name":"Shared","bank_type":"shared","scope_type":"space","scope_id":"` + testSpaceA + `","sensitivity":"internal","retention_class":"standard"}`, want: http.StatusCreated},
		{name: "member create", uid: "space-member", method: http.MethodPost, target: "/v1/memory/banks", body: `{"display_name":"Shared","bank_type":"shared","scope_type":"space","scope_id":"` + testSpaceA + `","sensitivity":"internal","retention_class":"standard"}`, want: http.StatusForbidden, wantCode: errcode.ErrSharedForbidden.ID},
		{name: "non-member create hidden", uid: "outsider", method: http.MethodPost, target: "/v1/memory/banks", body: `{"display_name":"Shared","bank_type":"shared","scope_type":"space","scope_id":"` + testSpaceA + `","sensitivity":"internal","retention_class":"standard"}`, want: http.StatusNotFound, wantCode: errcode.ErrSharedNotFound.ID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, _ := testBankRuntime()
			runtime.BankCreator = &fakeLogicalBankCreator{}
			response := performProfileRequest(t, testBankRouter(runtime, test.uid, test.role), test.method, test.target, []byte(test.body))
			if test.want < http.StatusBadRequest {
				if response.Code != test.want {
					t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
				}
				assertManagementResponseRedacted(t, response, "hindsight_bank_id", "owner_uid", "policy_version_id")
				return
			}
			assertManagementError(t, response, test.want, test.wantCode)
		})
	}
}

func TestBankAPICrossAgentIDORAndInactiveLifecycle(t *testing.T) {
	runtime, store := testBankRuntime()
	store.banks["bank-agent-b"] = MemoryBank{ID: "bank-agent-b", ScopeType: BankScopeAgent, ScopeID: "agent-space-b", Status: BankStatusActive, Version: 1}
	cross := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), http.MethodPatch, "/v1/memory/banks/bank-agent-b", []byte(`{"status":"disabled","expected_version":1}`))
	assertManagementError(t, cross, http.StatusNotFound, errcode.ErrSharedNotFound.ID)

	store.banks["bank-provisioning"] = MemoryBank{ID: "bank-provisioning", ScopeType: BankScopeSpace, ScopeID: testSpaceA, Status: BankStatusProvisioning, Version: 0}
	disabled := performProfileRequest(t, testBankRouter(runtime, "space-owner", ""), http.MethodPatch, "/v1/memory/banks/bank-provisioning", []byte(`{"status":"disabled","expected_version":0}`))
	if disabled.Code != http.StatusOK || store.updated == nil || store.updated.Status != BankStatusDisabled {
		t.Fatalf("provisioning disable status = %d, updated = %+v, body = %s", disabled.Code, store.updated, disabled.Body.String())
	}
}

func TestManagementResponsesExcludeSecretsAndInternalFields(t *testing.T) {
	profileRuntime := testProfileRuntime()
	profile := performProfileRequest(t, testProfileRouter(profileRuntime, "platform-admin", "admin"), http.MethodGet, "/v1/memory/agents/"+testAgentA, nil)
	if profile.Code != http.StatusOK {
		t.Fatalf("profile status = %d, body = %s", profile.Code, profile.Body.String())
	}
	assertManagementResponseRedacted(t, profile, "hindsight_bank_id", "bearer", "token", "raw_credentials", "credentials", "content", "policy_version_id", "credential_epoch")

	bankRuntime, _ := testBankRuntime()
	banks := performProfileRequest(t, testBankRouter(bankRuntime, "platform-admin", "admin"), http.MethodGet, "/v1/memory/banks", nil)
	if banks.Code != http.StatusOK {
		t.Fatalf("banks status = %d, body = %s", banks.Code, banks.Body.String())
	}
	assertManagementResponseRedacted(t, banks, "hindsight_bank_id", "bearer", "token", "raw_credentials", "credentials", "content", "owner_uid", "policy_version_id")
}

func TestBankAPIUsesProductionBankServiceForCreate(t *testing.T) {
	service, mock := newTestBankService(t)
	expectMissingIntent(mock)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM memory_policy_version.*active").WillReturnRows(sqlmockRows("id", "policy-v1"))
	mock.ExpectExec("INSERT INTO `memory_bank`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `memory_bank_provisioning_intent`").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	runtime := testProfileRuntime()
	runtime.Banks = service
	runtime.BankCreator = service
	body := []byte(`{"display_name":"Private","bank_type":"private","scope_type":"agent","scope_id":"` + testAgentA + `","sensitivity":"restricted","retention_class":"caller-selected"}`)
	response := performProfileRequest(t, testBankRouter(runtime, "platform-admin", "admin"), http.MethodPost, "/v1/memory/banks", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var bank bankResponse
	decodeProfileResponse(t, response, &bank)
	if bank.Status != BankStatusProvisioning || bank.Sensitivity != "confidential" || bank.RetentionClass != RetentionClassPersonal {
		t.Fatalf("created bank = %+v", bank)
	}
	assertManagementResponseRedacted(t, response, "hindsight_bank_id", "owner_uid", "policy_version_id")
	assertSQLExpectations(t, mock)
}

func TestBankAPIUsesProductionBankServiceForDisable(t *testing.T) {
	service, mock := newTestBankService(t)
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT \\* FROM memory_bank.*bank-agent-a").WillReturnRows(bankRows().AddRow("bank-agent-a", "hs-secret", "Private", "private", BankScopeAgent, "agent-space-a", "space-owner", "confidential", RetentionClassPersonal, "policy-v1", BankStatusActive, false, 2, now, now, nil))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM memory_bank.*bank-agent-a.*FOR UPDATE").WillReturnRows(bankRows().AddRow("bank-agent-a", "hs-secret", "Private", "private", BankScopeAgent, "agent-space-a", "space-owner", "confidential", RetentionClassPersonal, "policy-v1", BankStatusActive, false, 2, now, now, nil))
	mock.ExpectExec("UPDATE `memory_bank` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `memory_bank_provisioning_intent`").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	runtime := testProfileRuntime()
	runtime.Banks = service
	response := performProfileRequest(t, testBankRouter(runtime, "space-admin", ""), http.MethodPatch, "/v1/memory/banks/bank-agent-a", []byte(`{"status":"disabled","expected_version":2}`))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var bank bankResponse
	decodeProfileResponse(t, response, &bank)
	if bank.Status != BankStatusDisabled || bank.Version != 3 {
		t.Fatalf("disabled bank = %+v", bank)
	}
	assertManagementResponseRedacted(t, response, "hindsight_bank_id", "owner_uid", "policy_version_id")
	assertSQLExpectations(t, mock)
}

func assertManagementError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, status, response.Body.String())
	}
	var body struct {
		Status int `json:"status"`
		Error  struct {
			Code       string `json:"code"`
			HTTPStatus int    `json:"http_status"`
		} `json:"error"`
	}
	decodeProfileResponse(t, response, &body)
	if body.Status != status || body.Error.HTTPStatus != status || body.Error.Code != code {
		t.Fatalf("error envelope = %+v, want status %d code %q", body, status, code)
	}
}

func assertManagementResponseRedacted(t *testing.T, response *httptest.ResponseRecorder, forbidden ...string) {
	t.Helper()
	var value interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	lower := bytes.ToLower(encoded)
	for _, field := range forbidden {
		if bytes.Contains(lower, []byte(field)) {
			t.Fatalf("forbidden management field %q leaked: %s", field, response.Body.String())
		}
	}
}

func sqlmockRows(column string, value interface{}) *sqlmock.Rows {
	return sqlmock.NewRows([]string{column}).AddRow(value)
}
