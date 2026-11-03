package agent_memory

import (
	"os"
	"strings"
	"testing"

	migrate "github.com/rubenv/sql-migrate"
)

func TestAgentMemoryMigrationHasCompleteUpAndDown(t *testing.T) {
	const migrationPath = "sql/20260723000001_agent_memory_control_plane.sql"
	file, err := os.Open(migrationPath)
	if err != nil {
		t.Fatalf("open migration: %v", err)
	}
	defer file.Close()

	migration, err := migrate.ParseMigration(migrationPath, file)
	if err != nil {
		t.Fatalf("parse migration: %v", err)
	}
	if len(migration.Up) != 11 {
		t.Fatalf("up statements = %d, want 11", len(migration.Up))
	}
	if len(migration.Down) != 11 {
		t.Fatalf("down statements = %d, want 11", len(migration.Down))
	}

	wantTables := []string{
		"agent_memory_profile",
		"memory_bank",
		"memory_bank_provisioning_intent",
		"memory_grant",
		"memory_policy_version",
		"knowledge_item",
		"knowledge_evidence",
		"knowledge_transition",
		"memory_audit_event",
		"governance_run",
		"legacy_bank_mapping",
	}
	body, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	text := string(body)
	for _, table := range wantTables {
		if !strings.Contains(text, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("migration missing CREATE for %s", table)
		}
		if !strings.Contains(text, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("migration missing DROP for %s", table)
		}
	}
	for _, versionedTable := range []string{"agent_memory_profile", "memory_bank", "knowledge_item"} {
		start := strings.Index(text, "CREATE TABLE IF NOT EXISTS "+versionedTable)
		if start < 0 {
			continue
		}
		end := strings.Index(text[start:], ";")
		if end < 0 || !strings.Contains(text[start:start+end], "version BIGINT NOT NULL DEFAULT 0") {
			t.Errorf("migration missing optimistic version for %s", versionedTable)
		}
	}
	if strings.Contains(strings.ToUpper(text), "ALTER TABLE APP_BOT") ||
		strings.Contains(strings.ToUpper(text), "DROP TABLE IF EXISTS APP_BOT") {
		t.Fatal("agent_memory migration must not alter or drop app_bot")
	}
	if !strings.Contains(text, "hindsight_bank_id VARCHAR(120) DEFAULT NULL") ||
		!strings.Contains(text, "status ENUM('provisioning', 'active', 'disabled', 'archived', 'migration_unmapped') NOT NULL DEFAULT 'provisioning'") {
		t.Fatal("memory_bank must permit a non-authorizing logical provisioning state without a physical id")
	}
	if !strings.Contains(text, "UNIQUE KEY uk_memory_bank_intent_canonical (canonical_key)") ||
		!strings.Contains(text, "UNIQUE KEY uk_memory_bank_intent_lifecycle (bank_id, operation, bank_version)") {
		t.Fatal("bank provisioning intents must be durably idempotent")
	}
}

