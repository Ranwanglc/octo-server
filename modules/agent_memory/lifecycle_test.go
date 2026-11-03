package agent_memory

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

func lifecycleContext() *effectiveAgentContext {
	spaceID := "space-1"
	return &effectiveAgentContext{
		AgentUID: "agent-1", RobotID: "app-1", OwnerUID: "owner-1", ScopeType: ProfileScopeSpace,
		SpaceID: &spaceID, ProfileStatus: "active", TrustTier: "normal", CredentialEpoch: 7,
		Operation: CapabilityCurate, PolicyVersionID: "policy-1",
		AllowedBanks: []effectiveAgentBank{{BankID: "bank-1", HindsightBankID: "physical", BankType: "tentative", Sensitivity: "internal"}},
	}
}

func newLifecycleTestService(t *testing.T, context *effectiveAgentContext, err error) (*LifecycleService, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newAgentMemorySQLMock(t)
	connection := &dbr.Connection{DB: db, EventReceiver: &dbr.NullEventReceiver{}, Dialect: dialect.MySQL}
	service := NewLifecycleService(NewStore(connection.NewSession(nil)), persistenceContextStub{context: context, err: err})
	service.newID = func() string { return "generated-id" }
	service.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }
	return service, mock
}

func lifecycleItemRow(state string, version int64) *sqlmock.Rows {
	return sqlmock.NewRows(persistenceColumns).AddRow(
		"item-1", "bank-1", nil, "request-1", prepareInput().CanonicalKey, state, "stored", "tentative",
		"experience", "agent-1", "owner-1", "space-1", 7, nil, "relay", prepareInput().OriginFingerprint, .5, "internal",
		nil, nil, nil, "policy-1", nil, prepareInput().ContentHash, "", version, time.Now(), time.Now(),
	)
}

func transitionColumns() []string {
	return []string{
		"id", "knowledge_item_id", "from_state", "to_state", "actor_type", "actor_id", "reason_code",
		"reason_redacted", "policy_version_id", "governance_run_id", "rollback_of", "idempotency_key", "created_at",
	}
}

func lifecycleTransitionInput() LifecycleTransitionInput {
	return LifecycleTransitionInput{
		BearerToken: "app-secret", KnowledgeItemID: "item-1", Action: "verify",
		Reason: "manual review", ExpectedVersion: 3, IdempotencyKey: "curate:item-1:verify:3",
	}
}

// ApplyTransition advances the item under its expected version, appends the transition,
// and records an audit event.
func TestLifecycleApplyTransitionAdvancesUnderVersion(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("corroborated", 3))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_transition").WillReturnRows(sqlmock.NewRows(transitionColumns()))
	mock.ExpectExec("UPDATE `knowledge_item` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("verified", 4))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	record, err := service.ApplyTransition(lifecycleTransitionInput())
	if err != nil || record.State != "verified" || record.Version != 4 || record.ReasonCode != "CURATE_VERIFY" {
		t.Fatalf("ApplyTransition() = %#v, %v", record, err)
	}
	assertSQLExpectations(t, mock)
}

// A concurrent apply that finds the version already advanced loses (single winner).
func TestLifecycleApplyTransitionVersionConflictSingleWinner(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("corroborated", 3))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_transition").WillReturnRows(sqlmock.NewRows(transitionColumns()))
	mock.ExpectExec("UPDATE `knowledge_item` SET").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	if _, err := service.ApplyTransition(lifecycleTransitionInput()); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("ApplyTransition() err = %v, want ErrVersionConflict", err)
	}
	assertSQLExpectations(t, mock)
}

// A replayed transition (same idempotency key) is a no-op returning the applied item.
func TestLifecycleApplyTransitionIdempotentReplay(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("corroborated", 3))
	mock.ExpectBegin()
	existing := sqlmock.NewRows(transitionColumns()).AddRow(
		"t-1", "item-1", "corroborated", "verified", "user", "owner-1", "CURATE_VERIFY",
		"", "policy-1", nil, nil, "curate:item-1:verify:3", time.Now(),
	)
	mock.ExpectQuery("SELECT .*knowledge_transition").WillReturnRows(existing)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("verified", 4))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	record, err := service.ApplyTransition(lifecycleTransitionInput())
	if err != nil || record.State != "verified" || record.Version != 4 {
		t.Fatalf("idempotent replay = %#v, %v", record, err)
	}
	assertSQLExpectations(t, mock)
}

