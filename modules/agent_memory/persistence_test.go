package agent_memory

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

type persistenceContextStub struct {
	context *effectiveAgentContext
	err     error
}

func (stub persistenceContextStub) Resolve(agentContextRequest) (*effectiveAgentContext, error) {
	return stub.context, stub.err
}

func persistenceContext() *effectiveAgentContext {
	spaceID := "space-1"
	return &effectiveAgentContext{
		AgentUID: "agent-1", RobotID: "app-1", OwnerUID: "owner-1", ScopeType: ProfileScopeSpace,
		SpaceID: &spaceID, ProfileStatus: "active", TrustTier: "normal", CredentialEpoch: 7,
		Operation: CapabilityPropose, PolicyVersionID: "policy-1",
		AllowedBanks: []effectiveAgentBank{{BankID: "bank-1", HindsightBankID: "physical-secret", BankType: "tentative", Sensitivity: "internal"}},
	}
}

func prepareInput() PersistencePrepare {
	return PersistencePrepare{
		BearerToken: "app-secret", BankID: "bank-1", IdempotencyKey: "request-1",
		CanonicalKey:   "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1",
		LifecycleState: "tentative", FactType: "experience", AssertionKind: "relay", Confidence: .5,
		ContentHash:       "b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2",
		OriginFingerprint: "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3",
		EvidenceIDs:       []string{"evidence-1"}, ExpectedVersion: 0,
	}
}

var persistenceColumns = []string{
	"id", "bank_id", "hindsight_memory_id", "idempotency_key", "canonical_key", "state", "persistence_status", "lifecycle_state",
	"fact_type", "authenticated_source_agent_uid", "owner_uid", "space_id", "credential_epoch", "source_instance_id", "assertion_kind",
	"origin_fingerprint", "confidence", "sensitivity", "valid_from", "valid_until", "review_after", "policy_version_id", "gate_run_id",
	"content_hash", "failure_reason_code", "version", "created_at", "updated_at",
}

func persistenceRows(status, state string, version int64, memoryID any) *sqlmock.Rows {
	return sqlmock.NewRows(persistenceColumns).AddRow(
		"control-1", "bank-1", memoryID, "request-1", prepareInput().CanonicalKey, state, status, "tentative",
		"experience", "agent-1", "owner-1", "space-1", 7, nil, "relay", prepareInput().OriginFingerprint, .5, "internal",
		nil, nil, nil, "policy-1", nil, prepareInput().ContentHash, "", version, time.Now(), time.Now(),
	)
}

func newPersistenceTestService(t *testing.T) (*PersistenceService, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newAgentMemorySQLMock(t)
	connection := &dbr.Connection{DB: db, EventReceiver: &dbr.NullEventReceiver{}, Dialect: dialect.MySQL}
	service := NewPersistenceService(NewStore(connection.NewSession(nil)), persistenceContextStub{context: persistenceContext()})
	service.newID = func() string { return "generated-id" }
	return service, mock
}

