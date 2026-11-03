package agent_memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type captureAuditStore struct {
	events []MemoryAuditEvent
	err    error
}

func (store *captureAuditStore) AppendAuditEvent(event MemoryAuditEvent) error {
	store.events = append(store.events, event)
	return store.err
}

func TestAuditServiceAcceptsOnlyStructuredRedactedFields(t *testing.T) {
	store := &captureAuditStore{}
	service := NewAuditService(store)
	service.newID = func() string { return "audit-1" }
	service.now = func() time.Time { return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC) }

	event, err := service.AuthDecision(AuditRecord{
		PrincipalAgentUID: testAgentA,
		SpaceID:           testSpaceA,
		ResourceID:        "bank-opaque-1",
		Capability:        CapabilityRecall,
		Decision:          AuditDecisionAllow,
		ReasonCode:        string(PolicyReasonAllowed),
		RequestID:         "request-1",
		CredentialJTIHash: strings.Repeat("a", 64),
		PolicyVersionID:   "policy-1",
		ContentHash:       strings.Repeat("b", 64),
		Metadata:          map[string]string{"operation": "authorize", "outcome": "allow"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Record(event); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 1 || string(store.events[0].MetadataJSON) != `{"operation":"authorize","outcome":"allow"}` {
		t.Fatalf("stored audit = %+v", store.events)
	}
	if strings.Contains(string(store.events[0].MetadataJSON), "memory content") {
		t.Fatalf("metadata retained content: %s", store.events[0].MetadataJSON)
	}
}

func TestAuditServiceRejectsTokensRawContentAndUnrestrictedMetadata(t *testing.T) {
	service := NewAuditService(&captureAuditStore{})
	base := AuditRecord{
		PrincipalUserUID: "admin-1", ResourceID: testAgentA, Decision: AuditDecisionSuccess,
		ReasonCode: "profile_updated", PolicyVersionID: "policy-1", Metadata: map[string]string{"operation": "update_profile"},
	}
	tests := []struct {
		name   string
		mutate func(*AuditRecord)
	}{
		{name: "bearer principal", mutate: func(record *AuditRecord) { record.PrincipalUserUID = "Bearer secret-token" }},
		{name: "jwt resource", mutate: func(record *AuditRecord) { record.ResourceID = "aaaaaaaa.bbbbbbbb.cccccccc" }},
		{name: "raw content metadata", mutate: func(record *AuditRecord) { record.Metadata = map[string]string{"snippet": "full candidate content"} }},
		{name: "arbitrary operation", mutate: func(record *AuditRecord) { record.Metadata = map[string]string{"operation": "dump_request_body"} }},
		{name: "oversized metadata", mutate: func(record *AuditRecord) {
			record.Metadata = map[string]string{"changed_fields": strings.Repeat("x", 121)}
		}},
		{name: "raw credential hash", mutate: func(record *AuditRecord) { record.CredentialJTIHash = "raw-jti" }},
		{name: "unknown reason", mutate: func(record *AuditRecord) { record.ReasonCode = "user supplied reason text" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := base
			test.mutate(&record)
			if _, err := service.ProfileDecision(record); !errors.Is(err, ErrInvalidAuditEvent) {
				t.Fatalf("error = %v, want ErrInvalidAuditEvent", err)
			}
		})
	}
}

func TestStoreAuditAppendIsInsertOnlyAndErrorsAreSanitized(t *testing.T) {
	store, mock := newTestStore(t)
	event := validAuditEvent()
	secret := "Bearer raw-secret-value"
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnError(errors.New(secret))

	err := store.AppendAuditEvent(event)
	if !errors.Is(err, ErrAuditPersistence) {
		t.Fatalf("error = %v, want ErrAuditPersistence", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreReplaceProfileGrantsRollsBackWhenAuditFails(t *testing.T) {
	store, mock := newTestStore(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM `memory_grant`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnError(errors.New("Bearer must-not-leak"))
	mock.ExpectRollback()

	err := store.ReplaceProfileGrantsWithAudit(testAgentA, 1, nil, validAuditEvent())
	if !errors.Is(err, ErrAuditPersistence) || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreSyncProfileRollsBackWhenAuditFails(t *testing.T) {
	store, mock := newTestStore(t)
	profile := validSpaceProfile()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agent_memory_profile` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `memory_audit_event`").WillReturnError(errors.New("raw credential"))
	mock.ExpectRollback()

	err := store.SyncProfileWithAudit(&profile, 0, validAuditEvent())
	if !errors.Is(err, ErrAuditPersistence) || strings.Contains(err.Error(), "raw credential") {
		t.Fatalf("error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func validAuditEvent() MemoryAuditEvent {
	return MemoryAuditEvent{
		ID: "audit-1", EventType: AuditEventProfileUpdate, PrincipalUserUID: "admin-1",
		ResourceType: "agent_profile", ResourceID: testAgentA, Decision: AuditDecisionSuccess,
		ReasonCode: "profile_updated", PolicyVersionID: "policy-1", CreatedAt: time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC),
	}
}

func TestManagementServicesFailClosedWithoutAtomicAuditStore(t *testing.T) {
	spaceID := testSpaceA
	profile := AgentMemoryProfile{
		AgentUID: testAgentA, RobotID: testAgentA, OwnerUID: testOwnerA,
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID, PolicyVersionID: "policy-1", Version: 1,
	}
	profileStore := &fakeProfileStore{profile: &profile}
	profileService := NewProfileService(&fakeRobotAuthority{state: AuthoritativeRobotState{
		RobotID: testAgentA, AgentUID: testAgentA, OwnerUID: testOwnerA,
		ScopeType: ProfileScopeSpace, SpaceID: &spaceID, Lifecycle: RobotLifecycleActive,
	}}, profileStore)
	if _, err := profileService.UpdateProfileWithAudit(context.Background(), profile, boolPtr(true), nil, 1, validAuditEvent()); !errors.Is(err, ErrAuditPersistence) {
		t.Fatalf("profile error = %v, want ErrAuditPersistence", err)
	}
	if profileStore.syncCalls != 0 {
		t.Fatalf("profile state changed without atomic audit support")
	}

	grantStore := &fakeGrantStore{}
	grantService := NewGrantService(&fakeBankCatalog{banks: map[string]MemoryBank{
		"bank-1": {ID: "bank-1", BankType: "private", ScopeType: BankScopeAgent, ScopeID: testAgentA, Status: "active"},
	}}, grantStore)
	if _, err := grantService.ReplaceProfileGrantsWithAudit(profile, testOwnerA, "security review", 1, []GrantInput{{
		PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "bank-1", Capability: CapabilityRecall, Effect: GrantEffectAllow,
	}}, validAuditEvent()); !errors.Is(err, ErrAuditPersistence) {
		t.Fatalf("grant error = %v, want ErrAuditPersistence", err)
	}
	if grantStore.grants != nil {
		t.Fatalf("grants changed without atomic audit support: %+v", grantStore.grants)
	}
}

func TestGrantServiceRejectsCredentialLikeAuditReason(t *testing.T) {
	profile := AgentMemoryProfile{AgentUID: testAgentA, PolicyVersionID: "policy-1", Version: 1}
	store := &fakeGrantStore{}
	service := NewGrantService(&fakeBankCatalog{}, store)
	_, err := service.ReplaceProfileGrants(profile, "admin-1", "Bearer raw-secret-token", 1, nil)
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("error = %v, want ErrInvalidGrant", err)
	}
	if store.grants != nil {
		t.Fatalf("credential-like audit reason reached store")
	}
}

func boolPtr(value bool) *bool { return &value }
