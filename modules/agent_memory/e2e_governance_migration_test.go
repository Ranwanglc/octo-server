package agent_memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

// T056 server leg of the governance/migration/rollback acceptance. It drives the
// real store-backed GovernanceService (dry-run vs apply) and the real
// LifecycleService.Rollback over sqlmock, using only the isolated e2e_* IDs, and
// proves SC-007 on the authoritative side: dry-run mutates no knowledge item,
// apply advances under the optimistic version, rollback restores the prior state
// with the transition history preserved, and no path issues a physical delete.

const (
	e2eGovBank    = "bank_e2e_space_alpha_verified"
	e2eGovItem    = "ki_e2e_space_alpha_1"
	e2eGovPolicy  = "mpv_e2e_0001"
	e2eGovRunKey  = "gov_e2e_apply_0001"
	e2eGovScopeID = "space_e2e_alpha"
)

func newE2EGovernanceStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newAgentMemorySQLMock(t)
	connection := &dbr.Connection{DB: db, EventReceiver: &dbr.NullEventReceiver{}, Dialect: dialect.MySQL}
	return NewStore(connection.NewSession(nil)), mock
}

func e2eStoreGovernanceService(store *Store) *GovernanceService {
	service := NewGovernanceService(store, fixedPolicyResolver{id: e2eGovPolicy})
	counter := 0
	service.newID = func() string { counter++; return "e2e-gov-run-" + string(rune('a'+counter-1)) }
	service.now = func() time.Time { return time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC) }
	return service
}

func e2eLifecycleContext() *effectiveAgentContext {
	spaceID := e2eGovScopeID
	return &effectiveAgentContext{
		AgentUID: "agent_e2e_alpha", RobotID: "app_bot_e2e_alpha", OwnerUID: "owner_e2e_alpha",
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID, ProfileStatus: "active", TrustTier: "high",
		CredentialEpoch: 42, Operation: CapabilityCurate, PolicyVersionID: e2eGovPolicy,
		AllowedBanks: []effectiveAgentBank{{BankID: e2eGovBank, HindsightBankID: "hs_e2e_space_alpha_verified", BankType: "shared", Sensitivity: "internal"}},
	}
}

func e2eGovItemRow(state string, version int64, validUntil *time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(persistenceColumns).AddRow(
		e2eGovItem, e2eGovBank, nil, "req_e2e_1", "canon_e2e", state, "stored", "verified",
		"world", "agent_e2e_alpha", "owner_e2e_alpha", e2eGovScopeID, 42, nil, "firsthand",
		"origin_e2e", .95, "internal", nil, validUntil, nil, e2eGovPolicy, nil, "hash_e2e", "", version, time.Now(), time.Now(),
	)
}

func e2eGovRunRow(runID, status string) *sqlmock.Rows {
	cols := []string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}
	return sqlmock.NewRows(cols).AddRow(
		runID, GovernanceModeApply, "space", e2eGovScopeID, e2eGovPolicy, status, e2eGovRunKey, 1, 1, 0, time.Now(), time.Now(), "owner_e2e_alpha", nil, 0,
	)
}

func e2eGovTransitionCols() []string {
	return []string{
		"id", "knowledge_item_id", "from_state", "to_state", "actor_type", "actor_id", "reason_code",
		"reason_redacted", "policy_version_id", "governance_run_id", "rollback_of", "idempotency_key", "created_at",
	}
}

// Dry-run: the governance run records one soft-transition proposal and issues NO
// UPDATE against knowledge_item (zero state changes), and never proposes delete/revoke.
func TestE2EGovernanceDryRunChangesNoKnowledgeState(t *testing.T) {
	store, mock := newE2EGovernanceStore(t)
	service := e2eStoreGovernanceService(store)
	past := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// scope banks -> governable items
	// Create first probes idempotency (miss), then resolves scope banks and items.
	mock.ExpectQuery("SELECT .*governance_run").WillReturnError(dbr.ErrNotFound)
	mock.ExpectQuery("SELECT .*memory_bank").WillReturnRows(e2eBankRow())
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(e2eGovItemRow("verified", 3, &past))
	// persist run + proposal transition; dry-run must NOT UPDATE knowledge_item
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	run, err := service.Create(context.Background(), GovernanceRunRequest{
		Mode: GovernanceModeDryRun, ScopeType: GovernanceScopeSpace, ScopeID: e2eGovScopeID,
		IdempotencyKey: "gov_e2e_dry_0001", ActorUID: "owner_e2e_alpha",
	})
	if err != nil {
		t.Fatalf("dry-run create error = %v", err)
	}
	if run.Mode != GovernanceModeDryRun || run.ProposedCount != 1 || run.AppliedCount != 0 {
		t.Fatalf("dry-run run = %+v", run)
	}
	assertSQLExpectations(t, mock)
}

