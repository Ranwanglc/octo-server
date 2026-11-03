package agent_memory

import (
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	testSpaceA = "space-agent-memory-a"
	testSpaceB = "space-agent-memory-b"
	testAgentA = "agent-memory-a"
	testAgentB = "agent-memory-b"
	testOwnerA = "owner-agent-memory-a"
	testOwnerB = "owner-agent-memory-b"
)

type testMemoryBank struct {
	ID              string
	HindsightBankID string
	ScopeType       string
	ScopeID         string
	OwnerUID        string
}

type testMemoryGrant struct {
	ID            string
	PrincipalType string
	PrincipalID   string
	BankID        string
	Capability    string
	Effect        string
}

type testMemoryFixture struct {
	SpaceA string
	SpaceB string
	AgentA string
	AgentB string
	Banks  map[string]testMemoryBank
	Grants map[string]testMemoryGrant
}

func newTestMemoryFixture() testMemoryFixture {
	bankA := testMemoryBank{
		ID:              "bank-agent-a-private",
		HindsightBankID: "hs-agent-a-private",
		ScopeType:       "agent",
		ScopeID:         testAgentA,
		OwnerUID:        testOwnerA,
	}
	bankB := testMemoryBank{
		ID:              "bank-agent-b-private",
		HindsightBankID: "hs-agent-b-private",
		ScopeType:       "agent",
		ScopeID:         testAgentB,
		OwnerUID:        testOwnerB,
	}
	spaceBankA := testMemoryBank{
		ID:              "bank-space-a-shared",
		HindsightBankID: "hs-space-a-shared",
		ScopeType:       "space",
		ScopeID:         testSpaceA,
		OwnerUID:        testOwnerA,
	}

	return testMemoryFixture{
		SpaceA: testSpaceA,
		SpaceB: testSpaceB,
		AgentA: testAgentA,
		AgentB: testAgentB,
		Banks: map[string]testMemoryBank{
			"agent_a": bankA,
			"agent_b": bankB,
			"space_a": spaceBankA,
		},
		Grants: map[string]testMemoryGrant{
			"agent_a_private_recall": {
				ID:            "grant-agent-a-private-recall",
				PrincipalType: "agent",
				PrincipalID:   testAgentA,
				BankID:        bankA.ID,
				Capability:    "recall",
				Effect:        "allow",
			},
			"agent_a_space_recall": {
				ID:            "grant-agent-a-space-recall",
				PrincipalType: "agent",
				PrincipalID:   testAgentA,
				BankID:        spaceBankA.ID,
				Capability:    "recall",
				Effect:        "allow",
			},
			"agent_a_cross_space_denied": {
				ID:            "grant-agent-a-cross-space-denied",
				PrincipalType: "agent",
				PrincipalID:   testAgentA,
				BankID:        bankB.ID,
				Capability:    "recall",
				Effect:        "deny",
			},
		},
	}
}

func newAgentMemorySQLMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}
