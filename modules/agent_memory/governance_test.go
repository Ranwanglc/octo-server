package agent_memory

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
)

// fakeGovernanceStore records runs, proposals, and apply intent without a database so
// dry-run vs apply behavior and idempotency can be asserted deterministically.
type fakeGovernanceStore struct {
	banks       []MemoryBank
	items       []KnowledgeItem
	runsByID    map[string]GovernanceRun
	runsByKey   map[string]GovernanceRun
	transitions map[string][]KnowledgeTransition
	persisted   []GovernanceProposal
	appliedFlag bool
	persistErr  error
}

func newFakeGovernanceStore() *fakeGovernanceStore {
	return &fakeGovernanceStore{
		runsByID:    map[string]GovernanceRun{},
		runsByKey:   map[string]GovernanceRun{},
		transitions: map[string][]KnowledgeTransition{},
	}
}

func (store *fakeGovernanceStore) FindGovernanceRun(runID string) (*GovernanceRun, error) {
	run, ok := store.runsByID[runID]
	if !ok {
		return nil, ErrGovernanceRunNotFound
	}
	return &run, nil
}

func (store *fakeGovernanceStore) FindGovernanceRunByIdempotencyKey(key string) (*GovernanceRun, error) {
	run, ok := store.runsByKey[key]
	if !ok {
		return nil, ErrGovernanceRunNotFound
	}
	return &run, nil
}

