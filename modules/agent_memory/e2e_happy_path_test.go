package agent_memory

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gocraft/dbr/v2"
	"github.com/gocraft/dbr/v2/dialect"
)

// T054 server leg of the synthetic end-to-end happy path.
//
// Proves the "Web configure Agent A -> server policy/profile/grant -> effective
// agent context" slice in process: given the profile, policy, Bank, and grant
// that the Web/3000 configuration writes for Agent alpha, the internal
// agent-context resolver returns exactly the isolated synthetic Bank the gateway
// then submits/recalls against. No real service, container, or credential.
//
// It shares the reserved e2e_* identifiers (newE2EFixture) with the gateway
// happy-path fixtures so a full local run uses one consistent synthetic set.

const e2eRobotToken = "e2e-synthetic-token-alpha-0000000000"

func e2eAlphaContextStore(fixture e2eFixture) *agentContextTestStore {
	alpha := fixture.Banks["alpha_private"]
	return &agentContextTestStore{
		profile: &AgentMemoryProfile{
			AgentUID: fixture.AgentAlpha, RobotID: fixture.AgentAlpha, OwnerUID: e2eOwnerAlpha,
			ScopeType: ProfileScopeSpace, SpaceID: ptr(fixture.SpaceAlpha), Enabled: true,
			TrustTier: "normal", Status: "active", PolicyVersionID: e2ePolicyVersion, CredentialEpoch: 42,
		},
		policy: &MemoryPolicyVersion{ID: e2ePolicyVersion, Status: "active", DocumentJSON: []byte(`{"sensitivity_ceiling":"restricted","capability_ceiling":["propose"]}`)},
		banks: []MemoryBank{{
			ID: alpha.ID, HindsightBankID: NullableString(alpha.HindsightBankID), BankType: "personal",
			ScopeType: BankScopeAgent, ScopeID: fixture.AgentAlpha, PolicyVersionID: e2ePolicyVersion,
			Status: "active", Sensitivity: "internal",
		}},
		grants: []MemoryGrant{{
			PrincipalType: string(PrincipalTypeAgent), PrincipalID: fixture.AgentAlpha, BankID: alpha.ID,
			Capability: string(CapabilityPropose), Effect: string(GrantEffectAllow), PolicyVersionID: e2ePolicyVersion,
		}},
	}
}

func ptr(s string) *string { return &s }

func expectE2ERobot(mock sqlmock.Sqlmock, fixture e2eFixture) {
	rows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"robot_id", "creator_uid", "space_id", "status", "credential_epoch"}).
			AddRow(fixture.AgentAlpha, e2eOwnerAlpha, fixture.SpaceAlpha, 1, 42)
	}
	for range 2 {
		mock.ExpectQuery("SELECT .*credential_epoch.*").WillReturnRows(rows())
	}
}

func TestE2EAgentAlphaConfigurationResolvesIsolatedContext(t *testing.T) {
	fixture := newE2EFixture()

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	session := (&dbr.Connection{DB: rawDB, Dialect: dialect.MySQL, EventReceiver: &dbr.NullEventReceiver{}}).NewSession(nil)
	service := NewAgentContextService(session, e2eAlphaContextStore(fixture))
	service.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	expectE2ERobot(mock, fixture)

	context, err := service.Resolve(agentContextRequest{BearerToken: e2eRobotToken, Operation: CapabilityPropose})
	if err != nil {
		t.Fatal(err)
	}
	if context.AgentUID != fixture.AgentAlpha || context.OwnerUID != e2eOwnerAlpha {
		t.Fatalf("identity = %+v", context)
	}
	if context.ScopeType != ProfileScopeSpace || context.SpaceID == nil || *context.SpaceID != fixture.SpaceAlpha {
		t.Fatalf("scope = %+v", context)
	}
	if context.PolicyVersionID != e2ePolicyVersion || context.TrustTier != "normal" || context.CredentialEpoch != 42 {
		t.Fatalf("policy/trust facts = %+v", context)
	}
	if context.Operation != CapabilityPropose || context.ExpiresAt.Sub(context.IssuedAt) != agentContextTTL {
		t.Fatalf("operation/lifetime = %+v", context)
	}
	alpha := fixture.Banks["alpha_private"]
	if len(context.AllowedBanks) != 1 || context.AllowedBanks[0].BankID != alpha.ID ||
		context.AllowedBanks[0].HindsightBankID != alpha.HindsightBankID {
		t.Fatalf("allowed banks = %+v, want only %q -> %q", context.AllowedBanks, alpha.ID, alpha.HindsightBankID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestE2EAgentAlphaCannotResolveBetaBank(t *testing.T) {
	fixture := newE2EFixture()

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	session := (&dbr.Connection{DB: rawDB, Dialect: dialect.MySQL, EventReceiver: &dbr.NullEventReceiver{}}).NewSession(nil)
	store := e2eAlphaContextStore(fixture)
	service := NewAgentContextService(session, store)
	service.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	// Only the first authoritative robot lookup runs: resolution is forbidden before the
	// revalidation query because the requested Bank is not in alpha's set.
	mock.ExpectQuery("SELECT .*credential_epoch.*").
		WillReturnRows(sqlmock.NewRows([]string{"robot_id", "creator_uid", "space_id", "status", "credential_epoch"}).
			AddRow(fixture.AgentAlpha, e2eOwnerAlpha, fixture.SpaceAlpha, 1, 42))

	// Requesting agent beta's opaque Bank is forbidden: the store never lists it
	// for alpha, so cross-Agent/Space reach is denied without leaking existence.
	beta := fixture.Banks["beta_private"]
	if _, err := service.Resolve(agentContextRequest{
		BearerToken: e2eRobotToken, Operation: CapabilityPropose, RequestedBankID: beta.ID,
	}); err == nil {
		t.Fatal("Resolve() allowed cross-agent bank")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
