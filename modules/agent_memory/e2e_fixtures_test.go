package agent_memory

import (
	"strings"
	"testing"
)

// Isolated end-to-end acceptance fixtures (T053, server side).
//
// These IDs share the reserved "e2e" namespace with the gateway acceptance
// fixtures so a full local run uses one consistent, non-overlapping set of
// synthetic Space/Agent/Bank identifiers. They never collide with the unit-test
// fixtures in fixtures_test.go or with legacy Hindsight Banks. No real data.
const (
	e2eSpaceAlpha = "space_e2e_alpha"
	e2eSpaceBeta  = "space_e2e_beta"
	e2eAgentAlpha = "agent_e2e_alpha"
	e2eAgentBeta  = "agent_e2e_beta"
	e2eOwnerAlpha = "owner_e2e_alpha"
	e2eOwnerBeta  = "owner_e2e_beta"

	e2ePolicyVersion = "mpv_e2e_0001"
)

type e2eMemoryBank struct {
	ID              string
	HindsightBankID string
	BankType        string
	ScopeType       string
	ScopeID         string
	OwnerUID        string
}

type e2eMemory struct {
	ResourceID string
	BankID     string
	State      string
	Content    string
}

type e2eFixture struct {
	SpaceAlpha string
	SpaceBeta  string
	AgentAlpha string
	AgentBeta  string
	Banks      map[string]e2eMemoryBank
	Memories   map[string]e2eMemory
}

func newE2EFixture() e2eFixture {
	alphaPrivate := e2eMemoryBank{
		ID:              "bank_e2e_alpha_private",
		HindsightBankID: "hs_e2e_alpha_private",
		BankType:        "private",
		ScopeType:       "agent",
		ScopeID:         e2eAgentAlpha,
		OwnerUID:        e2eOwnerAlpha,
	}
	alphaSpace := e2eMemoryBank{
		ID:              "bank_e2e_space_alpha_verified",
		HindsightBankID: "hs_e2e_space_alpha_verified",
		BankType:        "shared",
		ScopeType:       "space",
		ScopeID:         e2eSpaceAlpha,
		OwnerUID:        e2eOwnerAlpha,
	}
	betaPrivate := e2eMemoryBank{
		ID:              "bank_e2e_beta_private",
		HindsightBankID: "hs_e2e_beta_private",
		BankType:        "private",
		ScopeType:       "agent",
		ScopeID:         e2eAgentBeta,
		OwnerUID:        e2eOwnerBeta,
	}
	betaSpace := e2eMemoryBank{
		ID:              "bank_e2e_space_beta_verified",
		HindsightBankID: "hs_e2e_space_beta_verified",
		BankType:        "shared",
		ScopeType:       "space",
		ScopeID:         e2eSpaceBeta,
		OwnerUID:        e2eOwnerBeta,
	}
	return e2eFixture{
		SpaceAlpha: e2eSpaceAlpha,
		SpaceBeta:  e2eSpaceBeta,
		AgentAlpha: e2eAgentAlpha,
		AgentBeta:  e2eAgentBeta,
		Banks: map[string]e2eMemoryBank{
			"alpha_private":        alphaPrivate,
			"alpha_space_verified": alphaSpace,
			"beta_private":         betaPrivate,
			"beta_space_verified":  betaSpace,
		},
		Memories: map[string]e2eMemory{
			"alpha_private": {
				ResourceID: "m_e2e_alpha_private_1",
				BankID:     alphaPrivate.ID,
				State:      "tentative",
				Content:    "synthetic alpha private tentative note",
			},
			"alpha_space_verified": {
				ResourceID: "m_e2e_space_alpha_1",
				BankID:     alphaSpace.ID,
				State:      "verified",
				Content:    "synthetic space alpha verified fact",
			},
			"beta_private": {
				ResourceID: "m_e2e_beta_private_1",
				BankID:     betaPrivate.ID,
				State:      "tentative",
				Content:    "synthetic beta private tentative note",
			},
			"beta_space_verified": {
				ResourceID: "m_e2e_space_beta_1",
				BankID:     betaSpace.ID,
				State:      "verified",
				Content:    "synthetic space beta verified fact",
			},
		},
	}
}

