package agent_memory

import (
	"testing"
	"time"
)

func TestResolveCapability(t *testing.T) {
	now := time.Date(2026, time.July, 23, 11, 14, 0, 0, time.UTC)
	spaceA := testSpaceA
	baseProfile := AgentMemoryProfile{
		PolicyVersionID: "policy-1",
		AgentUID:        testAgentA,
		ScopeType:       ProfileScopeSpace,
		SpaceID:         &spaceA,
		Enabled:         true,
		Status:          "active",
	}
	basePrincipal := PolicyPrincipal{
		Type:           PrincipalTypeAgent,
		ID:             testAgentA,
		OrganizationID: "org-agent-memory-a",
	}
	basePolicy := &MemoryPolicyVersion{ID: "policy-1", Status: "active", DocumentJSON: []byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["recall"]}`)}
	baseBank := MemoryBank{
		ID:          "bank-agent-memory",
		ScopeType:   BankScopeAgent,
		ScopeID:     testAgentA,
		Status:      "active",
		Sensitivity: "internal",
	}
	allow := MemoryGrant{
		ID:            "grant-allow",
		PrincipalType: string(PrincipalTypeAgent),
		PrincipalID:   testAgentA,
		BankID:        baseBank.ID,
		Capability:    string(CapabilityRecall),
		Effect:        string(GrantEffectAllow),
	}
	deny := allow
	deny.ID = "grant-deny"
	deny.Effect = string(GrantEffectDeny)

	tests := []struct {
		name       string
		mutate     func(*CapabilityRequest)
		wantAllow  bool
		wantReason PolicyReasonCode
	}{
		{
			name:       "agent scope allow",
			wantAllow:  true,
			wantReason: PolicyReasonAllowed,
		},
		{
			name: "space scope allow",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ScopeType = BankScopeSpace
				request.Bank.ScopeID = testSpaceA
			},
			wantAllow:  true,
			wantReason: PolicyReasonAllowed,
		},
		{
			name: "organization scope allow",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ScopeType = BankScopeOrganization
				request.Bank.ScopeID = request.Principal.OrganizationID
			},
			wantAllow:  true,
			wantReason: PolicyReasonAllowed,
		},
		{
			name: "cross organization denied",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ScopeType = BankScopeOrganization
				request.Bank.ScopeID = "org-agent-memory-b"
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "cross space denied despite allow",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ScopeType = BankScopeSpace
				request.Bank.ScopeID = testSpaceB
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "expired allow ignored",
			mutate: func(request *CapabilityRequest) {
				expired := now
				request.Grants[0].ValidUntil = &expired
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "future allow ignored",
			mutate: func(request *CapabilityRequest) {
				future := now.Add(time.Second)
				request.Grants[0].ValidFrom = &future
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "revoked allow ignored",
			mutate: func(request *CapabilityRequest) {
				revoked := now.Add(-time.Hour)
				request.Grants[0].RevokedAt = &revoked
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "inactive profile denied",
			mutate: func(request *CapabilityRequest) {
				request.Profile.Enabled = false
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "revoked profile denied",
			mutate: func(request *CapabilityRequest) {
				request.Profile.Status = "revoked"
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "inactive bank denied",
			mutate: func(request *CapabilityRequest) {
				request.Bank.Status = "disabled"
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "deny overrides following allow",
			mutate: func(request *CapabilityRequest) {
				request.Grants = []MemoryGrant{deny, allow}
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "deny overrides preceding allow",
			mutate: func(request *CapabilityRequest) {
				request.Grants = []MemoryGrant{allow, deny}
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "expired deny does not override live allow",
			mutate: func(request *CapabilityRequest) {
				expiredDeny := deny
				expired := now
				expiredDeny.ValidUntil = &expired
				request.Grants = []MemoryGrant{allow, expiredDeny}
			},
			wantAllow:  true,
			wantReason: PolicyReasonAllowed,
		},
		{
			name: "revoked deny does not override live allow",
			mutate: func(request *CapabilityRequest) {
				revokedDeny := deny
				revoked := now.Add(-time.Second)
				revokedDeny.RevokedAt = &revoked
				request.Grants = []MemoryGrant{allow, revokedDeny}
			},
			wantAllow:  true,
			wantReason: PolicyReasonAllowed,
		},
		{
			name: "unknown capability denied",
			mutate: func(request *CapabilityRequest) {
				request.Capability = Capability("delete")
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "unknown principal denied",
			mutate: func(request *CapabilityRequest) {
				request.Principal.Type = PrincipalType("service")
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "unknown profile scope denied",
			mutate: func(request *CapabilityRequest) {
				request.Profile.ScopeType = "organization"
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "unknown bank scope denied",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ScopeType = "project"
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "different agent principal denied",
			mutate: func(request *CapabilityRequest) {
				request.Principal.ID = testAgentB
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "bank display name cannot authorize",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ID = "opaque-bank-id-b"
				request.Bank.DisplayName = allow.BankID
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "hindsight bank id cannot authorize",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ID = "opaque-bank-id-b"
				request.Bank.HindsightBankID = NullableString(allow.BankID)
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "bank id prefix cannot authorize",
			mutate: func(request *CapabilityRequest) {
				request.Bank.ID = allow.BankID + "-child"
			},
			wantReason: PolicyReasonForbidden,
		},
		{
			name: "no matching allow denied",
			mutate: func(request *CapabilityRequest) {
				request.Grants = nil
			},
			wantReason: PolicyReasonForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := CapabilityRequest{
				Principal:  basePrincipal,
				Profile:    baseProfile,
				Bank:       baseBank,
				Capability: CapabilityRecall,
				Grants:     []MemoryGrant{allow},
				Policy:     basePolicy,
				Now:        now,
			}
			if test.mutate != nil {
				test.mutate(&request)
			}

			decision := ResolveCapability(request)
			if decision.Allowed != test.wantAllow {
				t.Fatalf("Allowed = %t, want %t", decision.Allowed, test.wantAllow)
			}
			if decision.ReasonCode != test.wantReason {
				t.Fatalf("ReasonCode = %q, want %q", decision.ReasonCode, test.wantReason)
			}
		})
	}
}

func TestEffectivePermissionExplanationParityWithRuntimeResolver(t *testing.T) {
	now := time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC)
	space := testSpaceA
	active := true
	base := CapabilityRequest{
		Principal:  PolicyPrincipal{Type: PrincipalTypeAgent, ID: testAgentA},
		Profile:    AgentMemoryProfile{AgentUID: testAgentA, OwnerUID: testOwnerA, ScopeType: ProfileScopeSpace, SpaceID: &space, Enabled: true, Status: "active", PolicyVersionID: "policy-1"},
		Bank:       MemoryBank{ID: "bank-1", ScopeType: BankScopeSpace, ScopeID: space, Status: BankStatusActive, PolicyVersionID: "policy-1"},
		Capability: CapabilityRecall,
		Policy:     &MemoryPolicyVersion{ID: "policy-1", Status: "active", DocumentJSON: []byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["recall"]}`)},
		Now:        now, RobotActive: &active,
	}
	base.Grants = []MemoryGrant{{PrincipalType: string(PrincipalTypeAgent), PrincipalID: testAgentA, BankID: "bank-1", Capability: "recall", Effect: "allow", PolicyVersionID: "policy-1"}}
	cases := []struct {
		name   string
		mutate func(*CapabilityRequest)
	}{
		{"allowed", func(*CapabilityRequest) {}},
		{"validity expired", func(r *CapabilityRequest) { expired := now; r.Grants[0].ValidUntil = &expired }},
		{"offer exact revision subset represented by fenced live grant", func(r *CapabilityRequest) {
			r.Grants[0].SourceKind = "offer_acceptance"
			r.Grants[0].GrantSetVersion = 3
		}},
		{"scope transfer", func(r *CapabilityRequest) { r.Bank.ScopeID = "other-space" }},
		{"owner profile disabled", func(r *CapabilityRequest) { r.Profile.Enabled = false }},
		{"robot inactive", func(r *CapabilityRequest) { inactive := false; r.RobotActive = &inactive }},
		{"bank disabled", func(r *CapabilityRequest) { r.Bank.Status = BankStatusDisabled }},
		{"explicit deny", func(r *CapabilityRequest) {
			deny := r.Grants[0]
			deny.Effect = "deny"
			r.Grants = append(r.Grants, deny)
		}},
		{"policy ceiling", func(r *CapabilityRequest) {
			r.Policy.DocumentJSON = []byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["propose"]}`)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.Grants = append([]MemoryGrant(nil), base.Grants...)
			policy := *base.Policy
			policy.DocumentJSON = append([]byte(nil), base.Policy.DocumentJSON...)
			request.Policy = &policy
			test.mutate(&request)
			runtimeDecision := ResolveCapability(request)
			management := explainDecision(request.Bank.ID, request.Capability, "ownership", ResolveCapability(request))
			if (management.State == "effective") != runtimeDecision.Allowed || (!runtimeDecision.Allowed && management.ReasonCode != runtimeDecision.Cause) {
				t.Fatalf("runtime=%+v management=%+v", runtimeDecision, management)
			}
		})
	}
}

func TestResolveCapabilityDenialsUseAntiEnumerationReason(t *testing.T) {
	now := time.Date(2026, time.July, 23, 11, 14, 0, 0, time.UTC)
	spaceID := testSpaceA
	base := CapabilityRequest{
		Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: testAgentA},
		Profile: AgentMemoryProfile{
			AgentUID:  testAgentA,
			ScopeType: ProfileScopeSpace,
			SpaceID:   &spaceID,
			Enabled:   true,
			Status:    "active",
		},
		Bank: MemoryBank{
			ID:        "bank-a",
			ScopeType: BankScopeAgent,
			ScopeID:   testAgentA,
			Status:    "active",
		},
		Capability: CapabilityRecall,
		Now:        now,
	}

	tests := []struct {
		name   string
		mutate func(*CapabilityRequest)
	}{
		{name: "missing grant"},
		{name: "unknown capability", mutate: func(request *CapabilityRequest) { request.Capability = "unknown" }},
		{name: "unknown principal", mutate: func(request *CapabilityRequest) { request.Principal.Type = "unknown" }},
		{name: "cross space", mutate: func(request *CapabilityRequest) {
			request.Bank.ScopeType = BankScopeSpace
			request.Bank.ScopeID = testSpaceB
		}},
		{name: "inactive bank", mutate: func(request *CapabilityRequest) { request.Bank.Status = "archived" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := base
			if test.mutate != nil {
				test.mutate(&request)
			}
			decision := ResolveCapability(request)
			if decision.Allowed || decision.ReasonCode != PolicyReasonForbidden {
				t.Fatalf("decision = %+v, want uniform forbidden denial", decision)
			}
		})
	}
}

func TestPolicyCapabilityCeilingDefaultsDeny(t *testing.T) {
	for _, document := range [][]byte{nil, []byte(`{}`), []byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["unknown"]}`), []byte(`not-json`)} {
		if policyAllows(document, CapabilityRecall) {
			t.Fatalf("document %q unexpectedly allowed recall", document)
		}
	}
	if !policyAllows([]byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["recall"]}`), CapabilityRecall) {
		t.Fatal("explicit recall ceiling denied")
	}
	if policyAllows([]byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["recall"]}`), CapabilityAdmin) {
		t.Fatal("capability above ceiling allowed")
	}
}