func TestPersistencePrepareCreatesEvidenceTransitionAndAuditAtomically(t *testing.T) {
	service, mock := newPersistenceTestService(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("INSERT INTO `knowledge_item`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `knowledge_evidence`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	result, err := service.Prepare(prepareInput())
	if err != nil || result.ControlID != "generated-id" || result.PersistenceStatus != "persisting" || result.Version != 0 {
		t.Fatalf("Prepare() = %#v, %v", result, err)
	}
	assertSQLExpectations(t, mock)
}

func TestPersistencePrepareIdenticalRetryReturnsExistingAndConflictDoesNotDuplicate(t *testing.T) {
	service, mock := newPersistenceTestService(t)
	for _, conflict := range []bool{false, true} {
		mock.ExpectBegin()
		rows := persistenceRows("persisting", "persisting", 0, nil)
		if conflict {
			rows = sqlmock.NewRows(persistenceColumns).AddRow(
				"control-1", "bank-1", nil, "request-1", prepareInput().CanonicalKey, "persisting", "persisting", "tentative",
				"world", "agent-1", "owner-1", "space-1", 7, nil, "relay", prepareInput().OriginFingerprint, .5, "internal",
				nil, nil, nil, "policy-1", nil, prepareInput().ContentHash, "", 0, time.Now(), time.Now(),
			)
		}
		mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(rows)
		mock.ExpectQuery("SELECT .*evidence_item_id").WillReturnRows(sqlmock.NewRows([]string{"evidence_item_id"}).AddRow("evidence-1"))
		if conflict {
			mock.ExpectRollback()
		} else {
			mock.ExpectCommit()
		}
		result, err := service.Prepare(prepareInput())
		if conflict && !errors.Is(err, ErrPersistenceConflict) {
			t.Fatalf("conflict error = %v", err)
		}
		if !conflict && (err != nil || result.ControlID != "control-1") {
			t.Fatalf("retry = %#v, %v", result, err)
		}
	}
	assertSQLExpectations(t, mock)
}

func TestPersistenceCompleteIdempotencyConflictAndCAS(t *testing.T) {
	service, mock := newPersistenceTestService(t)
	input := PersistenceMutation{BearerToken: "app-secret", ControlID: "control-1", ExpectedVersion: 0, HindsightMemoryID: "memory-1", LifecycleState: "tentative"}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(persistenceRows("persisting", "persisting", 0, nil))
	mock.ExpectExec("UPDATE `knowledge_item`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	result, err := service.Complete(input)
	if err != nil || result.PersistenceStatus != "stored" || result.Version != 1 {
		t.Fatalf("Complete() = %#v, %v", result, err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(persistenceRows("stored", "tentative", 1, "memory-1"))
	mock.ExpectCommit()
	result, err = service.Complete(input)
	if err != nil || result.Version != 1 {
		t.Fatalf("idempotent Complete() = %#v, %v", result, err)
	}

	conflict := input
	conflict.HindsightMemoryID = "memory-2"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(persistenceRows("stored", "tentative", 1, "memory-1"))
	mock.ExpectRollback()
	if _, err = service.Complete(conflict); !errors.Is(err, ErrPersistenceConflict) {
		t.Fatalf("conflicting Complete error = %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(persistenceRows("persisting", "persisting", 0, nil))
	mock.ExpectExec("UPDATE `knowledge_item`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if _, err = service.Complete(input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("CAS Complete error = %v", err)
	}
	assertSQLExpectations(t, mock)
}

func TestPersistencePrepareCanonicalDuplicateConverges(t *testing.T) {
	service, mock := newPersistenceTestService(t)
	input := prepareInput()
	input.IdempotencyKey = "request-2"
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(persistenceRows("persisting", "persisting", 0, nil))
	mock.ExpectQuery("SELECT .*evidence_item_id").WillReturnRows(sqlmock.NewRows([]string{"evidence_item_id"}).AddRow("evidence-1"))
	mock.ExpectCommit()
	result, err := service.Prepare(input)
	if err != nil || result.ControlID != "control-1" {
		t.Fatalf("canonical Prepare() = %#v, %v", result, err)
	}
	assertSQLExpectations(t, mock)
}

func TestPersistenceFailureAndReconciliationAreVisible(t *testing.T) {
	service, mock := newPersistenceTestService(t)
	for _, reconciliation := range []bool{false, true} {
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT .*knowledge_item").WillReturnRows(persistenceRows("persisting", "persisting", 0, nil))
		mock.ExpectExec("UPDATE `knowledge_item`").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec("INSERT INTO `knowledge_transition`").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()
		result, err := service.Fail(PersistenceMutation{BearerToken: "app-secret", ControlID: "control-1", ExpectedVersion: 0, ReasonCode: "UPSTREAM_FAILED", Retryable: true, Reconciliation: reconciliation})
		want := "retryable_failed"
		if reconciliation {
			want = "reconciliation_required"
		}
		if err != nil || result.PersistenceStatus != want || result.ReasonCode != "UPSTREAM_FAILED" {
			t.Fatalf("Fail() = %#v, %v", result, err)
		}
	}
	assertSQLExpectations(t, mock)
}

func TestPersistenceFailsClosedOnInactiveAuthority(t *testing.T) {
	service, _ := newPersistenceTestService(t)
	service.contexts = persistenceContextStub{err: ErrAgentContextAuthentication}
	if _, err := service.Prepare(prepareInput()); !errors.Is(err, ErrPersistenceForbidden) {
		t.Fatalf("Prepare error = %v", err)
	}
	if _, err := service.Complete(PersistenceMutation{BearerToken: "x", ControlID: "control-1", HindsightMemoryID: "memory-1", LifecycleState: "tentative"}); !errors.Is(err, ErrPersistenceForbidden) {
		t.Fatalf("Complete error = %v", err)
	}
}
