package agent_memory

import (
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// dry-run persistence records proposal transitions only; no knowledge_item UPDATE and never a DELETE.
func TestStorePersistGovernanceRunDryRunRecordsProposalsOnly(t *testing.T) {
	store, mock := newTestStore(t)
	run := &GovernanceRun{ID: "run-1", Mode: GovernanceModeDryRun, ScopeType: "space", ScopeID: testSpaceA, PolicyVersionID: "policy-1", Status: "completed", IdempotencyKey: "gov-dry", ProposedCount: 1}
	proposals := []GovernanceProposal{{
		BankID: "bank-space-a", ExpectedVersion: 2,
		Transition: KnowledgeTransition{ID: "t-1", KnowledgeItemID: "item-1", FromState: "verified", ToState: "stale", ActorType: "governance_job", ActorID: "gov", ReasonCode: "GOVERNANCE_EXPIRED", PolicyVersionID: "policy-1", IdempotencyKey: "gov-dry:dry_run:item-1:2"},
	}}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := store.PersistGovernanceRun(run, proposals, false); err != nil {
		t.Fatalf("PersistGovernanceRun dry-run error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// apply persistence advances the item under its expected version, then records the transition.
func TestStorePersistGovernanceRunApplyAdvancesItemVersion(t *testing.T) {
	store, mock := newTestStore(t)
	run := &GovernanceRun{ID: "run-2", Mode: GovernanceModeApply, ScopeType: "space", ScopeID: testSpaceA, PolicyVersionID: "policy-1", Status: "completed", IdempotencyKey: "gov-apply", ProposedCount: 1, AppliedCount: 1}
	proposals := []GovernanceProposal{{
		BankID: "bank-space-a", ExpectedVersion: 4,
		Transition: KnowledgeTransition{ID: "t-2", KnowledgeItemID: "item-2", FromState: "corroborated", ToState: "stale", ActorType: "governance_job", ActorID: "gov", ReasonCode: "GOVERNANCE_EXPIRED", PolicyVersionID: "policy-1", IdempotencyKey: "gov-apply:apply:item-2:4"},
	}}

	mock.ExpectBegin()
	expectApplyAuthorityLocks(mock, "policy-1")
	mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
	expectApplyItemLocks(mock, "policy-1", testSpaceA, "gov", "bank-space-a", "item-2", "corroborated", 4)
	mock.ExpectExec("UPDATE `knowledge_item` SET .* WHERE " + regexp.QuoteMeta("(id='item-2' AND bank_id='bank-space-a' AND state='corroborated' AND version=4)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := store.PersistGovernanceRun(run, proposals, true); err != nil {
		t.Fatalf("PersistGovernanceRun apply error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Apply is scoped to the proposal's logical Bank and cannot touch a shared legacy Bank.
func TestStorePersistGovernanceRunApplyLeavesSharedLegacyBankUntouched(t *testing.T) {
	store, mock := newTestStore(t)
	run := &GovernanceRun{ID: "run-governed", Mode: GovernanceModeApply, ScopeType: "space", ScopeID: testSpaceA, PolicyVersionID: "policy-1", Status: "completed", IdempotencyKey: "gov-scoped", ProposedCount: 1, AppliedCount: 1}
	proposals := []GovernanceProposal{{
		BankID: "bank-governed", ExpectedVersion: 2,
		Transition: KnowledgeTransition{ID: "t-governed", KnowledgeItemID: "item-governed", FromState: "verified", ToState: "stale", ActorType: "governance_job", ActorID: "gov", ReasonCode: "GOVERNANCE_EXPIRED", PolicyVersionID: "policy-1", IdempotencyKey: "gov-scoped:apply:item-governed:2"},
	}}

	mock.ExpectBegin()
	expectApplyAuthorityLocks(mock, "policy-1")
	mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
	expectApplyItemLocks(mock, "policy-1", testSpaceA, "gov", "bank-governed", "item-governed", "verified", 2)
	mock.ExpectExec("UPDATE `knowledge_item` SET .* WHERE " + regexp.QuoteMeta("(id='item-governed' AND bank_id='bank-governed' AND state='verified' AND version=2)")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := store.PersistGovernanceRun(run, proposals, true); err != nil {
		t.Fatalf("PersistGovernanceRun scoped apply error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// apply persistence rolls back when the item version drifted (concurrent worker protection).
func TestStorePersistGovernanceRunApplyRollsBackOnVersionConflict(t *testing.T) {
	store, mock := newTestStore(t)
	run := &GovernanceRun{ID: "run-3", Mode: GovernanceModeApply, ScopeType: "space", ScopeID: testSpaceA, PolicyVersionID: "policy-1", Status: "completed", IdempotencyKey: "gov-apply-conflict"}
	proposals := []GovernanceProposal{{
		BankID: "bank-space-a", ExpectedVersion: 4,
		Transition: KnowledgeTransition{ID: "t-3", KnowledgeItemID: "item-3", FromState: "corroborated", ToState: "stale", ActorType: "governance_job", ActorID: "gov", ReasonCode: "GOVERNANCE_EXPIRED", PolicyVersionID: "policy-1", IdempotencyKey: "gov-apply-conflict:apply:item-3:4"},
	}}

	mock.ExpectBegin()
	expectApplyAuthorityLocks(mock, "policy-1")
	mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
	expectApplyItemLocks(mock, "policy-1", testSpaceA, "", "bank-space-a", "item-3", "corroborated", 4)
	mock.ExpectExec("UPDATE `knowledge_item` SET").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	if err := store.PersistGovernanceRun(run, proposals, true); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("PersistGovernanceRun apply error = %v, want ErrVersionConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectApplyAuthorityLocks(mock sqlmock.Sqlmock, policyID string) {
	mock.ExpectQuery("SELECT .*memory_policy_version.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(policyID, "active"))
	mock.ExpectQuery("SELECT role FROM space_member.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow(1))
}

func expectApplyItemLocks(mock sqlmock.Sqlmock, policyID, scopeID, actor, bankID, itemID, state string, version int64) {
	now := time.Now()
	mock.ExpectQuery("SELECT .*memory_bank.*FOR UPDATE").WillReturnRows(bankRows().AddRow(bankID, "hs", "Bank", "shared", BankScopeSpace, scopeID, actor, "internal", RetentionClassProject, policyID, BankStatusActive, false, 1, now, now, nil))
	mock.ExpectQuery("SELECT .*knowledge_item.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "bank_id", "state", "policy_version_id", "version"}).AddRow(itemID, bankID, state, policyID, version))
}

func TestStorePersistGovernanceRunApplyRevalidatesLockedAuthorityAndFacts(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(sqlmock.Sqlmock)
		want    error
	}{
		{"concurrent authority revocation", func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery("SELECT .*memory_policy_version.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("policy-1", "active"))
			mock.ExpectQuery("SELECT role FROM space_member.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"role"}))
		}, ErrGovernanceForbidden},
		{"policy drift", func(mock sqlmock.Sqlmock) {
			mock.ExpectQuery("SELECT .*memory_policy_version.*FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow("policy-1", "retired"))
		}, ErrGovernanceConflict},
		{"bank disabled", func(mock sqlmock.Sqlmock) {
			expectApplyAuthorityLocks(mock, "policy-1")
			mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
			now := time.Now()
			mock.ExpectQuery("SELECT .*memory_bank.*FOR UPDATE").WillReturnRows(bankRows().AddRow("bank-1", "hs", "Bank", "shared", BankScopeSpace, testSpaceA, "owner", "internal", RetentionClassProject, "policy-1", BankStatusDisabled, false, 2, now, now, &now))
		}, ErrGovernanceConflict},
		{"item version conflict", func(mock sqlmock.Sqlmock) {
			expectApplyAuthorityLocks(mock, "policy-1")
			mock.ExpectExec("INSERT INTO `governance_run`").WillReturnResult(sqlmock.NewResult(1, 1))
			expectApplyItemLocks(mock, "policy-1", testSpaceA, "owner", "bank-1", "item-1", "verified", 8)
		}, ErrGovernanceConflict},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store, mock := newTestStore(t)
			run := &GovernanceRun{ID: "run", Mode: GovernanceModeApply, ScopeType: "space", ScopeID: testSpaceA, PolicyVersionID: "policy-1", Status: "completed", IdempotencyKey: "key", CreatedByUID: "owner"}
			proposal := GovernanceProposal{BankID: "bank-1", ExpectedVersion: 7, Transition: KnowledgeTransition{ID: "transition", KnowledgeItemID: "item-1", FromState: "verified", ToState: "stale", PolicyVersionID: "policy-1", IdempotencyKey: "transition-key"}}
			mock.ExpectBegin()
			test.arrange(mock)
			mock.ExpectRollback()
			if err := store.PersistGovernanceRun(run, []GovernanceProposal{proposal}, true); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStorePersistGovernanceRunRollsBackOnIdempotencyConflict(t *testing.T) {
	store, mock := newTestStore(t)
	run := &GovernanceRun{ID: "run-4", Mode: GovernanceModeDryRun, ScopeType: "space", ScopeID: testSpaceA, PolicyVersionID: "policy-1", Status: "completed", IdempotencyKey: "gov-dup"}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `governance_run`").WillReturnError(errors.New("Error 1062: Duplicate entry for uk_governance_run_idempotency"))
	mock.ExpectRollback()

	if err := store.PersistGovernanceRun(run, nil, false); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("PersistGovernanceRun error = %v, want ErrIdempotencyConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
