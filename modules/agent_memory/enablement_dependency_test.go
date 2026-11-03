package agent_memory

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestEnablementDependencyVersionsUsesAuthoritativeRobotSpaceAndCompleteFacts(t *testing.T) {
	store, mock := newTestStore(t)
	now := time.Date(2026, 7, 31, 4, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT .*COALESCE\\(space_id, ''\\).* FROM robot WHERE \\(robot_id='robot-a'\\)").
		WillReturnRows(sqlmock.NewRows([]string{"robot_id", "creator_uid", "space_id", "status", "credential_epoch"}).AddRow("robot-a", "owner-a", "space-authoritative", 1, 8))
	mock.ExpectQuery("SELECT p.version.*agent_memory_profile.*JOIN.*robot").
		WillReturnRows(sqlmock.NewRows([]string{"version", "policy_version_id", "credential_epoch", "authorization_epoch", "enabled"}).AddRow(3, "policy-1", 8, 11, false))
	mock.ExpectQuery("SELECT id, scope_type, scope_id, status, policy_version_id, version FROM memory_bank WHERE .*scope_id='robot-a'.*scope_id='space-authoritative'.*ORDER BY id ASC").
		WillReturnRows(sqlmock.NewRows([]string{"id", "scope_type", "scope_id", "status", "policy_version_id", "version"}).
			AddRow("bank-agent", "agent", "robot-a", "active", "policy-1", 2).
			AddRow("bank-space", "space", "space-authoritative", "disabled", "policy-1", 7))
	mock.ExpectQuery("SELECT id, revision, bank_id.* FROM memory_capability_offer").
		WillReturnRows(sqlmock.NewRows([]string{"id", "revision", "bank_id", "target_robot_id", "audience_space_id", "status", "policy_version_id", "offered_capabilities_json", "valid_from", "valid_until", "version"}).AddRow("offer-1", 2, "bank-space", "robot-a", "space-authoritative", "active", "policy-1", []byte(`["recall"]`), now, nil, 4))
	mock.ExpectQuery("SELECT id, offer_id.* FROM memory_offer_acceptance").
		WillReturnRows(sqlmock.NewRows([]string{"id", "offer_id", "offer_revision", "robot_id", "status", "accepted_by_owner_uid", "accepted_capabilities_json", "version"}).AddRow("accept-1", "offer-1", 2, "robot-a", "accepted", "owner-a", []byte(`["recall"]`), 5))
	mock.ExpectQuery("SELECT id, bank_id, capability.* FROM memory_grant").
		WillReturnRows(sqlmock.NewRows([]string{"id", "bank_id", "capability", "effect", "source_kind", "source_offer_id", "source_acceptance_id", "grant_set_version", "policy_version_id", "valid_from", "valid_until", "revoked_at"}).AddRow("grant-1", "bank-space", "recall", "allow", "offer_acceptance", "offer-1", "accept-1", 2, "policy-1", now, nil, nil))
	mock.ExpectQuery("SELECT id, version, document_hash, status FROM memory_policy_version").
		WillReturnRows(sqlmock.NewRows([]string{"id", "version", "document_hash", "status"}).AddRow("policy-1", "v1", "hash-1", "active"))

	got, err := store.EnablementDependencyVersions("robot-a")
	if err != nil {
		t.Fatal(err)
	}
	robot := got["robot"].(map[string]interface{})
	if robot["space_id"] != "space-authoritative" {
		t.Fatalf("robot snapshot = %#v", robot)
	}
	banks := got["banks"].(map[string]interface{})
	if len(banks) != 2 || banks["bank-space"].(map[string]interface{})["status"] != "disabled" {
		t.Fatalf("banks = %#v", banks)
	}
	if len(got["offers"].(map[string]interface{})) != 1 || len(got["acceptances"].(map[string]interface{})) != 1 || len(got["grants"].(map[string]interface{})) != 1 {
		t.Fatalf("incomplete dependency graph: %#v", got)
	}
	raw, _ := json.Marshal(got)
	var roundTrip map[string]interface{}
	if json.Unmarshal(raw, &roundTrip) != nil || !equalDependencyVersions(roundTrip, got) {
		t.Fatal("durable JSON snapshot did not compare equal")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdvanceCommittedProfileSnapshotRecordsEnabledTransition(t *testing.T) {
	raw := []byte(`{"profile":{"version":2,"enabled":false}}`)
	advanced, err := advanceCommittedProfileSnapshot(raw, 3)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(advanced, &got); err != nil {
		t.Fatal(err)
	}
	profile := got["profile"].(map[string]interface{})
	if profile["version"] != float64(3) || profile["enabled"] != true {
		t.Fatalf("profile = %#v", profile)
	}
}

func TestDependencySnapshotDetectsEveryReadinessAuthorityDrift(t *testing.T) {
	base := map[string]interface{}{
		"robot":       map[string]interface{}{"space_id": "space-a", "status": 1, "credential_epoch": 4},
		"profile":     map[string]interface{}{"version": 2, "enabled": false, "authorization_epoch": 7, "policy_version_id": "policy-1"},
		"banks":       map[string]interface{}{"bank-space": map[string]interface{}{"status": "active", "version": 3}},
		"offers":      map[string]interface{}{"offer-1:1": map[string]interface{}{"status": "active", "version": 1}},
		"acceptances": map[string]interface{}{"accept-1": map[string]interface{}{"status": "accepted", "version": 1}},
		"grants":      map[string]interface{}{"grant-1": map[string]interface{}{"revoked_at": nil, "grant_set_version": 1}},
		"policy":      map[string]interface{}{"id": "policy-1", "hash": "hash-1", "status": "active"},
	}
	clone := func() map[string]interface{} {
		raw, _ := json.Marshal(base)
		var got map[string]interface{}
		_ = json.Unmarshal(raw, &got)
		return got
	}
	cases := map[string]func(map[string]interface{}){
		"bank disable": func(v map[string]interface{}) {
			v["banks"].(map[string]interface{})["bank-space"].(map[string]interface{})["status"] = "disabled"
		},
		"bank version": func(v map[string]interface{}) {
			v["banks"].(map[string]interface{})["bank-space"].(map[string]interface{})["version"] = float64(4)
		},
		"space transfer": func(v map[string]interface{}) { v["robot"].(map[string]interface{})["space_id"] = "space-b" },
		"grant revoke": func(v map[string]interface{}) {
			v["grants"].(map[string]interface{})["grant-1"].(map[string]interface{})["revoked_at"] = "2026-07-31T04:00:00Z"
		},
		"offer revoke": func(v map[string]interface{}) {
			v["offers"].(map[string]interface{})["offer-1:1"].(map[string]interface{})["status"] = "revoked"
		},
		"acceptance withdraw": func(v map[string]interface{}) {
			v["acceptances"].(map[string]interface{})["accept-1"].(map[string]interface{})["status"] = "withdrawn"
		},
		"policy drift":     func(v map[string]interface{}) { v["policy"].(map[string]interface{})["hash"] = "hash-2" },
		"credential epoch": func(v map[string]interface{}) { v["robot"].(map[string]interface{})["credential_epoch"] = float64(5) },
		"authorization epoch": func(v map[string]interface{}) {
			v["profile"].(map[string]interface{})["authorization_epoch"] = float64(8)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			current := clone()
			mutate(current)
			if equalDependencyVersions(base, current) {
				t.Fatal("authority drift was not detected")
			}
		})
	}
}
