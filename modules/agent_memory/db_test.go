package agent_memory

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

func newTestStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock := newAgentMemorySQLMock(t)
	connection := &dbr.Connection{
		DB:            rawDB,
		EventReceiver: &dbr.NullEventReceiver{},
		Dialect:       dialect.MySQL,
	}
	return NewStore(connection.NewSession(nil)), mock
}

func TestStoreCreateProfileReturnsDuplicate(t *testing.T) {
	store, mock := newTestStore(t)
	profile := validSpaceProfile()

	mock.ExpectExec("INSERT INTO `agent_memory_profile`").
		WillReturnError(&mysql.MySQLError{Number: 1062, Message: "duplicate primary key"})

	err := store.CreateProfile(&profile)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("CreateProfile error = %v, want ErrDuplicate", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCreateGovernanceRunReturnsIdempotencyConflict(t *testing.T) {
	store, mock := newTestStore(t)
	run := GovernanceRun{
		ID:              "run-1",
		Mode:            "apply",
		ScopeType:       "space",
		ScopeID:         testSpaceA,
		PolicyVersionID: "policy-1",
		Status:          "queued",
		IdempotencyKey:  "governance-space-a-1",
		CreatedByUID:    testOwnerA,
	}

	mock.ExpectExec("INSERT INTO `governance_run`").
		WillReturnError(errors.New("Error 1062: Duplicate entry for uk_governance_run_idempotency"))

	err := store.CreateGovernanceRun(&run)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("CreateGovernanceRun error = %v, want ErrIdempotencyConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCreateGovernanceRunKeepsDuplicateIDDistinct(t *testing.T) {
	store, mock := newTestStore(t)
	run := GovernanceRun{ID: "run-1", IdempotencyKey: "new-key"}

	mock.ExpectExec("INSERT INTO `governance_run`").
		WillReturnError(&mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'run-1' for key 'PRIMARY'"})

	err := store.CreateGovernanceRun(&run)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("CreateGovernanceRun error = %v, want ErrDuplicate", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCreateTransitionReturnsIdempotencyConflict(t *testing.T) {
	store, mock := newTestStore(t)
	transition := KnowledgeTransition{
		ID:              "transition-1",
		KnowledgeItemID: "item-1",
		FromState:       "candidate",
		ToState:         "tentative",
		ActorType:       "agent",
		ActorID:         testAgentA,
		ReasonCode:      "submitted",
		PolicyVersionID: "policy-1",
		IdempotencyKey:  "transition-item-1-tentative",
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `knowledge_transition`").
		WillReturnError(&mysql.MySQLError{Number: 1062, Message: "uk_knowledge_transition_idempotency"})
	mock.ExpectRollback()

	err := store.TransitionKnowledgeItem("bank-1", 0, &transition)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("CreateTransition error = %v, want ErrIdempotencyConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreTransitionKnowledgeItemRollsBackOnVersionConflict(t *testing.T) {
	store, mock := newTestStore(t)
	transition := KnowledgeTransition{
		ID:              "transition-2",
		KnowledgeItemID: "item-2",
		FromState:       "corroborated",
		ToState:         "verified",
		ActorType:       "user",
		ActorID:         testOwnerA,
		ReasonCode:      "reviewed",
		PolicyVersionID: "policy-1",
		IdempotencyKey:  "transition-item-2-verified",
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `knowledge_transition`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `knowledge_item` SET .* WHERE " + regexp.QuoteMeta("(id='item-2' AND bank_id='bank-1' AND state='corroborated' AND version=3)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.TransitionKnowledgeItem("bank-1", 3, &transition)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("TransitionKnowledgeItem error = %v, want ErrVersionConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreWithTxRollsBackCallbackFailure(t *testing.T) {
	store, mock := newTestStore(t)
	wantErr := errors.New("write failed")

	mock.ExpectBegin()
	mock.ExpectRollback()

	err := store.WithTx(func(*dbr.Tx) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTx error = %v, want %v", err, wantErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreUpdateKnowledgeStateUsesOptimisticVersion(t *testing.T) {
	store, mock := newTestStore(t)
	query := "UPDATE `knowledge_item` SET .* WHERE " + regexp.QuoteMeta("(id='item-1' AND bank_id='bank-1' AND state='corroborated' AND version=4)")
	mock.ExpectExec(query).WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.UpdateKnowledgeState("item-1", "bank-1", "corroborated", "verified", 4)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateKnowledgeState error = %v, want ErrVersionConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreUpdateBankPinsScope(t *testing.T) {
	store, mock := newTestStore(t)
	bank := MemoryBank{
		ID:              "bank-space-a",
		ScopeType:       BankScopeSpace,
		ScopeID:         testSpaceA,
		DisplayName:     "Space A",
		PolicyVersionID: "policy-1",
	}
	query := "UPDATE `memory_bank` SET .* WHERE " + regexp.QuoteMeta("(id='bank-space-a' AND scope_type='space' AND scope_id='space-agent-memory-a' AND version=2)")
	mock.ExpectExec(query).WillReturnResult(sqlmock.NewResult(0, 0))

	err := store.UpdateBank(&bank, 2)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateBank error = %v, want ErrVersionConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentMemoryProfileScopeInvariants(t *testing.T) {
	spaceID := testSpaceA
	tests := []struct {
		name    string
		profile AgentMemoryProfile
		wantErr bool
	}{
		{name: "platform", profile: AgentMemoryProfile{ScopeType: ProfileScopePlatform}},
		{name: "space", profile: AgentMemoryProfile{ScopeType: ProfileScopeSpace, SpaceID: &spaceID}},
		{name: "platform with space", profile: AgentMemoryProfile{ScopeType: ProfileScopePlatform, SpaceID: &spaceID}, wantErr: true},
		{name: "space without space", profile: AgentMemoryProfile{ScopeType: ProfileScopeSpace}, wantErr: true},
		{name: "unknown", profile: AgentMemoryProfile{ScopeType: "global"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.profile.ValidateScope()
			if test.wantErr != errors.Is(err, ErrInvalidScope) {
				t.Fatalf("ValidateScope error = %v, want invalid scope %t", err, test.wantErr)
			}
		})
	}
}

func TestMemoryBankScopeInvariants(t *testing.T) {
	tests := []struct {
		name    string
		bank    MemoryBank
		wantErr bool
	}{
		{name: "agent", bank: MemoryBank{ScopeType: BankScopeAgent, ScopeID: testAgentA}},
		{name: "space", bank: MemoryBank{ScopeType: BankScopeSpace, ScopeID: testSpaceA}},
		{name: "organization", bank: MemoryBank{ScopeType: BankScopeOrganization, ScopeID: "org-1"}},
		{name: "missing id", bank: MemoryBank{ScopeType: BankScopeSpace}, wantErr: true},
		{name: "unknown", bank: MemoryBank{ScopeType: "platform", ScopeID: "platform"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.bank.ValidateScope()
			if test.wantErr != errors.Is(err, ErrInvalidScope) {
				t.Fatalf("ValidateScope error = %v, want invalid scope %t", err, test.wantErr)
			}
		})
	}
}

func validSpaceProfile() AgentMemoryProfile {
	spaceID := testSpaceA
	return AgentMemoryProfile{
		AgentUID:        testAgentA,
		RobotID:         "robot-a",
		OwnerUID:        testOwnerA,
		ScopeType:       ProfileScopeSpace,
		SpaceID:         &spaceID,
		TrustTier:       "normal",
		PolicyVersionID: "policy-1",
		Status:          "disabled",
	}
}

func TestStoreReplaceProfileGrantsRollsBackOnInsertFailure(t *testing.T) {
	store, mock := newTestStore(t)
	grant := MemoryGrant{
		ID: "grant-1", PrincipalType: string(PrincipalTypeAgent), PrincipalID: testAgentA,
		BankID: "bank-agent-a", Capability: string(CapabilityRecall), Effect: string(GrantEffectAllow),
		IssuedByUID: testOwnerA, Reason: "replace", PolicyVersionID: "policy-1",
	}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM `memory_grant`").WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO `memory_grant`").WillReturnError(errors.New("insert failed"))
	mock.ExpectRollback()

	err := store.ReplaceProfileGrants(testAgentA, 2, []MemoryGrant{grant})
	if err == nil || errors.Is(err, ErrVersionConflict) {
		t.Fatalf("ReplaceProfileGrants error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreReplaceProfileGrantsReturnsVersionConflictBeforeDeleting(t *testing.T) {
	store, mock := newTestStore(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := store.ReplaceProfileGrants(testAgentA, 2, nil)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("ReplaceProfileGrants error = %v, want ErrVersionConflict", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