func (store *fakeGovernanceStore) ListGovernanceRuns(scopeType, scopeID string, _ int) ([]GovernanceRun, error) {
	var runs []GovernanceRun
	for _, run := range store.runsByID {
		if run.ScopeType == scopeType && run.ScopeID == scopeID {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

func (store *fakeGovernanceStore) ListGovernanceRunTransitions(runID string) ([]KnowledgeTransition, error) {
	return store.transitions[runID], nil
}

func (store *fakeGovernanceStore) ListBanks() ([]MemoryBank, error) {
	return store.banks, nil
}

func (store *fakeGovernanceStore) ListGovernableKnowledgeItems(bankIDs []string, _ int) ([]KnowledgeItem, error) {
	allowed := map[string]struct{}{}
	for _, id := range bankIDs {
		allowed[id] = struct{}{}
	}
	var out []KnowledgeItem
	for _, item := range store.items {
		if _, ok := allowed[item.BankID]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

func (store *fakeGovernanceStore) PersistGovernanceRun(run *GovernanceRun, proposals []GovernanceProposal, apply bool) error {
	if store.persistErr != nil {
		return store.persistErr
	}
	store.persisted = proposals
	store.appliedFlag = apply
	store.runsByID[run.ID] = *run
	store.runsByKey[run.IdempotencyKey] = *run
	transitions := make([]KnowledgeTransition, 0, len(proposals))
	for _, proposal := range proposals {
		transitions = append(transitions, proposal.Transition)
	}
	store.transitions[run.ID] = transitions
	return nil
}

type fixedPolicyResolver struct {
	id  string
	err error
}

func (resolver fixedPolicyResolver) ActivePolicyVersionID(context.Context) (string, error) {
	return resolver.id, resolver.err
}

func governanceService(store *fakeGovernanceStore, policyID string) *GovernanceService {
	service := NewGovernanceService(store, fixedPolicyResolver{id: policyID})
	counter := 0
	service.newID = func() string {
		counter++
		return "gen-" + string(rune('a'+counter-1))
	}
	service.now = func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) }
	return service
}

func TestGovernanceDryRunRecordsProposalsWithoutMutatingItems(t *testing.T) {
	store := newFakeGovernanceStore()
	store.banks = []MemoryBank{{ID: "bank-space-a", ScopeType: BankScopeSpace, ScopeID: testSpaceA, Status: BankStatusActive, PolicyVersionID: "policy-1"}}
	past := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	store.items = []KnowledgeItem{
		{ID: "item-expired", BankID: "bank-space-a", State: "verified", ValidUntil: &past, Version: 3},
		{ID: "item-fresh", BankID: "bank-space-a", State: "verified", Version: 1},
	}
	service := governanceService(store, "policy-1")

	run, err := service.Create(context.Background(), GovernanceRunRequest{
		Mode: GovernanceModeDryRun, ScopeType: GovernanceScopeSpace, ScopeID: testSpaceA,
		IdempotencyKey: "gov-dry-1", ActorUID: "space-owner",
	})
	if err != nil {
		t.Fatalf("dry-run create error = %v", err)
	}
	if run.Mode != GovernanceModeDryRun || run.ProposedCount != 1 || run.AppliedCount != 0 {
		t.Fatalf("dry-run run = %+v", run)
	}
	if store.appliedFlag {
		t.Fatal("dry-run must not request apply mutation")
	}
	if len(store.persisted) != 1 || store.persisted[0].Transition.KnowledgeItemID != "item-expired" || store.persisted[0].Transition.ToState != "stale" {
		t.Fatalf("dry-run proposals = %+v", store.persisted)
	}
	if store.persisted[0].Transition.ActorType != "governance_job" {
		t.Fatalf("proposal actor type = %q", store.persisted[0].Transition.ActorType)
	}
}

func TestGovernanceApplyAdvancesItemsUnderExpectedVersion(t *testing.T) {
	store := newFakeGovernanceStore()
	store.banks = []MemoryBank{{ID: "bank-space-a", ScopeType: BankScopeSpace, ScopeID: testSpaceA, Status: BankStatusActive, PolicyVersionID: "policy-1"}}
	past := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	store.items = []KnowledgeItem{{ID: "item-expired", BankID: "bank-space-a", State: "corroborated", ValidUntil: &past, Version: 5}}
	service := governanceService(store, "policy-1")

	run, err := service.Create(context.Background(), GovernanceRunRequest{
		Mode: GovernanceModeApply, ScopeType: GovernanceScopeSpace, ScopeID: testSpaceA,
		IdempotencyKey: "gov-apply-1", ActorUID: "space-owner",
	})
	if err != nil {
		t.Fatalf("apply create error = %v", err)
	}
	if !store.appliedFlag {
		t.Fatal("apply mode must request item mutation")
	}
	if run.AppliedCount != 1 || run.ProposedCount != 1 {
		t.Fatalf("apply run = %+v", run)
	}
	if store.persisted[0].ExpectedVersion != 5 || store.persisted[0].Transition.FromState != "corroborated" {
		t.Fatalf("apply proposal = %+v", store.persisted[0])
	}
}

func TestGovernanceNeverProposesPhysicalDeletion(t *testing.T) {
	past := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	items := []KnowledgeItem{
		{ID: "expired-verified", State: "verified", ValidUntil: &past},
		{ID: "expired-stale", State: "stale", ValidUntil: &past},
		{ID: "fresh", State: "tentative"},
	}
	for _, item := range items {
		toState, _, ok := proposeSoftTransition(item, now)
		if ok && (toState == "deleted" || toState == "revoked") {
			t.Fatalf("governance proposed destructive transition to %q for %s", toState, item.ID)
		}
	}
}

func TestGovernanceCreateIsIdempotent(t *testing.T) {
	store := newFakeGovernanceStore()
	store.banks = []MemoryBank{{ID: "bank-space-a", ScopeType: BankScopeSpace, ScopeID: testSpaceA, Status: BankStatusActive, PolicyVersionID: "policy-1"}}
	service := governanceService(store, "policy-1")
	request := GovernanceRunRequest{Mode: GovernanceModeDryRun, ScopeType: GovernanceScopeSpace, ScopeID: testSpaceA, IdempotencyKey: "gov-idem-1", ActorUID: "space-owner"}

	first, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("first create error = %v", err)
	}
	second, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("second create error = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent create returned distinct runs %q and %q", first.ID, second.ID)
	}
}

func TestGovernanceCreateRejectsIdempotencyKeyReuseAcrossScope(t *testing.T) {
	store := newFakeGovernanceStore()
	store.banks = []MemoryBank{{ID: "bank-space-a", ScopeType: BankScopeSpace, ScopeID: testSpaceA, Status: BankStatusActive, PolicyVersionID: "policy-1"}}
	service := governanceService(store, "policy-1")
	if _, err := service.Create(context.Background(), GovernanceRunRequest{Mode: GovernanceModeDryRun, ScopeType: GovernanceScopeSpace, ScopeID: testSpaceA, IdempotencyKey: "gov-reuse", ActorUID: "space-owner"}); err != nil {
		t.Fatalf("seed create error = %v", err)
	}
	_, err := service.Create(context.Background(), GovernanceRunRequest{Mode: GovernanceModeApply, ScopeType: GovernanceScopeSpace, ScopeID: testSpaceB, IdempotencyKey: "gov-reuse", ActorUID: "space-owner"})
	if err != ErrGovernanceConflict {
		t.Fatalf("scope reuse error = %v, want ErrGovernanceConflict", err)
	}
}

func testGovernanceRuntime(store *fakeGovernanceStore) *Runtime {
	runtime := testProfileRuntime()
	service := governanceService(store, "policy-1")
	runtime.Governance = service
	runtime.Audits = NewAuditService(&recordingAuditStore{})
	return runtime
}

type recordingAuditStore struct {
	events []MemoryAuditEvent
}

func (store *recordingAuditStore) AppendAuditEvent(event MemoryAuditEvent) error {
	store.events = append(store.events, event)
	return nil
}

func testGovernanceRouter(runtime *Runtime, uid, role string) *wkhttp.WKHttp {
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
	group.GET("/governance/runs", runtime.listGovernanceRuns)
	group.POST("/governance/runs", runtime.createGovernanceRun)
	group.GET("/governance/runs/:run_id", runtime.getGovernanceRun)
	return router
}

func TestGovernanceRunAPIRoleMatrixAndAntiEnumeration(t *testing.T) {
	body := `{"mode":"dry_run","scope_type":"space","scope_id":"` + testSpaceA + `","idempotency_key":"gov-role-1"}`
	tests := []struct {
		name     string
		uid      string
		role     string
		want     int
		wantCode string
	}{
		{name: "platform admin", uid: "platform-admin", role: "admin", want: http.StatusAccepted},
		{name: "space owner", uid: "space-owner", want: http.StatusAccepted},
		{name: "space admin", uid: "space-admin", want: http.StatusAccepted},
		{name: "ordinary member", uid: "space-member", want: http.StatusForbidden, wantCode: errcode.ErrSharedForbidden.ID},
		{name: "non-member hidden", uid: "outsider", want: http.StatusNotFound, wantCode: errcode.ErrSharedNotFound.ID},
		{name: "cross-space hidden", uid: "space-b-admin", want: http.StatusNotFound, wantCode: errcode.ErrSharedNotFound.ID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newFakeGovernanceStore()
			runtime := testGovernanceRuntime(store)
			response := performProfileRequest(t, testGovernanceRouter(runtime, test.uid, test.role), http.MethodPost, "/v1/memory/governance/runs", []byte(body))
			if test.want == http.StatusAccepted {
				if response.Code != test.want {
					t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
				}
				assertManagementResponseRedacted(t, response, "hindsight_bank_id", "policy_version_id", "created_by_uid", "idempotency_key")
				return
			}
			assertManagementError(t, response, test.want, test.wantCode)
		})
	}
}

