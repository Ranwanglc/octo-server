package agent_memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestBankServiceCreatePersistsLogicalBankAndIntentAtomically(t *testing.T) {
	service, mock := newTestBankService(t)
	expectMissingIntent(mock)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM memory_policy_version.*active").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("policy-v1"))
	mock.ExpectExec("INSERT INTO `memory_bank`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `memory_bank_provisioning_intent`").
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	bank, err := service.CreateLogicalBank(context.Background(), LogicalBankCreate{
		ID: "bank-1", DisplayName: "Private", BankType: "private", ScopeType: BankScopeAgent,
		ScopeID: testAgentA, OwnerUID: testOwnerA, Sensitivity: "restricted", RetentionClass: "caller-selected",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bank.HindsightBankID != "" || bank.Status != BankStatusProvisioning || bank.PolicyVersionID != "policy-v1" {
		t.Fatalf("created bank = %+v", bank)
	}
	if bank.Sensitivity != "confidential" || bank.RetentionClass != RetentionClassPersonal {
		t.Fatalf("template defaults not applied: %+v", bank)
	}
	assertSQLExpectations(t, mock)
}

func TestBankServiceCreateRollsBackWhenIntentFails(t *testing.T) {
	service, mock := newTestBankService(t)
	expectMissingIntent(mock)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM memory_policy_version.*active").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("policy-v1"))
	mock.ExpectExec("INSERT INTO `memory_bank`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `memory_bank_provisioning_intent`").WillReturnError(errors.New("intent unavailable"))
	mock.ExpectRollback()

	_, err := service.CreateLogicalBank(context.Background(), validLogicalBankCreate())
	if err == nil {
		t.Fatal("CreateLogicalBank error = nil")
	}
	assertSQLExpectations(t, mock)
}

func TestBankServiceCreateIsIdempotentAndDetectsCanonicalConflict(t *testing.T) {
	for _, test := range []struct {
		name      string
		ownerUID  string
		wantError error
	}{
		{name: "same logical bank", ownerUID: testOwnerA},
		{name: "conflicting owner", ownerUID: testOwnerB, wantError: ErrIdempotencyConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, mock := newTestBankService(t)
			canonicalKey := bankProvisionCanonicalKey(BankScopeAgent, testAgentA, "private")
			mock.ExpectQuery("SELECT \\* FROM memory_bank_provisioning_intent.*" + canonicalKey).
				WillReturnRows(intentRows().AddRow("intent-1", "bank-existing", BankIntentProvision, canonicalKey, 0, []byte(`{}`), "pending", 0, nil, time.Now(), time.Now(), nil))
			mock.ExpectQuery("SELECT \\* FROM memory_bank.*bank-existing").
				WillReturnRows(bankRows().AddRow("bank-existing", nil, "Private", "private", BankScopeAgent, testAgentA, testOwnerA, "confidential", RetentionClassPersonal, "policy-v1", BankStatusProvisioning, false, 0, time.Now(), time.Now(), nil))

			input := validLogicalBankCreate()
			input.OwnerUID = test.ownerUID
			bank, err := service.CreateLogicalBank(context.Background(), input)
			if !errors.Is(err, test.wantError) {
				t.Fatalf("CreateLogicalBank error = %v, want %v", err, test.wantError)
			}
			if test.wantError == nil && (bank == nil || bank.ID != "bank-existing") {
				t.Fatalf("idempotent bank = %+v", bank)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func TestBankServiceDisableIsAtomicNonDestructiveAndStopsAuthorization(t *testing.T) {
	service, mock := newTestBankService(t)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM memory_bank.*bank-1.*FOR UPDATE").
		WillReturnRows(bankRows().AddRow("bank-1", "hs-1", "Private", "private", BankScopeAgent, testAgentA, testOwnerA, "confidential", RetentionClassPersonal, "policy-v1", BankStatusActive, false, 3, now, now, nil))
	mock.ExpectExec("UPDATE `memory_bank` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `memory_bank_provisioning_intent`").
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	disabledAt := now.Add(time.Second)
	bank, err := service.UpdateLogicalBank(context.Background(), LogicalBankUpdate{Bank: MemoryBank{
		ID: "bank-1", HindsightBankID: "hs-1", DisplayName: "Private", BankType: "private",
		ScopeType: BankScopeAgent, ScopeID: testAgentA, OwnerUID: testOwnerA, Sensitivity: "confidential",
		RetentionClass: RetentionClassPersonal, PolicyVersionID: "policy-v1", Status: BankStatusDisabled,
		Version: 3, DisabledAt: &disabledAt,
	}, ExpectedVersion: 3})
	if err != nil {
		t.Fatal(err)
	}
	if bank.Status != BankStatusDisabled || bank.Version != 4 || bank.HindsightBankID != "hs-1" {
		t.Fatalf("disabled bank = %+v", bank)
	}
	decision := ResolveCapability(CapabilityRequest{
		Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: testAgentA},
		Profile:   AgentMemoryProfile{AgentUID: testAgentA, ScopeType: ProfileScopePlatform, Enabled: true, Status: "active"},
		Bank:      *bank, Capability: CapabilityRecall, Now: now,
		Grants: []MemoryGrant{{PrincipalType: string(PrincipalTypeAgent), PrincipalID: testAgentA, BankID: bank.ID, Capability: string(CapabilityRecall), Effect: string(GrantEffectAllow)}},
	})
	if decision.Allowed {
		t.Fatal("disabled bank authorized an existing grant")
	}
	assertSQLExpectations(t, mock)
}

func TestBankServiceReenableWithoutPhysicalIDReturnsToProvisioning(t *testing.T) {
	service, mock := newTestBankService(t)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM memory_bank.*bank-1.*FOR UPDATE").
		WillReturnRows(bankRows().AddRow("bank-1", nil, "Private", "private", BankScopeAgent, testAgentA, testOwnerA, "confidential", RetentionClassPersonal, "policy-v1", BankStatusDisabled, false, 2, now, now, now))
	mock.ExpectExec("UPDATE `memory_bank` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `memory_bank_provisioning_intent`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	bank, err := service.UpdateLogicalBank(context.Background(), LogicalBankUpdate{Bank: MemoryBank{
		ID: "bank-1", DisplayName: "Private", BankType: "private", ScopeType: BankScopeAgent,
		ScopeID: testAgentA, OwnerUID: testOwnerA, Sensitivity: "confidential", RetentionClass: RetentionClassPersonal,
		PolicyVersionID: "policy-v1", Status: BankStatusActive, Version: 2,
	}, ExpectedVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	if bank.Status != BankStatusProvisioning || bank.HindsightBankID != "" {
		t.Fatalf("re-enabled bank = %+v", bank)
	}
	assertSQLExpectations(t, mock)
}

func TestBankServiceReenableWithPhysicalIDEnqueuesNewerProvisionIntent(t *testing.T) {
	service, mock := newTestBankService(t)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM memory_bank.*bank-1.*FOR UPDATE").
		WillReturnRows(bankRows().AddRow("bank-1", "hs-1", "Private", "private", BankScopeAgent, testAgentA, testOwnerA, "confidential", RetentionClassPersonal, "policy-v1", BankStatusDisabled, false, 4, now, now, now))
	mock.ExpectExec("UPDATE `memory_bank` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `memory_bank_provisioning_intent`").WithArgs(sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	bank, err := service.UpdateLogicalBank(context.Background(), LogicalBankUpdate{Bank: MemoryBank{
		ID: "bank-1", HindsightBankID: "hs-1", DisplayName: "Private", BankType: "private",
		ScopeType: BankScopeAgent, ScopeID: testAgentA, OwnerUID: testOwnerA, Sensitivity: "confidential",
		RetentionClass: RetentionClassPersonal, PolicyVersionID: "policy-v1", Status: BankStatusActive, Version: 4,
	}, ExpectedVersion: 4})
	if err != nil {
		t.Fatal(err)
	}
	if bank.Status != BankStatusActive || bank.Version != 5 {
		t.Fatalf("re-enabled bank = %+v", bank)
	}
	assertSQLExpectations(t, mock)
}

func TestBankServiceUpdateRejectsStaleVersionBeforeWriting(t *testing.T) {
	service, mock := newTestBankService(t)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM memory_bank.*bank-1.*FOR UPDATE").
		WillReturnRows(bankRows().AddRow("bank-1", "hs-1", "Private", "private", BankScopeAgent, testAgentA, testOwnerA, "confidential", RetentionClassPersonal, "policy-v1", BankStatusActive, false, 4, now, now, nil))
	mock.ExpectRollback()

	_, err := service.UpdateLogicalBank(context.Background(), LogicalBankUpdate{Bank: MemoryBank{
		ID: "bank-1", ScopeType: BankScopeAgent, ScopeID: testAgentA,
	}, ExpectedVersion: 3})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateLogicalBank error = %v, want ErrVersionConflict", err)
	}
	assertSQLExpectations(t, mock)
}

func newTestBankService(t *testing.T) (*BankService, sqlmock.Sqlmock) {
	t.Helper()
	store, mock := newTestStore(t)
	return NewBankService(store), mock
}

func validLogicalBankCreate() LogicalBankCreate {
	return LogicalBankCreate{ID: "bank-1", DisplayName: "Private", BankType: "private", ScopeType: BankScopeAgent, ScopeID: testAgentA, OwnerUID: testOwnerA}
}

func expectMissingIntent(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT \\* FROM memory_bank_provisioning_intent.*" + bankProvisionCanonicalKey(BankScopeAgent, testAgentA, "private")).
		WillReturnRows(intentRows())
}

func bankRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "hindsight_bank_id", "display_name", "bank_type", "scope_type", "scope_id", "owner_uid", "sensitivity", "retention_class", "policy_version_id", "status", "legacy", "version", "created_at", "updated_at", "disabled_at"})
}

func intentRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "bank_id", "operation", "canonical_key", "bank_version", "payload_json", "status", "attempt_count", "last_error", "created_at", "updated_at", "completed_at"})
}

func assertSQLExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