func TestLifecycleApplyTransitionRejectsUnsupportedAction(t *testing.T) {
	service, _ := newLifecycleTestService(t, lifecycleContext(), nil)
	input := lifecycleTransitionInput()
	input.Action = "delete"
	if _, err := service.ApplyTransition(input); !errors.Is(err, ErrLifecycleUnsupported) {
		t.Fatalf("err = %v, want ErrLifecycleUnsupported", err)
	}
}

func TestLifecycleApplyTransitionRejectsUnauthorizedBank(t *testing.T) {
	context := lifecycleContext()
	context.AllowedBanks = []effectiveAgentBank{{BankID: "other-bank", HindsightBankID: "p", BankType: "tentative"}}
	service, mock := newLifecycleTestService(t, context, nil)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("corroborated", 3))
	if _, err := service.ApplyTransition(lifecycleTransitionInput()); !errors.Is(err, ErrLifecycleForbidden) {
		t.Fatalf("err = %v, want ErrLifecycleForbidden", err)
	}
	assertSQLExpectations(t, mock)
}

// A terminal/invalid prior state cannot be transitioned by the requested action.
func TestLifecycleApplyTransitionRejectsInvalidFromState(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("revoked", 3))
	if _, err := service.ApplyTransition(lifecycleTransitionInput()); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
	assertSQLExpectations(t, mock)
}

// Rollback restores each applied transition's prior state, appends a rollback
// transition referencing the original, and marks the run rolled_back.
func TestLifecycleRollbackRestoresPriorStateAndPreservesHistory(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	runRow := sqlmock.NewRows([]string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}).
		AddRow("run-1", GovernanceModeApply, "space", "space-1", "policy-1", "completed", "gov-apply", 1, 1, 0, time.Now(), time.Now(), "owner-1", nil, 0)
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(runRow)

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows([]string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}).
		AddRow("run-1", GovernanceModeApply, "space", "space-1", "policy-1", "completed", "gov-apply", 1, 1, 0, time.Now(), time.Now(), "owner-1", nil, 0))
	mock.ExpectQuery("SELECT role FROM space_member.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow(1))
	mock.ExpectQuery("SELECT .*memory_policy_version.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("policy-1", "active"))
	applied := sqlmock.NewRows(transitionColumns()).AddRow(
		"t-1", "item-1", "corroborated", "stale", "governance_job", "gov", "GOVERNANCE_EXPIRED",
		"", "policy-1", "run-1", nil, "gov-apply:apply:item-1:3", time.Now(),
	)
	mock.ExpectQuery("SELECT .*knowledge_transition").WillReturnRows(applied)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("stale", 4))
	mock.ExpectQuery("SELECT .*memory_bank.*FOR UPDATE").WillReturnRows(bankRows().AddRow("bank-1", "hs", "Bank", "shared", BankScopeSpace, "space-1", "owner-1", "internal", RetentionClassProject, "policy-1", BankStatusActive, false, 1, time.Now(), time.Now(), nil))
	mock.ExpectExec("UPDATE `knowledge_item` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `governance_run` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	record, err := service.Rollback(LifecycleRollbackInput{BearerToken: "app-secret", RunID: "run-1"})
	if err != nil || record.RunStatus != "rolled_back" || record.RunID != "run-1" {
		t.Fatalf("Rollback() = %#v, %v", record, err)
	}
	assertSQLExpectations(t, mock)
}

// Rolling back an already rolled-back run is idempotent (no re-mutation).
func TestLifecycleRollbackIdempotentWhenAlreadyRolledBack(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	cols := []string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("run-1", GovernanceModeApply, "space", "space-1", "policy-1", "rolled_back", "gov-apply", 1, 1, 0, time.Now(), time.Now(), "owner-1", nil, 0))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("run-1", GovernanceModeApply, "space", "space-1", "policy-1", "rolled_back", "gov-apply", 1, 1, 0, time.Now(), time.Now(), "owner-1", nil, 0))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	record, err := service.Rollback(LifecycleRollbackInput{BearerToken: "app-secret", RunID: "run-1"})
	if err != nil || record.RunStatus != "rolled_back" {
		t.Fatalf("idempotent rollback = %#v, %v", record, err)
	}
	assertSQLExpectations(t, mock)
}