func TestGovernanceRunAPIRejectsMalformedUnknownAndInvalidMode(t *testing.T) {
	for _, body := range []string{
		`{"mode":"dry_run","scope_type":"space","scope_id":"` + testSpaceA + `"}`,
		`{"mode":"purge","scope_type":"space","scope_id":"` + testSpaceA + `","idempotency_key":"k"}`,
		`{"mode":"dry_run","scope_type":"galaxy","scope_id":"x","idempotency_key":"k"}`,
		`{"mode":"dry_run","scope_type":"space","scope_id":"","idempotency_key":"k"}`,
		`{"mode":"dry_run","scope_type":"space","scope_id":"` + testSpaceA + `","idempotency_key":"k","unknown":true}`,
		`{"mode":"dry_run",`,
	} {
		store := newFakeGovernanceStore()
		runtime := testGovernanceRuntime(store)
		response := performProfileRequest(t, testGovernanceRouter(runtime, "space-owner", ""), http.MethodPost, "/v1/memory/governance/runs", []byte(body))
		assertManagementError(t, response, http.StatusBadRequest, errcode.ErrSharedParamInvalid.ID)
	}
}

func TestGovernanceRunGetEnforcesScopeAndHidesUnknown(t *testing.T) {
	store := newFakeGovernanceStore()
	store.runsByID["run-space-a"] = GovernanceRun{ID: "run-space-a", Mode: GovernanceModeDryRun, ScopeType: "space", ScopeID: testSpaceA, Status: "completed", ProposedCount: 2}
	store.transitions["run-space-a"] = []KnowledgeTransition{{KnowledgeItemID: "item-1", FromState: "verified", ToState: "stale", ActorType: "governance_job", ReasonCode: "GOVERNANCE_EXPIRED"}}
	runtime := testGovernanceRuntime(store)

	owner := performProfileRequest(t, testGovernanceRouter(runtime, "space-owner", ""), http.MethodGet, "/v1/memory/governance/runs/run-space-a", nil)
	if owner.Code != http.StatusOK {
		t.Fatalf("owner get status = %d, body = %s", owner.Code, owner.Body.String())
	}
	var detail governanceRunDetailResponse
	decodeProfileResponse(t, owner, &detail)
	if detail.ProposedCount != 2 || len(detail.Transitions) != 1 || detail.Transitions[0].ToState != "stale" {
		t.Fatalf("run detail = %+v", detail)
	}
	assertManagementResponseRedacted(t, owner, "policy_version_id", "created_by_uid", "idempotency_key")

	cross := performProfileRequest(t, testGovernanceRouter(runtime, "space-b-admin", ""), http.MethodGet, "/v1/memory/governance/runs/run-space-a", nil)
	assertManagementError(t, cross, http.StatusNotFound, errcode.ErrSharedNotFound.ID)

	missing := performProfileRequest(t, testGovernanceRouter(runtime, "space-owner", ""), http.MethodGet, "/v1/memory/governance/runs/run-unknown", nil)
	assertManagementError(t, missing, http.StatusNotFound, errcode.ErrSharedNotFound.ID)
}