// Apply then rollback: apply advances the item under its version and appends a
// transition; rollback restores the prior state, appends a rollback transition
// referencing the original, and marks the run rolled_back. No physical delete.
func TestE2EGovernanceApplyThenRollbackRestoresPriorState(t *testing.T) {
	store, mock := newE2EGovernanceStore(t)
	service := e2eStoreGovernanceService(store)
	lifecycle := NewLifecycleService(store, persistenceContextStub{context: e2eLifecycleContext()})
	lifecycle.newID = func() string { return "e2e-rollback-txn" }
	lifecycle.now = func() time.Time { return time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC) }
	past := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// --- apply governance run: verified -> stale (expired) ---
	mock.ExpectQuery("SELECT .*governance_run").WillReturnError(dbr.ErrNotFound)
	mock.ExpectQuery("SELECT .*memory_bank").WillReturnRows(e2eBankRow())
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(e2eGovItemRow("verified", 3, &past))
	mock.ExpectBegin()
	expectApplyAuthorityLocks(mock, e2eGovPolicy)
	mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
	expectApplyItemLocks(mock, e2eGovPolicy, e2eGovScopeID, "owner_e2e_alpha", e2eGovBank, e2eGovItem, "verified", 3)
	mock.ExpectExec("UPDATE `knowledge_item` SET").WillReturnResult(sqlmock.NewResult(0, 1)) // one item advanced
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	run, err := service.Create(context.Background(), GovernanceRunRequest{
		Mode: GovernanceModeApply, ScopeType: GovernanceScopeSpace, ScopeID: e2eGovScopeID,
		IdempotencyKey: e2eGovRunKey, ActorUID: "owner_e2e_alpha",
	})
	if err != nil {
		t.Fatalf("apply create error = %v", err)
	}
	if run.AppliedCount != 1 || run.ProposedCount != 1 {
		t.Fatalf("apply run = %+v", run)
	}
	runID := run.ID

	// --- rollback the apply run: stale -> verified (prior), history preserved ---
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(e2eGovRunRow(runID, "completed"))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(e2eGovRunRow(runID, "completed"))
	mock.ExpectQuery("SELECT role FROM space_member.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow(1))
	mock.ExpectQuery("SELECT .*memory_policy_version.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(e2eGovPolicy, "active"))
	applied := sqlmock.NewRows(e2eGovTransitionCols()).AddRow(
		"t-e2e-1", e2eGovItem, "verified", "stale", "governance_job", "owner_e2e_alpha", "GOVERNANCE_EXPIRED",
		"", e2eGovPolicy, runID, nil, e2eGovRunKey+":apply:"+e2eGovItem+":3", time.Now(),
	)
	mock.ExpectQuery("SELECT .*knowledge_transition").WillReturnRows(applied)
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(e2eGovItemRow("stale", 4, &past))
	mock.ExpectQuery("SELECT .*memory_bank.*FOR UPDATE").WillReturnRows(e2eBankRow())
	mock.ExpectExec("UPDATE `knowledge_item` SET").WillReturnResult(sqlmock.NewResult(0, 1)) // restore prior
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE `governance_run` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	record, err := lifecycle.Rollback(LifecycleRollbackInput{BearerToken: "app_bot_e2e_alpha", RunID: runID})
	if err != nil || record.RunStatus != "rolled_back" || record.RunID != runID {
		t.Fatalf("Rollback() = %#v, %v", record, err)
	}
	assertSQLExpectations(t, mock)
}

// Governance never proposes a physical/hard delete for any lifecycle state.
func TestE2EGovernanceNeverProposesDelete(t *testing.T) {
	past := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	for _, item := range []KnowledgeItem{
		{ID: e2eGovItem, State: "verified", ValidUntil: &past},
		{ID: e2eGovItem, State: "corroborated", ValidUntil: &past},
		{ID: e2eGovItem, State: "tentative"},
	} {
		toState, _, ok := proposeSoftTransition(item, now)
		if ok && (toState == "deleted" || toState == "revoked") {
			t.Fatalf("governance proposed destructive transition to %q", toState)
		}
	}
}

// Rollback of a dry-run governance run is refused: dry-run mutated nothing, so
// there is nothing to reverse (guards accidental "undo" of a no-op).
func TestE2EGovernanceDryRunIsNotReversible(t *testing.T) {
	store, mock := newE2EGovernanceStore(t)
	lifecycle := NewLifecycleService(store, persistenceContextStub{context: e2eLifecycleContext()})
	cols := []string{"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key", "proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid", "rollback_idempotency_key", "version"}
	dryRow := func() *sqlmock.Rows {
		return sqlmock.NewRows(cols).AddRow("e2e-gov-dry", GovernanceModeDryRun, "space", e2eGovScopeID, e2eGovPolicy, "completed", "gov_e2e_dry_0001", 1, 0, 0, time.Now(), time.Now(), "owner_e2e_alpha", nil, 0)
	}
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(dryRow())
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*governance_run").WillReturnRows(dryRow())
	mock.ExpectRollback()

	if _, err := lifecycle.Rollback(LifecycleRollbackInput{BearerToken: "app_bot_e2e_alpha", RunID: "e2e-gov-dry"}); !errors.Is(err, ErrLifecycleUnsupported) {
		t.Fatalf("dry-run rollback err = %v, want ErrLifecycleUnsupported", err)
	}
	assertSQLExpectations(t, mock)
}

func e2eBankRow() *sqlmock.Rows {
	now := time.Now().UTC()
	return bankRows().AddRow(
		e2eGovBank, "hs_e2e_space_alpha_verified", "Space Verified", "shared",
		BankScopeSpace, e2eGovScopeID, "owner_e2e_alpha", "internal", RetentionClassPersonal,
		e2eGovPolicy, BankStatusActive, false, 1, now, now, nil,
	)
}