// A dry-run governance run is not reversible.
func TestLifecycleRollbackRejectsDryRun(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	cols := []string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("run-1", GovernanceModeDryRun, "space", "space-1", "policy-1", "completed", "gov-dry", 1, 0, 0, time.Now(), time.Now(), "owner-1", nil, 0))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("run-1", GovernanceModeDryRun, "space", "space-1", "policy-1", "completed", "gov-dry", 1, 0, 0, time.Now(), time.Now(), "owner-1", nil, 0))
	mock.ExpectRollback()

	if _, err := service.Rollback(LifecycleRollbackInput{BearerToken: "app-secret", RunID: "run-1"}); !errors.Is(err, ErrLifecycleUnsupported) {
		t.Fatalf("err = %v, want ErrLifecycleUnsupported", err)
	}
	assertSQLExpectations(t, mock)
}

// A run outside the curator scope is hidden as not found.
func TestLifecycleRollbackHidesOutOfScopeRun(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	cols := []string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("run-1", GovernanceModeApply, "space", "other-space", "policy-1", "completed", "gov-apply", 1, 1, 0, time.Now(), time.Now(), "owner-1", nil, 0))

	if _, err := service.Rollback(LifecycleRollbackInput{BearerToken: "app-secret", RunID: "run-1"}); !errors.Is(err, ErrLifecycleNotFound) {
		t.Fatalf("err = %v, want ErrLifecycleNotFound", err)
	}
	assertSQLExpectations(t, mock)
}

// Rollback detects concurrent drift on the item and refuses to restore stale data.
func TestLifecycleRollbackVersionConflictOnDrift(t *testing.T) {
	service, mock := newLifecycleTestService(t, lifecycleContext(), nil)
	cols := []string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("run-1", GovernanceModeApply, "space", "space-1", "policy-1", "completed", "gov-apply", 1, 1, 0, time.Now(), time.Now(), "owner-1", nil, 0))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(sqlmock.NewRows(cols).
		AddRow("run-1", GovernanceModeApply, "space", "space-1", "policy-1", "completed", "gov-apply", 1, 1, 0, time.Now(), time.Now(), "owner-1", nil, 0))
	mock.ExpectQuery("SELECT role FROM space_member.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow(1))
	mock.ExpectQuery("SELECT .*memory_policy_version.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("policy-1", "active"))
	applied := sqlmock.NewRows(transitionColumns()).AddRow(
		"t-1", "item-1", "corroborated", "stale", "governance_job", "gov", "GOVERNANCE_EXPIRED",
		"", "policy-1", "run-1", nil, "gov-apply:apply:item-1:3", time.Now(),
	)
	mock.ExpectQuery("SELECT .*knowledge_transition").WillReturnRows(applied)
	// item drifted away from the recorded to_state -> conflict
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(lifecycleItemRow("verified", 5))
	mock.ExpectQuery("SELECT .*memory_bank.*FOR UPDATE").WillReturnRows(bankRows().AddRow("bank-1", "hs", "Bank", "shared", BankScopeSpace, "space-1", "owner-1", "internal", RetentionClassProject, "policy-1", BankStatusActive, false, 1, time.Now(), time.Now(), nil))
	mock.ExpectRollback()

	if _, err := service.Rollback(LifecycleRollbackInput{BearerToken: "app-secret", RunID: "run-1"}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
	assertSQLExpectations(t, mock)
}

// A failed context resolve (missing curate) is forbidden before any DB access.
func TestLifecycleApplyTransitionForbiddenWithoutCurate(t *testing.T) {
	service, _ := newLifecycleTestService(t, nil, ErrAgentContextForbidden)
	if _, err := service.ApplyTransition(lifecycleTransitionInput()); !errors.Is(err, ErrLifecycleForbidden) {
		t.Fatalf("err = %v, want ErrLifecycleForbidden", err)
	}
}

func TestGovernanceLifecycleHasNoPhysicalHindsightDelete(t *testing.T) {
	for _, path := range []string{"governance.go", "lifecycle.go", "db.go"} {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		normalized := strings.ToLower(string(source))
		for _, forbidden := range []string{"delete from `knowledge_item`", "delete from knowledge_item", "deletehindsight", "delete_hindsight"} {
			if strings.Contains(normalized, forbidden) {
				t.Fatalf("%s contains forbidden physical deletion path %q", path, forbidden)
			}
		}
	}
}