func TestGovernanceRunListRequiresAuthorizedScope(t *testing.T) {
	store := newFakeGovernanceStore()
	store.runsByID["run-space-a"] = GovernanceRun{ID: "run-space-a", Mode: GovernanceModeApply, ScopeType: "space", ScopeID: testSpaceA, Status: "completed"}
	runtime := testGovernanceRuntime(store)

	owner := performProfileRequest(t, testGovernanceRouter(runtime, "space-owner", ""), http.MethodGet, "/v1/memory/governance/runs?scope_type=space&scope_id="+testSpaceA, nil)
	if owner.Code != http.StatusOK {
		t.Fatalf("owner list status = %d, body = %s", owner.Code, owner.Body.String())
	}
	var listBody struct {
		Runs []governanceRunResponse `json:"runs"`
	}
	decodeProfileResponse(t, owner, &listBody)
	if len(listBody.Runs) != 1 || listBody.Runs[0].ID != "run-space-a" {
		t.Fatalf("owner list = %+v", listBody.Runs)
	}

	cross := performProfileRequest(t, testGovernanceRouter(runtime, "space-b-admin", ""), http.MethodGet, "/v1/memory/governance/runs?scope_type=space&scope_id="+testSpaceA, nil)
	assertManagementError(t, cross, http.StatusNotFound, errcode.ErrSharedNotFound.ID)

	invalid := performProfileRequest(t, testGovernanceRouter(runtime, "space-owner", ""), http.MethodGet, "/v1/memory/governance/runs", nil)
	assertManagementError(t, invalid, http.StatusBadRequest, errcode.ErrSharedParamInvalid.ID)
}

func TestGovernanceCreateRecordsAuditWithMode(t *testing.T) {
	store := newFakeGovernanceStore()
	audit := &recordingAuditStore{}
	runtime := testProfileRuntime()
	runtime.Governance = governanceService(store, "policy-1")
	runtime.Audits = NewAuditService(audit)
	body := `{"mode":"dry_run","scope_type":"space","scope_id":"` + testSpaceA + `","idempotency_key":"gov-audit-1"}`
	response := performProfileRequest(t, testGovernanceRouter(runtime, "space-owner", ""), http.MethodPost, "/v1/memory/governance/runs", []byte(body))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(audit.events) != 1 || audit.events[0].EventType != AuditEventGovernanceRun || audit.events[0].ReasonCode != "governance_dry_run" {
		t.Fatalf("governance audit = %+v", audit.events)
	}
}