// allIDs returns every synthetic identifier introduced by the E2E fixture, used
// to prove non-overlap with existing unit-test and legacy identifiers.
func (f e2eFixture) allIDs() []string {
	ids := []string{
		f.SpaceAlpha, f.SpaceBeta, f.AgentAlpha, f.AgentBeta,
		e2eOwnerAlpha, e2eOwnerBeta, e2ePolicyVersion,
	}
	for _, bank := range f.Banks {
		ids = append(ids, bank.ID, bank.HindsightBankID)
	}
	for _, mem := range f.Memories {
		ids = append(ids, mem.ResourceID)
	}
	return ids
}

func TestE2EFixtureIsolatedFromUnitFixtures(t *testing.T) {
	unit := newTestMemoryFixture()
	existing := map[string]struct{}{
		unit.SpaceA: {}, unit.SpaceB: {}, unit.AgentA: {}, unit.AgentB: {},
		testOwnerA: {}, testOwnerB: {},
	}
	for _, bank := range unit.Banks {
		existing[bank.ID] = struct{}{}
		existing[bank.HindsightBankID] = struct{}{}
	}

	fixture := newE2EFixture()
	for _, id := range fixture.allIDs() {
		if _, clash := existing[id]; clash {
			t.Fatalf("e2e id %q overlaps an existing unit-test id", id)
		}
	}
}

func TestE2EFixtureAvoidsLegacyBanks(t *testing.T) {
	fixture := newE2EFixture()
	legacy := map[string]struct{}{"ares": {}, "shared-craft": {}}
	for _, bank := range fixture.Banks {
		if _, bad := legacy[bank.HindsightBankID]; bad {
			t.Fatalf("e2e bank %q collides with a legacy Bank", bank.HindsightBankID)
		}
		if strings.HasPrefix(bank.HindsightBankID, "wg-") {
			t.Fatalf("e2e bank %q collides with an isolated wg-* Bank", bank.HindsightBankID)
		}
		if !strings.HasPrefix(bank.HindsightBankID, "hs_e2e_") {
			t.Fatalf("e2e bank %q left the reserved namespace", bank.HindsightBankID)
		}
	}
}

func TestE2EFixtureUniqueAndDisjointIdentities(t *testing.T) {
	fixture := newE2EFixture()
	seen := map[string]struct{}{}
	for _, id := range fixture.allIDs() {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate e2e id %q", id)
		}
		seen[id] = struct{}{}
	}

	alpha := map[string]struct{}{
		fixture.AgentAlpha: {}, fixture.SpaceAlpha: {}, e2eOwnerAlpha: {},
	}
	for _, id := range []string{fixture.AgentBeta, fixture.SpaceBeta, e2eOwnerBeta} {
		if _, clash := alpha[id]; clash {
			t.Fatalf("beta id %q overlaps alpha identity", id)
		}
	}
}

func TestE2EFixtureBankOwnershipConsistent(t *testing.T) {
	fixture := newE2EFixture()
	for key, mem := range fixture.Memories {
		bank, ok := fixture.Banks[key]
		if !ok {
			t.Fatalf("memory %q has no matching bank", key)
		}
		if mem.BankID != bank.ID {
			t.Fatalf("memory %q bank %q != expected %q", key, mem.BankID, bank.ID)
		}
		if bank.ScopeType == "agent" && mem.State != "tentative" {
			t.Fatalf("agent-scope memory %q expected tentative, got %q", key, mem.State)
		}
		if bank.ScopeType == "space" && mem.State != "verified" {
			t.Fatalf("space-scope memory %q expected verified, got %q", key, mem.State)
		}
	}
}
