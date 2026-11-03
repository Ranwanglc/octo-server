package agent_memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRobotAuthority struct {
	state AuthoritativeRobotState
	err   error
	want  string
}

func (authority *fakeRobotAuthority) LoadAuthoritativeRobot(_ context.Context, robotID string) (AuthoritativeRobotState, error) {
	if authority.want != "" && authority.want != robotID {
		return AuthoritativeRobotState{}, errors.New("unexpected app bot id")
	}
	return authority.state, authority.err
}

type fakeProfileStore struct {
	profile     *AgentMemoryProfile
	findErr     error
	createErr   error
	syncErr     error
	created     *AgentMemoryProfile
	synced      *AgentMemoryProfile
	syncVersion int64
	createCalls int
	syncCalls   int
}

func (store *fakeProfileStore) FindProfileByRobotID(string) (*AgentMemoryProfile, error) {
	if store.findErr != nil {
		return nil, store.findErr
	}
	if store.profile == nil {
		return nil, ErrProfileNotFound
	}
	profile := *store.profile
	profile.SpaceID = cloneStringPointer(store.profile.SpaceID)
	return &profile, nil
}

func (store *fakeProfileStore) FindProfileByAgentUID(string) (*AgentMemoryProfile, error) {
	return store.FindProfileByRobotID("")
}

func (store *fakeProfileStore) CreateProfile(profile *AgentMemoryProfile) error {
	store.createCalls++
	created := *profile
	created.SpaceID = cloneStringPointer(profile.SpaceID)
	store.created = &created
	return store.createErr
}

func (store *fakeProfileStore) SyncProfile(profile *AgentMemoryProfile, expectedVersion int64) error {
	store.syncCalls++
	store.syncVersion = expectedVersion
	synced := *profile
	synced.SpaceID = cloneStringPointer(profile.SpaceID)
	store.synced = &synced
	return store.syncErr
}

