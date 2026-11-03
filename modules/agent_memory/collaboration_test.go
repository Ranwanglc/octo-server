package agent_memory

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestOfferAcceptanceRequiresKnownSubsetForExactRevision(t *testing.T) {
	offered, err := normalizeCapabilities([]Capability{CapabilityPropose, CapabilityRecall})
	if err != nil {
		t.Fatal(err)
	}
	if !capabilitySubset([]Capability{CapabilityRecall}, offered) {
		t.Fatal("accepted subset was rejected")
	}
	if capabilitySubset([]Capability{CapabilityAdmin}, offered) {
		t.Fatal("acceptance exceeded offered capabilities")
	}
	if _, err := normalizeCapabilities([]Capability{CapabilityRecall, CapabilityRecall}); !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("duplicate capability error = %v", err)
	}
}

func TestBankAuthorityMatrixRejectsCrossScopeTypes(t *testing.T) {
	tests := []struct {
		scope    BankScopeType
		bankType string
		valid    bool
	}{
		{BankScopeAgent, "private", true},
		{BankScopeAgent, "tentative", true},
		{BankScopeAgent, "shared", false},
		{BankScopeSpace, "shared", true},
		{BankScopeSpace, "verified", true},
		{BankScopeSpace, "private", false},
		{BankScopeOrganization, "verified", true},
	}
	for _, test := range tests {
		request := createBankRequest{DisplayName: "Bank", BankType: test.bankType, ScopeType: test.scope, ScopeID: "scope-1", Sensitivity: "internal", RetentionClass: "standard"}
		if got := validBankCreate(request); got != test.valid {
			t.Errorf("scope=%s type=%s valid=%v, want %v", test.scope, test.bankType, got, test.valid)
		}
	}
}

func TestReadinessNeverTreatsEnabledWithoutProbeAsOperational(t *testing.T) {
	spaceID := "space-1"
	response := (*Runtime)(nil).profileResponse(AgentMemoryProfileRecord{AgentMemoryProfile: AgentMemoryProfile{RobotID: "robot-1", AgentUID: "robot-1", OwnerUID: "owner-1", ScopeType: ProfileScopeSpace, SpaceID: &spaceID, Enabled: true, Status: "active"}}, true)
	if response.Readiness != "blocked" || len(response.BlockerReasonCodes) != 1 || response.BlockerReasonCodes[0] != "FRESH_PROBE_REQUIRED" {
		t.Fatalf("readiness = %+v", response)
	}
}

func TestProfileReadinessRevalidatesCommittedProof(t *testing.T) {
	now := time.Date(2026, 7, 31, 3, 0, 0, 0, time.UTC)
	expires := now.Add(time.Minute)
	deps := map[string]interface{}{"profile": map[string]interface{}{"version": float64(2)}, "banks": map[string]interface{}{"bank-1": float64(1)}, "offers": map[string]interface{}{"offer-1": float64(3)}, "acceptances": map[string]interface{}{"accept-1": float64(4)}}
	raw, err := json.Marshal(deps)
	if err != nil {
		t.Fatal(err)
	}
	attempt := &EnablementAttempt{State: "committed", ProfileVersion: 2, CredentialEpoch: 5, AuthorizationEpoch: 7, PolicyVersionID: "policy-1", DependencyVersionsJSON: raw, ProbeExpiresAt: &expires}
	profile := &AgentMemoryProfile{Enabled: true, Status: "active", Version: 2, CredentialEpoch: 5, AuthorizationEpoch: 7, PolicyVersionID: "policy-1"}
	effective := []EffectivePermission{{BankID: "bank-1", Capability: CapabilityRecall, State: "effective"}}

	assert := func(name, wantReadiness, wantBlocker string, gotReadiness string, gotBlockers []string) {
		t.Helper()
		if gotReadiness != wantReadiness || (wantBlocker == "" && len(gotBlockers) != 0) || (wantBlocker != "" && (len(gotBlockers) != 1 || gotBlockers[0] != wantBlocker)) {
			t.Errorf("%s readiness=%q blockers=%v, want %q/%q", name, gotReadiness, gotBlockers, wantReadiness, wantBlocker)
		}
	}
	readiness, blockers := determineProfileReadiness(now, attempt, profile, deps, effective, nil)
	assert("enabled and exact", "operational", "", readiness, blockers)

	expired := *attempt
	past := now.Add(-time.Second)
	expired.ProbeExpiresAt = &past
	readiness, blockers = determineProfileReadiness(now, &expired, profile, deps, effective, nil)
	assert("probe expired", "blocked", "PROBE_EXPIRED", readiness, blockers)

	drift := map[string]interface{}{"profile": deps["profile"], "banks": map[string]interface{}{"bank-1": float64(2)}, "offers": deps["offers"], "acceptances": deps["acceptances"]}
	readiness, blockers = determineProfileReadiness(now, attempt, profile, drift, effective, nil)
	assert("bank disable or dependency drift", "blocked", "DEPENDENCY_DRIFT", readiness, blockers)

	readiness, blockers = determineProfileReadiness(now, attempt, profile, deps, []EffectivePermission{{State: "unavailable", ReasonCode: "BANK_UNAVAILABLE"}}, nil)
	assert("runtime revocation", "blocked", "RUNTIME_DENIED", readiness, blockers)

	readiness, blockers = determineProfileReadiness(now, attempt, profile, deps, effective, errors.New("policy resolver failed"))
	assert("policy drift", "blocked", "DEPENDENCY_DRIFT", readiness, blockers)
}