func TestMemoryCollaborationMigrationIsReversibleAndPreservesLegacyRows(t *testing.T) {
	const migrationPath = "sql/20260730000003_memory_collaboration.sql"
	file, err := os.Open(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	migration, err := migrate.ParseMigration(migrationPath, file)
	if err != nil {
		t.Fatal(err)
	}
	if len(migration.Up) != 6 || len(migration.Down) != 6 {
		t.Fatalf("collaboration migration statements up=%d down=%d, want 6/6", len(migration.Up), len(migration.Down))
	}
	body, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, table := range []string{"memory_capability_offer", "memory_offer_acceptance", "memory_enablement_attempt"} {
		if !strings.Contains(text, "CREATE TABLE IF NOT EXISTS "+table) || !strings.Contains(text, "DROP TABLE IF EXISTS "+table) {
			t.Errorf("missing reversible table %s", table)
		}
	}
	if strings.Contains(text, "DROP TABLE IF EXISTS memory_grant") || strings.Contains(text, "DROP TABLE IF EXISTS memory_bank") {
		t.Fatal("collaboration migration must preserve legacy grants and Banks")
	}
	for _, field := range []string{"authorization_epoch", "source_kind", "source_offer_id", "source_acceptance_id", "grant_set_version", "created_by_uid"} {
		if !strings.Contains(text, field) {
			t.Errorf("missing collaboration field %s", field)
		}
	}
}

func TestRobotProfileAuthorityDownHandlesFreshAndLegacyPaths(t *testing.T) {
	body, err := os.ReadFile("sql/20260730000002_robot_profile_authority.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, fragment := range []string{"@agent_memory_profile_has_legacy", "@agent_memory_profile_drop_sql", "@agent_memory_profile_restore_sql", "'SELECT 1'"} {
		if !strings.Contains(text, fragment) {
			t.Errorf("missing safe Down branch %s", fragment)
		}
	}
	if strings.Contains(text, "DROP TABLE IF EXISTS agent_memory_profile;\nRENAME TABLE") {
		t.Fatal("fresh-schema Down must not unconditionally drop the profile")
	}
}

// This CI-portable schema contract is intentionally narrower than executing the
// migration on MySQL: it proves every robot/profile column referenced by the
// governance apply authority lock exists in the authoritative migrations. MySQL
// integration remains covered by the repository environment tests when available.
func TestGovernanceApplyAuthoritySQLUsesRealRobotProfileColumns(t *testing.T) {
	profileMigration, err := os.ReadFile("sql/20260730000002_robot_profile_authority.sql")
	if err != nil {
		t.Fatal(err)
	}
	robotBase, err := os.ReadFile("../robot/sql/20260226000002_robot_legacy01.sql")
	if err != nil {
		t.Fatal(err)
	}
	robotMemory, err := os.ReadFile("../robot/sql/20260730000001_robot_memory_authority.sql")
	if err != nil {
		t.Fatal(err)
	}
	profileColumns := migrationColumns(string(profileMigration), []string{"robot_id", "enabled", "policy_version_id", "version"})
	robotColumns := migrationColumns(string(robotBase)+"\n"+string(robotMemory), []string{"creator_uid", "space_id", "credential_epoch"})
	// robot_id/status are baseline columns predating this repository's additive migrations;
	// production structs and queries are the checked-in schema contract for them.
	robotDBSource, err := os.ReadFile("../robot/db.go")
	if err != nil {
		t.Fatal(err)
	}
	robotAPISource, err := os.ReadFile("../robot/api.go")
	if err != nil {
		t.Fatal(err)
	}
	robotColumns["robot_id"] = strings.Contains(string(robotDBSource), `db:"robot_id"`)
	robotColumns["status"] = strings.Contains(string(robotAPISource), `db:"status"`)
	governanceSource, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(governanceSource)
	for _, reference := range []struct{ alias, column string }{
		{"p", "robot_id"}, {"p", "enabled"}, {"r", "robot_id"}, {"r", "creator_uid"}, {"r", "status"},
	} {
		columns := profileColumns
		if reference.alias == "r" {
			columns = robotColumns
		}
		if !columns[reference.column] {
			t.Errorf("governance authority references missing %s.%s", reference.alias, reference.column)
		}
		if !strings.Contains(source, reference.alias+"."+reference.column) {
			t.Errorf("contract no longer observes expected locked authority column %s.%s", reference.alias, reference.column)
		}
	}
	if !strings.Contains(source, `.Suffix("FOR UPDATE")`) {
		t.Fatal("governance apply authority query must retain FOR UPDATE")
	}
}

func migrationColumns(sql string, names []string) map[string]bool {
	columns := make(map[string]bool, len(names))
	lower := strings.ToLower(sql)
	for _, name := range names {
		columns[name] = strings.Contains(lower, " "+name+" ") || strings.Contains(lower, "`"+name+"`")
	}
	return columns
}