func TestProfileServiceCreatesPublishedProfileFromAuthoritativeRobot(t *testing.T) {
	spaceID := testSpaceA
	authority := &fakeRobotAuthority{
		want: "robot-a",
		state: AuthoritativeRobotState{
			RobotID:         "robot-a",
			AgentUID:        "robot-a",
			OwnerUID:        testOwnerA,
			ScopeType:       ProfileScopeSpace,
			SpaceID:         &spaceID,
			Lifecycle:       RobotLifecycleActive,
			CredentialEpoch: 7,
		},
	}
	store := &fakeProfileStore{}
	service := NewProfileService(authority, store)
	spoofedSpace := testSpaceB
	spoofedDisabledAt := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	profile, err := service.SynchronizeProfile(context.Background(), AgentMemoryProfile{
		RobotID:         "robot-a",
		AgentUID:        "caller-agent",
		OwnerUID:        "caller-owner",
		ScopeType:       ProfileScopePlatform,
		SpaceID:         &spoofedSpace,
		Enabled:         true,
		TrustTier:       "high",
		PolicyVersionID: "policy-1",
		CredentialEpoch: 999,
		Status:          "revoked",
		DisabledAt:      &spoofedDisabledAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProfileAuthority(t, profile, authority.state)
	if profile.TrustTier != DefaultProfileTrustTier {
		t.Fatalf("TrustTier = %q, want %q", profile.TrustTier, DefaultProfileTrustTier)
	}
	if !profile.Enabled || profile.Status != "active" || profile.DisabledAt != nil {
		t.Fatalf("published profile lifecycle = enabled:%t status:%q disabled_at:%v", profile.Enabled, profile.Status, profile.DisabledAt)
	}
	if store.createCalls != 1 || store.syncCalls != 0 {
		t.Fatalf("create calls = %d, sync calls = %d", store.createCalls, store.syncCalls)
	}
}

func TestProfileServiceDisablesUnpublishedRobot(t *testing.T) {
	now := time.Date(2026, 7, 23, 11, 30, 0, 0, time.UTC)
	state := authoritativePlatformState(RobotLifecycleDisabled, 3)
	store := &fakeProfileStore{profile: existingPlatformProfile(2)}
	service := NewProfileService(&fakeRobotAuthority{state: state}, store)
	service.now = func() time.Time { return now }

	profile, err := service.SynchronizeProfile(context.Background(), AgentMemoryProfile{
		RobotID:   state.RobotID,
		Enabled:   true,
		TrustTier: "low",
		Status:    "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Enabled || profile.Status != "disabled" || profile.DisabledAt == nil || !profile.DisabledAt.Equal(now) {
		t.Fatalf("unpublished profile lifecycle = enabled:%t status:%q disabled_at:%v", profile.Enabled, profile.Status, profile.DisabledAt)
	}
	if profile.TrustTier != "high" {
		t.Fatalf("TrustTier = %q, want existing server-owned high", profile.TrustTier)
	}
}

func TestProfileServiceRevokesDeletedRobot(t *testing.T) {
	now := time.Date(2026, 7, 23, 11, 31, 0, 0, time.UTC)
	state := authoritativePlatformState(RobotLifecycleRevoked, 4)
	store := &fakeProfileStore{profile: existingPlatformProfile(5)}
	service := NewProfileService(&fakeRobotAuthority{state: state}, store)
	service.now = func() time.Time { return now }

	profile, err := service.SynchronizeProfile(context.Background(), AgentMemoryProfile{
		RobotID: state.RobotID,
		Enabled: true,
		Status:  "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Enabled || profile.Status != "revoked" || profile.DisabledAt == nil || !profile.DisabledAt.Equal(now) {
		t.Fatalf("deleted profile lifecycle = enabled:%t status:%q disabled_at:%v", profile.Enabled, profile.Status, profile.DisabledAt)
	}
	if profile.CredentialEpoch != 4 {
		t.Fatalf("CredentialEpoch = %d, want 4", profile.CredentialEpoch)
	}
}

func TestProfileServiceSynchronizesRotatedCredentialEpoch(t *testing.T) {
	state := authoritativePlatformState(RobotLifecycleActive, 12)
	existing := existingPlatformProfile(8)
	existing.CredentialEpoch = 11
	store := &fakeProfileStore{profile: existing}
	service := NewProfileService(&fakeRobotAuthority{state: state}, store)

	profile, err := service.SynchronizeProfile(context.Background(), AgentMemoryProfile{
		RobotID: state.RobotID,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.CredentialEpoch != 12 || store.synced.CredentialEpoch != 12 {
		t.Fatalf("credential epochs = result:%d stored:%d, want 12", profile.CredentialEpoch, store.synced.CredentialEpoch)
	}
	if store.syncVersion != 8 || profile.Version != 9 {
		t.Fatalf("versions = expected:%d result:%d, want 8 and 9", store.syncVersion, profile.Version)
	}
}

func TestProfileServiceRejectsCredentialEpochRollback(t *testing.T) {
	state := authoritativePlatformState(RobotLifecycleActive, 10)
	existing := existingPlatformProfile(8)
	existing.CredentialEpoch = 11
	store := &fakeProfileStore{profile: existing}
	service := NewProfileService(&fakeRobotAuthority{state: state}, store)

	_, err := service.SynchronizeProfile(context.Background(), AgentMemoryProfile{
		RobotID: state.RobotID,
		Enabled: true,
	})
	if !errors.Is(err, ErrCredentialEpochRollback) {
		t.Fatalf("SynchronizeProfile error = %v, want ErrCredentialEpochRollback", err)
	}
	if store.syncCalls != 0 {
		t.Fatalf("sync calls = %d, want 0", store.syncCalls)
	}
}

func TestProfileServiceSynchronizesOwnerAndSpaceChanges(t *testing.T) {
	oldSpace := testSpaceA
	newSpace := testSpaceB
	existing := existingPlatformProfile(6)
	existing.OwnerUID = testOwnerA
	existing.ScopeType = ProfileScopeSpace
	existing.SpaceID = &oldSpace
	state := AuthoritativeRobotState{
		RobotID:         existing.RobotID,
		AgentUID:        existing.AgentUID,
		OwnerUID:        testOwnerB,
		ScopeType:       ProfileScopeSpace,
		SpaceID:         &newSpace,
		Lifecycle:       RobotLifecycleActive,
		CredentialEpoch: 10,
	}
	store := &fakeProfileStore{profile: existing}
	service := NewProfileService(&fakeRobotAuthority{state: state}, store)

	profile, err := service.SynchronizeProfile(context.Background(), AgentMemoryProfile{
		RobotID:   existing.RobotID,
		OwnerUID:  "caller-owner",
		ScopeType: ProfileScopePlatform,
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProfileAuthority(t, profile, state)
	assertProfileAuthority(t, store.synced, state)
	if store.syncVersion != 6 {
		t.Fatalf("sync version = %d, want 6", store.syncVersion)
	}
}

func TestProfileServiceRejectsMismatchedAuthorityIdentity(t *testing.T) {
	state := authoritativePlatformState(RobotLifecycleActive, 1)
	state.RobotID = "different-robot"
	store := &fakeProfileStore{}
	service := NewProfileService(&fakeRobotAuthority{state: state}, store)

	_, err := service.SynchronizeProfile(context.Background(), AgentMemoryProfile{RobotID: "robot-a"})
	if !errors.Is(err, ErrRobotAuthorityMismatch) {
		t.Fatalf("SynchronizeProfile error = %v, want ErrRobotAuthorityMismatch", err)
	}
	if store.createCalls != 0 || store.syncCalls != 0 {
		t.Fatalf("writes after mismatch = create:%d sync:%d", store.createCalls, store.syncCalls)
	}
}

func authoritativePlatformState(lifecycle RobotLifecycle, credentialEpoch int64) AuthoritativeRobotState {
	return AuthoritativeRobotState{
		RobotID:         "robot-a",
		AgentUID:        "robot-a",
		OwnerUID:        testOwnerA,
		ScopeType:       ProfileScopePlatform,
		Lifecycle:       lifecycle,
		CredentialEpoch: credentialEpoch,
	}
}

func existingPlatformProfile(version int64) *AgentMemoryProfile {
	return &AgentMemoryProfile{
		AgentUID:        "robot-a",
		RobotID:         "robot-a",
		OwnerUID:        testOwnerA,
		ScopeType:       ProfileScopePlatform,
		Enabled:         true,
		TrustTier:       "high",
		PolicyVersionID: "policy-existing",
		CredentialEpoch: 2,
		Version:         version,
		Status:          "active",
	}
}

func assertProfileAuthority(t *testing.T, profile *AgentMemoryProfile, state AuthoritativeRobotState) {
	t.Helper()
	if profile.RobotID != state.RobotID || profile.AgentUID != state.AgentUID || profile.OwnerUID != state.OwnerUID {
		t.Fatalf("identity = app_bot:%q agent:%q owner:%q, want %q %q %q", profile.RobotID, profile.AgentUID, profile.OwnerUID, state.RobotID, state.AgentUID, state.OwnerUID)
	}
	if profile.ScopeType != state.ScopeType {
		t.Fatalf("ScopeType = %q, want %q", profile.ScopeType, state.ScopeType)
	}
	if (profile.SpaceID == nil) != (state.SpaceID == nil) || profile.SpaceID != nil && *profile.SpaceID != *state.SpaceID {
		t.Fatalf("SpaceID = %v, want %v", profile.SpaceID, state.SpaceID)
	}
	if profile.CredentialEpoch != state.CredentialEpoch {
		t.Fatalf("CredentialEpoch = %d, want %d", profile.CredentialEpoch, state.CredentialEpoch)
	}
}

type fakeBankCatalog struct {
	banks map[string]MemoryBank
}

func (catalog *fakeBankCatalog) FindBankByID(bankID string) (*MemoryBank, error) {
	bank, ok := catalog.banks[bankID]
	if !ok {
		return nil, ErrBankNotFound
	}
	return &bank, nil
}

type fakeGrantStore struct {
	agentUID        string
	expectedVersion int64
	grants          []MemoryGrant
	err             error
}

func (store *fakeGrantStore) ReplaceProfileGrants(agentUID string, expectedVersion int64, grants []MemoryGrant) error {
	if store.err != nil {
		return store.err
	}
	store.agentUID = agentUID
	store.expectedVersion = expectedVersion
	store.grants = append([]MemoryGrant(nil), grants...)
	return nil
}

func TestGrantServiceReplacesScopedGrantsAndDenyOverridesAllow(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	spaceID := testSpaceA
	profile := AgentMemoryProfile{
		AgentUID: testAgentA, OwnerUID: testOwnerA, ScopeType: ProfileScopeSpace, SpaceID: &spaceID,
		Enabled: true, Status: "active", PolicyVersionID: "policy-1", Version: 4,
	}
	bank := MemoryBank{ID: "bank-agent-a", BankType: "private", ScopeType: BankScopeAgent, ScopeID: testAgentA, Status: "active"}
	store := &fakeGrantStore{}
	service := NewGrantService(&fakeBankCatalog{banks: map[string]MemoryBank{bank.ID: bank}}, store)
	service.now = func() time.Time { return now }
	ids := []string{"grant-allow", "grant-deny"}
	service.newID = func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	}

	grants, err := service.ReplaceProfileGrants(profile, testOwnerA, "incident containment", 4, []GrantInput{
		{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: bank.ID, Capability: CapabilityRecall, Effect: GrantEffectAllow},
		{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: bank.ID, Capability: CapabilityRecall, Effect: GrantEffectDeny},
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.agentUID != testAgentA || store.expectedVersion != 4 || len(store.grants) != 2 {
		t.Fatalf("stored replacement = uid:%q version:%d grants:%+v", store.agentUID, store.expectedVersion, store.grants)
	}
	decision := ResolveCapability(CapabilityRequest{
		Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: testAgentA}, Profile: profile, Bank: bank,
		Capability: CapabilityRecall, Grants: grants, Now: now,
	})
	if decision.Allowed || decision.ReasonCode != PolicyReasonForbidden {
		t.Fatalf("deny precedence decision = %+v", decision)
	}
}

func TestGrantServiceRejectsMalformedAndCrossScopeInputs(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	spaceID := testSpaceA
	profile := AgentMemoryProfile{AgentUID: testAgentA, OwnerUID: testOwnerA, ScopeType: ProfileScopeSpace, SpaceID: &spaceID, PolicyVersionID: "policy-1", Version: 1}
	catalog := &fakeBankCatalog{banks: map[string]MemoryBank{
		"bank-agent-a": {ID: "bank-agent-a", ScopeType: BankScopeAgent, ScopeID: testAgentA, Status: "active"},
		"bank-agent-b": {ID: "bank-agent-b", ScopeType: BankScopeAgent, ScopeID: testAgentB, Status: "active"},
		"logical-bank": {ID: "logical-bank", HindsightBankID: "hs-agent-a", ScopeType: BankScopeAgent, ScopeID: testAgentA, Status: "active"},
	}}
	tests := []struct {
		name   string
		reason string
		input  GrantInput
		want   error
	}{
		{name: "missing reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "bank-agent-a", Capability: CapabilityRecall, Effect: GrantEffectAllow}, want: ErrInvalidGrant},
		{name: "capability", reason: "reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "bank-agent-a", Capability: "delete", Effect: GrantEffectAllow}, want: ErrInvalidGrant},
		{name: "effect", reason: "reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "bank-agent-a", Capability: CapabilityRecall, Effect: "permit"}, want: ErrInvalidGrant},
		{name: "principal type", reason: "reason", input: GrantInput{PrincipalType: "client_owner", PrincipalID: testOwnerA, BankID: "bank-agent-a", Capability: CapabilityRecall, Effect: GrantEffectAllow}, want: ErrInvalidGrant},
		{name: "cross agent principal", reason: "reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentB, BankID: "bank-agent-a", Capability: CapabilityRecall, Effect: GrantEffectAllow}, want: ErrGrantScope},
		{name: "cross agent bank", reason: "reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "bank-agent-b", Capability: CapabilityRecall, Effect: GrantEffectAllow}, want: ErrGrantScope},
		{name: "raw hindsight id", reason: "reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "hs-agent-a", Capability: CapabilityRecall, Effect: GrantEffectAllow}, want: ErrGrantScope},
		{name: "invalid window", reason: "reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "bank-agent-a", Capability: CapabilityRecall, Effect: GrantEffectAllow, ValidFrom: timePtr(now.Add(time.Hour)), ValidUntil: timePtr(now.Add(time.Minute))}, want: ErrInvalidGrant},
		{name: "expired window", reason: "reason", input: GrantInput{PrincipalType: PrincipalTypeAgent, PrincipalID: testAgentA, BankID: "bank-agent-a", Capability: CapabilityRecall, Effect: GrantEffectAllow, ValidUntil: timePtr(now)}, want: ErrInvalidGrant},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeGrantStore{}
			service := NewGrantService(catalog, store)
			service.now = func() time.Time { return now }
			_, err := service.ReplaceProfileGrants(profile, "space-admin", test.reason, 1, []GrantInput{test.input})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if store.grants != nil {
				t.Fatalf("invalid replacement reached store: %+v", store.grants)
			}
		})
	}
}

func timePtr(value time.Time) *time.Time { return &value }
