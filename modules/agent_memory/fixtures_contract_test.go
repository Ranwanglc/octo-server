package agent_memory

import "testing"

func TestMemoryFixtureProvidesCrossSpaceNegativeCase(t *testing.T) {
	fixture := newTestMemoryFixture()
	if fixture.SpaceA == fixture.SpaceB || fixture.AgentA == fixture.AgentB {
		t.Fatal("fixture spaces and agents must be distinct")
	}

	denial := fixture.Grants["agent_a_cross_space_denied"]
	foreignBank := fixture.Banks["agent_b"]
	if denial.PrincipalID != fixture.AgentA || denial.BankID != foreignBank.ID {
		t.Fatalf("cross-space denial does not connect Agent A to Bank B: %#v", denial)
	}
	if denial.Effect != "deny" {
		t.Fatalf("cross-space fixture effect = %q, want deny", denial.Effect)
	}
	if foreignBank.ScopeID != fixture.AgentB {
		t.Fatalf("foreign bank scope = %q, want Agent B", foreignBank.ScopeID)
	}
}

func TestAgentMemorySQLMockHelper(t *testing.T) {
	db, mock := newAgentMemorySQLMock(t)
	mock.ExpectQuery("SELECT 1").WillReturnRows(mock.NewRows([]string{"value"}).AddRow(1))

	var value int
	if err := db.QueryRow("SELECT 1").Scan(&value); err != nil {
		t.Fatalf("query sqlmock: %v", err)
	}
	if value != 1 {
		t.Fatalf("value = %d, want 1", value)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
