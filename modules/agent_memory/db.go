package agent_memory

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gocraft/dbr/v2"
)

var (
	ErrDuplicate               = errors.New("agent memory record already exists")
	ErrIdempotencyConflict     = errors.New("agent memory idempotency key already exists")
	ErrInvalidScope            = errors.New("invalid agent memory scope")
	ErrProfileNotFound         = errors.New("agent memory profile not found")
	ErrBankNotFound            = errors.New("agent memory bank not found")
	ErrBankIntentNotFound      = errors.New("agent memory bank intent not found")
	ErrPolicyNotFound          = errors.New("agent memory policy not found")
	ErrVersionConflict         = errors.New("agent memory version conflict")
	ErrGovernanceRunNotFound   = errors.New("agent memory governance run not found")
	ErrGovernanceNotReversible = errors.New("agent memory governance run not reversible")
)

type ProfileScopeType string

const (
	ProfileScopePlatform ProfileScopeType = "platform"
	ProfileScopeSpace    ProfileScopeType = "space"
)

type BankScopeType string

const (
	BankScopeAgent        BankScopeType = "agent"
	BankScopeSpace        BankScopeType = "space"
	BankScopeOrganization BankScopeType = "organization"
)

type NullableString string

func (value *NullableString) Scan(src interface{}) error {
	if src == nil {
		*value = ""
		return nil
	}
	switch typed := src.(type) {
	case string:
		*value = NullableString(typed)
	case []byte:
		*value = NullableString(typed)
	default:
		return fmt.Errorf("agent memory: cannot scan %T into nullable string", src)
	}
	return nil
}

func (value NullableString) Value() (driver.Value, error) {
	if value == "" {
		return nil, nil
	}
	return string(value), nil
}

type AgentMemoryProfile struct {
	RobotID             string           `db:"robot_id"`
	AgentUID            string           `db:"agent_uid"`
	OwnerUID            string           `db:"owner_uid"`
	ScopeType           ProfileScopeType `db:"scope_type"`
	SpaceID             *string          `db:"space_id"`
	Enabled             bool             `db:"enabled"`
	IncludeOwnTentative bool             `db:"include_own_tentative"`
	TrustTier           string           `db:"trust_tier"`
	PolicyVersionID     string           `db:"policy_version_id"`
	AuthorizationEpoch  int64            `db:"authorization_epoch"`
	CredentialEpoch     int64            `db:"credential_epoch"`
	Version             int64            `db:"version"`
	Status              string           `db:"status"`
	CreatedAt           time.Time        `db:"created_at"`
	UpdatedAt           time.Time        `db:"updated_at"`
	DisabledAt          *time.Time       `db:"disabled_at"`
}

type AgentMemoryProfileRecord struct {
	AgentMemoryProfile
	DisplayName string `db:"display_name"`
}

func (profile AgentMemoryProfile) ValidateScope() error {
	switch profile.ScopeType {
	case ProfileScopePlatform:
		if profile.SpaceID != nil && strings.TrimSpace(*profile.SpaceID) != "" {
			return fmt.Errorf("%w: platform profile must not have space_id", ErrInvalidScope)
		}
	case ProfileScopeSpace:
		if profile.SpaceID == nil || strings.TrimSpace(*profile.SpaceID) == "" {
			return fmt.Errorf("%w: space profile requires space_id", ErrInvalidScope)
		}
	default:
		return fmt.Errorf("%w: unsupported profile scope", ErrInvalidScope)
	}
	return nil
}

type MemoryBank struct {
	ID              string         `db:"id"`
	HindsightBankID NullableString `db:"hindsight_bank_id"`
	DisplayName     string         `db:"display_name"`
	BankType        string         `db:"bank_type"`
	ScopeType       BankScopeType  `db:"scope_type"`
	ScopeID         string         `db:"scope_id"`
	OwnerUID        string         `db:"owner_uid"`
	CreatedByUID    string         `db:"created_by_uid"`
	Sensitivity     string         `db:"sensitivity"`
	RetentionClass  string         `db:"retention_class"`
	PolicyVersionID string         `db:"policy_version_id"`
	Status          string         `db:"status"`
	Legacy          bool           `db:"legacy"`
	Version         int64          `db:"version"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	DisabledAt      *time.Time     `db:"disabled_at"`
}

type BankProvisioningIntent struct {
	ID           string     `db:"id"`
	BankID       string     `db:"bank_id"`
	Operation    string     `db:"operation"`
	CanonicalKey string     `db:"canonical_key"`
	BankVersion  int64      `db:"bank_version"`
	PayloadJSON  []byte     `db:"payload_json"`
	Status       string     `db:"status"`
	AttemptCount int64      `db:"attempt_count"`
	LastError    *string    `db:"last_error"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
	CompletedAt  *time.Time `db:"completed_at"`
}

func (bank MemoryBank) ValidateScope() error {
	if strings.TrimSpace(bank.ScopeID) == "" {
		return fmt.Errorf("%w: bank scope_id is required", ErrInvalidScope)
	}
	switch bank.ScopeType {
	case BankScopeAgent, BankScopeSpace, BankScopeOrganization:
		return nil
	default:
		return fmt.Errorf("%w: unsupported bank scope", ErrInvalidScope)
	}
}

type MemoryGrant struct {
	ID                 string     `db:"id"`
	PrincipalType      string     `db:"principal_type"`
	PrincipalID        string     `db:"principal_id"`
	BankID             string     `db:"bank_id"`
	Capability         string     `db:"capability"`
	Effect             string     `db:"effect"`
	SourceKind         string     `db:"source_kind"`
	SourceOfferID      *string    `db:"source_offer_id"`
	SourceAcceptanceID *string    `db:"source_acceptance_id"`
	GrantSetVersion    int64      `db:"grant_set_version"`
	ValidFrom          *time.Time `db:"valid_from"`
	ValidUntil         *time.Time `db:"valid_until"`
	IssuedByUID        string     `db:"issued_by_uid"`
	Reason             string     `db:"reason"`
	PolicyVersionID    string     `db:"policy_version_id"`
	CreatedAt          time.Time  `db:"created_at"`
	RevokedAt          *time.Time `db:"revoked_at"`
}

type MemoryPolicyVersion struct {
	ID           string     `db:"id"`
	Version      string     `db:"version"`
	Status       string     `db:"status"`
	DocumentJSON []byte     `db:"document_json"`
	DocumentHash string     `db:"document_hash"`
	CreatedByUID string     `db:"created_by_uid"`
	CreatedAt    time.Time  `db:"created_at"`
	ActivatedAt  *time.Time `db:"activated_at"`
}

type GovernanceRun struct {
	ID                     string     `db:"id"`
	Mode                   string     `db:"mode"`
	ScopeType              string     `db:"scope_type"`
	ScopeID                string     `db:"scope_id"`
	PolicyVersionID        string     `db:"policy_version_id"`
	Status                 string     `db:"status"`
	IdempotencyKey         string     `db:"idempotency_key"`
	ProposedCount          int64      `db:"proposed_count"`
	AppliedCount           int64      `db:"applied_count"`
	FailedCount            int64      `db:"failed_count"`
	StartedAt              *time.Time `db:"started_at"`
	FinishedAt             *time.Time `db:"finished_at"`
	CreatedByUID           string     `db:"created_by_uid"`
	RollbackIdempotencyKey *string    `db:"rollback_idempotency_key"`
	Version                int64      `db:"version"`
}

type KnowledgeItem struct {
	ID                          string     `db:"id"`
	BankID                      string     `db:"bank_id"`
	HindsightMemoryID           *string    `db:"hindsight_memory_id"`
	IdempotencyKey              string     `db:"idempotency_key"`
	CanonicalKey                string     `db:"canonical_key"`
	State                       string     `db:"state"`
	PersistenceStatus           string     `db:"persistence_status"`
	LifecycleState              string     `db:"lifecycle_state"`
	FactType                    string     `db:"fact_type"`
	AuthenticatedSourceAgentUID string     `db:"authenticated_source_agent_uid"`
	OwnerUID                    string     `db:"owner_uid"`
	SpaceID                     *string    `db:"space_id"`
	CredentialEpoch             int64      `db:"credential_epoch"`
	SourceInstanceID            *string    `db:"source_instance_id"`
	AssertionKind               string     `db:"assertion_kind"`
	OriginFingerprint           string     `db:"origin_fingerprint"`
	Confidence                  float64    `db:"confidence"`
	Sensitivity                 string     `db:"sensitivity"`
	ValidFrom                   *time.Time `db:"valid_from"`
	ValidUntil                  *time.Time `db:"valid_until"`
	ReviewAfter                 *time.Time `db:"review_after"`
	PolicyVersionID             string     `db:"policy_version_id"`
	GateRunID                   *string    `db:"gate_run_id"`
	ContentHash                 string     `db:"content_hash"`
	FailureReasonCode           string     `db:"failure_reason_code"`
	Version                     int64      `db:"version"`
	CreatedAt                   time.Time  `db:"created_at"`
	UpdatedAt                   time.Time  `db:"updated_at"`
}

type KnowledgeEvidence struct {
	ID                string    `db:"id"`
	KnowledgeItemID   string    `db:"knowledge_item_id"`
	EvidenceItemID    *string   `db:"evidence_item_id"`
	SourceAgentUID    string    `db:"source_agent_uid"`
	OriginFingerprint string    `db:"origin_fingerprint"`
	Relation          string    `db:"relation"`
	Independent       bool      `db:"independent"`
	CreatedAt         time.Time `db:"created_at"`
}

type KnowledgeTransition struct {
	ID              string    `db:"id"`
	KnowledgeItemID string    `db:"knowledge_item_id"`
	FromState       string    `db:"from_state"`
	ToState         string    `db:"to_state"`
	ActorType       string    `db:"actor_type"`
	ActorID         string    `db:"actor_id"`
	ReasonCode      string    `db:"reason_code"`
	ReasonRedacted  string    `db:"reason_redacted"`
	PolicyVersionID string    `db:"policy_version_id"`
	GovernanceRunID *string   `db:"governance_run_id"`
	RollbackOf      *string   `db:"rollback_of"`
	IdempotencyKey  string    `db:"idempotency_key"`
	CreatedAt       time.Time `db:"created_at"`
}

type LegacyBankMapping struct {
	HindsightBankID string     `db:"hindsight_bank_id"`
	BankID          *string    `db:"bank_id"`
	Classification  string     `db:"classification"`
	OwnerAgentUID   *string    `db:"owner_agent_uid"`
	MemoryCount     int64      `db:"memory_count"`
	InventoryHash   string     `db:"inventory_hash"`
	Status          string     `db:"status"`
	InventoriedAt   time.Time  `db:"inventoried_at"`
	ReconciledAt    *time.Time `db:"reconciled_at"`
}

type Store struct {
	session *dbr.Session
}

func NewStore(session *dbr.Session) *Store {
	return &Store{session: session}
}

func (store *Store) WithTx(fn func(*dbr.Tx) error) error {
	tx, err := store.session.Begin()
	if err != nil {
		return fmt.Errorf("agent memory: begin transaction: %w", err)
	}
	defer tx.RollbackUnlessCommitted()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("agent memory: commit transaction: %w", err)
	}
	return nil
}

func (store *Store) CreateProfile(profile *AgentMemoryProfile) error {
	return store.createProfile(store.session, profile)
}

func (store *Store) CreateProfileWithAudit(profile *AgentMemoryProfile, event MemoryAuditEvent) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		if err := store.createProfile(tx, profile); err != nil {
			return err
		}
		if err := appendAuditEvent(tx, event); err != nil {
			return ErrAuditPersistence
		}
		return nil
	})
}

func (store *Store) createProfile(exec auditInserter, profile *AgentMemoryProfile) error {
	if err := profile.ValidateScope(); err != nil {
		return err
	}
	_, err := exec.InsertInto("agent_memory_profile").Columns(
		"robot_id", "enabled", "include_own_tentative", "trust_tier", "policy_version_id", "authorization_epoch", "version", "disabled_at",
	).Values(
		profile.RobotID, profile.Enabled, profile.IncludeOwnTentative, profile.TrustTier, profile.PolicyVersionID, profile.AuthorizationEpoch, profile.Version, profile.DisabledAt,
	).Exec()
	return classifyInsertError(err, "")
}

func (store *Store) FindProfileByRobotID(robotID string) (*AgentMemoryProfile, error) {
	return store.findProfile(robotID)
}

func (store *Store) FindProfileByAgentUID(agentUID string) (*AgentMemoryProfile, error) {
	return store.findProfile(agentUID)
}

func (store *Store) findProfile(robotID string) (*AgentMemoryProfile, error) {
	var profile AgentMemoryProfile
	err := store.session.Select(
		"p.*", "r.robot_id AS agent_uid", "r.creator_uid AS owner_uid", "'space' AS scope_type",
		"NULLIF(r.space_id, '') AS space_id", "r.credential_epoch",
		"CASE WHEN r.status=1 AND r.space_id IS NOT NULL AND r.space_id<>'' AND p.enabled=1 THEN 'active' WHEN r.status=1 THEN 'disabled' ELSE 'revoked' END AS status",
	).From(dbr.I("agent_memory_profile").As("p")).
		Join(dbr.I("robot").As("r"), "r.robot_id=p.robot_id").Where("p.robot_id=?", robotID).LoadOne(&profile)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (store *Store) FindBankByID(bankID string) (*MemoryBank, error) {
	var bank MemoryBank
	err := store.session.Select("*").From("memory_bank").Where("id=?", bankID).LoadOne(&bank)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrBankNotFound
	}
	if err != nil {
		return nil, err
	}
	return &bank, nil
}

func (store *Store) ListBanks() ([]MemoryBank, error) {
	var banks []MemoryBank
	_, err := store.session.Select("*").From("memory_bank").Load(&banks)
	return banks, err
}

func (store *Store) ListAgentGrants(agentUID string) ([]MemoryGrant, error) {
	var grants []MemoryGrant
	_, err := store.session.Select("*").From("memory_grant").
		Where("principal_type=? AND principal_id=?", PrincipalTypeAgent, agentUID).
		Where("source_kind<>'offer_acceptance' OR EXISTS (SELECT 1 FROM memory_offer_acceptance a JOIN memory_capability_offer o ON o.id=a.offer_id AND o.revision=a.offer_revision JOIN robot r ON r.robot_id=a.robot_id JOIN memory_bank b ON b.id=o.bank_id WHERE a.id=memory_grant.source_acceptance_id AND a.status='accepted' AND o.status='active' AND r.status=1 AND r.creator_uid=a.accepted_by_owner_uid AND r.space_id=o.audience_space_id AND b.status='active' AND b.scope_type='space' AND b.scope_id=r.space_id)").Load(&grants)
	return grants, err
}

func (store *Store) FindPolicyVersionByID(policyVersionID string) (*MemoryPolicyVersion, error) {
	var policy MemoryPolicyVersion
	err := store.session.Select("*").From("memory_policy_version").Where("id=?", policyVersionID).LoadOne(&policy)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrPolicyNotFound
	}
	if err != nil {
		return nil, err
	}
	return &policy, nil
}

func (store *Store) ReplaceProfileGrants(agentUID string, expectedVersion int64, grants []MemoryGrant) error {
	return store.replaceProfileGrants(agentUID, expectedVersion, grants, nil)
}

func (store *Store) ReplaceProfileGrantsWithAudit(agentUID string, expectedVersion int64, grants []MemoryGrant, event MemoryAuditEvent) error {
	return store.replaceProfileGrants(agentUID, expectedVersion, grants, &event)
}

func (store *Store) replaceProfileGrants(agentUID string, expectedVersion int64, grants []MemoryGrant, event *MemoryAuditEvent) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		result, err := tx.Update("agent_memory_profile").
			Set("version", dbr.Expr("version+1")).
			Set("authorization_epoch", dbr.Expr("authorization_epoch+1")).
			Set("updated_at", dbr.Expr("CURRENT_TIMESTAMP")).
			Where("robot_id=? AND version=?", agentUID, expectedVersion).
			Exec()
		if err != nil {
			return err
		}
		if err := requireUpdated(result); err != nil {
			return err
		}

		if _, err := tx.DeleteFrom("memory_grant").Where("principal_type=? AND principal_id=? AND source_kind IN ('ownership','explicit_deny')", PrincipalTypeAgent, agentUID).Exec(); err != nil {
			return err
		}
		for _, grant := range grants {
			_, err := tx.InsertInto("memory_grant").Columns(
				"id", "principal_type", "principal_id", "bank_id", "capability", "effect",
				"valid_from", "valid_until", "issued_by_uid", "reason", "policy_version_id", "source_kind", "grant_set_version",
			).Values(
				grant.ID, grant.PrincipalType, grant.PrincipalID, grant.BankID, grant.Capability, grant.Effect,
				grant.ValidFrom, grant.ValidUntil, grant.IssuedByUID, grant.Reason, grant.PolicyVersionID, grant.SourceKind, expectedVersion+1,
			).Exec()
			if err != nil {
				return err
			}
		}
		if event != nil {
			if err := appendAuditEvent(tx, *event); err != nil {
				return ErrAuditPersistence
			}
		}
		return nil
	})
}

func (store *Store) AppendAuditEvent(event MemoryAuditEvent) error {
	if err := appendAuditEvent(store.session, event); err != nil {
		return ErrAuditPersistence
	}
	return nil
}

type auditInserter interface {
	InsertInto(string) *dbr.InsertStmt
}

func appendAuditEvent(exec auditInserter, event MemoryAuditEvent) error {
	if err := event.Validate(); err != nil {
		return err
	}
	_, err := exec.InsertInto("memory_audit_event").Columns(
		"id", "event_type", "principal_agent_uid", "principal_user_uid", "space_id",
		"resource_type", "resource_id", "capability", "decision", "reason_code", "request_id",
		"credential_jti_hash", "policy_version_id", "content_hash", "metadata_json", "created_at",
	).Values(
		event.ID, event.EventType, nullAuditValue(event.PrincipalAgentUID), nullAuditValue(event.PrincipalUserUID), nullAuditValue(event.SpaceID),
		event.ResourceType, event.ResourceID, nullAuditValue(string(event.Capability)), event.Decision, event.ReasonCode, nullAuditValue(event.RequestID),
		nullAuditValue(event.CredentialJTIHash), nullAuditValue(event.PolicyVersionID), nullAuditValue(event.ContentHash), nullAuditJSON(event.MetadataJSON), event.CreatedAt,
	).Exec()
	return err
}

func nullAuditValue(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func nullAuditJSON(value []byte) interface{} {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func (store *Store) FindProfileRecord(agentUID string) (*AgentMemoryProfileRecord, error) {
	var record AgentMemoryProfileRecord
	err := store.profileRecordQuery().Where("r.robot_id=?", agentUID).LoadOne(&record)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (store *Store) ListProfileRecords(scopeType ProfileScopeType, spaceID string) ([]AgentMemoryProfileRecord, error) {
	if scopeType != ProfileScopeSpace || strings.TrimSpace(spaceID) == "" {
		return nil, nil
	}
	var records []AgentMemoryProfileRecord
	_, err := store.profileRecordQuery().Where("r.space_id=?", spaceID).OrderAsc("u.name").OrderAsc("r.robot_id").Load(&records)
	return records, err
}

func (store *Store) profileRecordQuery() *dbr.SelectStmt {
	return store.session.Select(
		"r.robot_id", "r.robot_id AS agent_uid", "COALESCE(u.name, r.username, r.robot_id) AS display_name",
		"r.creator_uid AS owner_uid", "'space' AS scope_type", "NULLIF(r.space_id, '') AS space_id",
		"COALESCE(p.enabled, 0) AS enabled", "COALESCE(p.include_own_tentative, 0) AS include_own_tentative",
		"COALESCE(p.trust_tier, 'normal') AS trust_tier",
		"COALESCE(p.policy_version_id, (SELECT id FROM memory_policy_version WHERE status='active' ORDER BY activated_at DESC, created_at DESC LIMIT 1)) AS policy_version_id",
		"COALESCE(p.authorization_epoch, 0) AS authorization_epoch", "r.credential_epoch", "COALESCE(p.version, 0) AS version",
		"CASE WHEN r.status=1 AND r.space_id IS NOT NULL AND r.space_id<>'' AND COALESCE(p.enabled,0)=1 THEN 'active' WHEN r.status=1 THEN 'disabled' ELSE 'revoked' END AS status",
		"COALESCE(p.created_at, CURRENT_TIMESTAMP) AS created_at",
		"COALESCE(p.updated_at, CURRENT_TIMESTAMP) AS updated_at", "p.disabled_at",
	).From(dbr.I("robot").As("r")).
		LeftJoin(dbr.I("user").As("u"), "u.uid=r.robot_id").
		LeftJoin(dbr.I("agent_memory_profile").As("p"), "p.robot_id=r.robot_id")
}

func (store *Store) SyncProfile(profile *AgentMemoryProfile, expectedVersion int64) error {
	return store.syncProfile(store.session, profile, expectedVersion)
}

func (store *Store) SyncProfileWithAudit(profile *AgentMemoryProfile, expectedVersion int64, event MemoryAuditEvent) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		if err := store.syncProfile(tx, profile, expectedVersion); err != nil {
			return err
		}
		if err := appendAuditEvent(tx, event); err != nil {
			return ErrAuditPersistence
		}
		return nil
	})
}

type profileUpdater interface {
	Update(string) *dbr.UpdateStmt
}

func (store *Store) syncProfile(exec profileUpdater, profile *AgentMemoryProfile, expectedVersion int64) error {
	if err := profile.ValidateScope(); err != nil {
		return err
	}
	result, err := exec.Update("agent_memory_profile").SetMap(map[string]interface{}{
		"enabled": profile.Enabled, "include_own_tentative": profile.IncludeOwnTentative,
		"trust_tier": profile.TrustTier, "policy_version_id": profile.PolicyVersionID,
		"disabled_at": profile.DisabledAt, "version": dbr.Expr("version + 1"), "authorization_epoch": dbr.Expr("authorization_epoch + 1"),
	}).Where("robot_id=? AND version=?", profile.RobotID, expectedVersion).Exec()
	if err != nil {
		return err
	}
	return requireUpdated(result)
}

func (store *Store) UpdateProfile(profile *AgentMemoryProfile, expectedVersion int64) error {
	if err := profile.ValidateScope(); err != nil {
		return err
	}
	result, err := store.session.Update("agent_memory_profile").SetMap(map[string]interface{}{
		"enabled": profile.Enabled, "trust_tier": profile.TrustTier,
		"policy_version_id": profile.PolicyVersionID, "disabled_at": profile.DisabledAt,
		"version": dbr.Expr("version + 1"), "authorization_epoch": dbr.Expr("authorization_epoch + 1"),
	}).Where("robot_id=? AND version=?", profile.RobotID, expectedVersion).Exec()
	if err != nil {
		return err
	}
	return requireUpdated(result)
}

func (store *Store) CreateBank(bank *MemoryBank) error {
	if err := bank.ValidateScope(); err != nil {
		return err
	}
	_, err := store.session.InsertInto("memory_bank").Columns(
		"id", "hindsight_bank_id", "display_name", "bank_type", "scope_type", "scope_id", "owner_uid", "created_by_uid",
		"sensitivity", "retention_class", "policy_version_id", "status", "legacy", "version", "disabled_at",
	).Values(
		bank.ID, bank.HindsightBankID, bank.DisplayName, bank.BankType, bank.ScopeType, bank.ScopeID, bank.OwnerUID, bank.CreatedByUID,
		bank.Sensitivity, bank.RetentionClass, bank.PolicyVersionID, bank.Status, bank.Legacy, bank.Version, bank.DisabledAt,
	).Exec()
	return classifyInsertError(err, "")
}

func (store *Store) FindBankProvisioningIntent(canonicalKey string) (*BankProvisioningIntent, error) {
	var intent BankProvisioningIntent
	err := store.session.Select("*").From("memory_bank_provisioning_intent").Where("canonical_key=?", canonicalKey).LoadOne(&intent)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrBankIntentNotFound
	}
	if err != nil {
		return nil, err
	}
	return &intent, nil
}

func (store *Store) UpdateBank(bank *MemoryBank, expectedVersion int64) error {
	if err := bank.ValidateScope(); err != nil {
		return err
	}
	result, err := store.session.Update("memory_bank").SetMap(map[string]interface{}{
		"display_name":      bank.DisplayName,
		"bank_type":         bank.BankType,
		"sensitivity":       bank.Sensitivity,
		"retention_class":   bank.RetentionClass,
		"policy_version_id": bank.PolicyVersionID,
		"status":            bank.Status,
		"disabled_at":       bank.DisabledAt,
		"version":           dbr.Expr("version + 1"),
	}).Where("id=? AND scope_type=? AND scope_id=? AND version=?", bank.ID, bank.ScopeType, bank.ScopeID, expectedVersion).Exec()
	if err != nil {
		return err
	}
	return requireUpdated(result)
}

func (store *Store) CreateKnowledgeItem(item *KnowledgeItem) error {
	_, err := store.session.InsertInto("knowledge_item").Columns(
		"id", "bank_id", "hindsight_memory_id", "canonical_key", "state", "fact_type",
		"authenticated_source_agent_uid", "source_instance_id", "assertion_kind", "confidence", "sensitivity",
		"valid_from", "valid_until", "review_after", "policy_version_id", "gate_run_id", "content_hash", "version",
	).Values(
		item.ID, item.BankID, item.HindsightMemoryID, item.CanonicalKey, item.State, item.FactType,
		item.AuthenticatedSourceAgentUID, item.SourceInstanceID, item.AssertionKind, item.Confidence, item.Sensitivity,
		item.ValidFrom, item.ValidUntil, item.ReviewAfter, item.PolicyVersionID, item.GateRunID, item.ContentHash, item.Version,
	).Exec()
	return classifyInsertError(err, "")
}

func (store *Store) UpdateKnowledgeState(itemID, bankID, fromState, toState string, expectedVersion int64) error {
	result, err := store.session.Update("knowledge_item").SetMap(map[string]interface{}{
		"state":   toState,
		"version": dbr.Expr("version + 1"),
	}).Where("id=? AND bank_id=? AND state=? AND version=?", itemID, bankID, fromState, expectedVersion).Exec()
	if err != nil {
		return err
	}
	return requireUpdated(result)
}

func (store *Store) TransitionKnowledgeItem(bankID string, expectedVersion int64, transition *KnowledgeTransition) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		if err := store.CreateTransition(tx, transition); err != nil {
			return err
		}
		result, err := tx.Update("knowledge_item").SetMap(map[string]interface{}{
			"state":   transition.ToState,
			"version": dbr.Expr("version + 1"),
		}).Where(
			"id=? AND bank_id=? AND state=? AND version=?",
			transition.KnowledgeItemID, bankID, transition.FromState, expectedVersion,
		).Exec()
		if err != nil {
			return err
		}
		if err := requireUpdated(result); err != nil {
			return err
		}
		return nil
	})
}

func (store *Store) CreateGovernanceRun(run *GovernanceRun) error {
	return insertGovernanceRun(store.session, run)
}

func insertGovernanceRun(exec governanceRunInserter, run *GovernanceRun) error {
	_, err := exec.InsertInto("governance_run").Columns(
		"id", "mode", "scope_type", "scope_id", "policy_version_id", "status", "idempotency_key",
		"proposed_count", "applied_count", "failed_count", "started_at", "finished_at", "created_by_uid",
	).Values(
		run.ID, run.Mode, run.ScopeType, run.ScopeID, run.PolicyVersionID, run.Status, run.IdempotencyKey,
		run.ProposedCount, run.AppliedCount, run.FailedCount, run.StartedAt, run.FinishedAt, run.CreatedByUID,
	).Exec()
	return classifyInsertError(err, "uk_governance_run_idempotency")
}

type governanceRunInserter interface {
	InsertInto(string) *dbr.InsertStmt
}

func (store *Store) FindGovernanceRun(runID string) (*GovernanceRun, error) {
	var run GovernanceRun
	err := store.session.Select("*").From("governance_run").Where("id=?", runID).LoadOne(&run)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrGovernanceRunNotFound
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (store *Store) FindGovernanceRunByIdempotencyKey(key string) (*GovernanceRun, error) {
	var run GovernanceRun
	err := store.session.Select("*").From("governance_run").Where("idempotency_key=?", key).LoadOne(&run)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrGovernanceRunNotFound
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (store *Store) ListGovernanceRuns(scopeType, scopeID string, limit int) ([]GovernanceRun, error) {
	query := store.session.Select("*").From("governance_run").
		Where("scope_type=? AND scope_id=?", scopeType, scopeID).OrderDesc("started_at").OrderDesc("id")
	if limit > 0 {
		query = query.Limit(uint64(limit))
	}
	var runs []GovernanceRun
	_, err := query.Load(&runs)
	return runs, err
}

func (store *Store) ListGovernanceRunTransitions(runID string) ([]KnowledgeTransition, error) {
	var transitions []KnowledgeTransition
	_, err := store.session.Select("*").From("knowledge_transition").
		Where("governance_run_id=?", runID).OrderAsc("created_at").OrderAsc("id").Load(&transitions)
	return transitions, err
}

// ListGovernableKnowledgeItems loads active knowledge items for the given banks so
// the governance evaluator can propose soft transitions. Physically deleted items are excluded.
func (store *Store) ListGovernableKnowledgeItems(bankIDs []string, limit int) ([]KnowledgeItem, error) {
	if len(bankIDs) == 0 {
		return nil, nil
	}
	query := store.session.Select("*").From("knowledge_item").
		Where("bank_id IN ?", bankIDs).
		Where("state NOT IN ?", []string{"deleted", "revoked"}).
		OrderAsc("bank_id").OrderAsc("id")
	if limit > 0 {
		query = query.Limit(uint64(limit))
	}
	var items []KnowledgeItem
	_, err := query.Load(&items)
	return items, err
}

// PersistGovernanceRun records a governance run together with its transition rows in one
// transaction. In dry-run mode the transitions are proposal records only and no knowledge
// item state is mutated; in apply mode each transition also advances the item under its
// expected version. Governance never issues a physical delete.
func (store *Store) PersistGovernanceRun(run *GovernanceRun, proposals []GovernanceProposal, apply bool) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		if apply {
			// Re-read authority under the transaction that mutates the items. The
			// evaluation-time snapshot is never sufficient for apply.
			if err := authorizeGovernanceApply(tx, run); err != nil {
				return err
			}
		}
		if err := insertGovernanceRun(tx, run); err != nil {
			return err
		}
		for index := range proposals {
			proposal := &proposals[index]
			if apply {
				var bank MemoryBank
				if err := tx.Select("*").From("memory_bank").Where("id=?", proposal.BankID).Suffix("FOR UPDATE").LoadOne(&bank); err != nil {
					return ErrGovernanceConflict
				}
				if bank.Status != BankStatusActive || bank.PolicyVersionID != run.PolicyVersionID || string(bank.ScopeType) != run.ScopeType || bank.ScopeID != run.ScopeID {
					return ErrGovernanceConflict
				}
				item, err := loadPersistenceForUpdate(tx, proposal.Transition.KnowledgeItemID)
				if err != nil || item.BankID != bank.ID || item.PolicyVersionID != run.PolicyVersionID || item.State != proposal.Transition.FromState || item.Version != proposal.ExpectedVersion {
					return ErrGovernanceConflict
				}
				result, err := tx.Update("knowledge_item").SetMap(map[string]interface{}{
					"state":   proposal.Transition.ToState,
					"version": dbr.Expr("version + 1"),
				}).Where(
					"id=? AND bank_id=? AND state=? AND version=?",
					proposal.Transition.KnowledgeItemID, proposal.BankID, proposal.Transition.FromState, proposal.ExpectedVersion,
				).Exec()
				if err != nil {
					return err
				}
				if err := requireUpdated(result); err != nil {
					return err
				}
			}
			if err := store.CreateTransition(tx, &proposal.Transition); err != nil {
				return err
			}
		}
		return nil
	})
}

func authorizeGovernanceApply(tx *dbr.Tx, run *GovernanceRun) error {
	var policy MemoryPolicyVersion
	if err := tx.Select("*").From("memory_policy_version").Where("id=?", run.PolicyVersionID).Suffix("FOR UPDATE").LoadOne(&policy); err != nil || policy.Status != "active" {
		return ErrGovernanceConflict
	}
	switch GovernanceScopeType(run.ScopeType) {
	case GovernanceScopeSpace:
		var roles []int
		if _, err := tx.Select("role").From("space_member").Where("space_id=? AND uid=? AND status=1", run.ScopeID, run.CreatedByUID).Suffix("FOR UPDATE").Load(&roles); err != nil || len(roles) != 1 || roles[0] < 1 {
			return ErrGovernanceForbidden
		}
	case GovernanceScopeAgent:
		var owners []string
		if _, err := tx.Select("r.creator_uid").From(dbr.I("agent_memory_profile").As("p")).Join(dbr.I("robot").As("r"), "r.robot_id=p.robot_id").Where("p.robot_id=? AND p.enabled=1 AND r.status=1", run.ScopeID).Suffix("FOR UPDATE").Load(&owners); err != nil || len(owners) != 1 || owners[0] != run.CreatedByUID {
			return ErrGovernanceForbidden
		}
	default:
		// No durable organization/platform authority row exists in this module.
		return ErrGovernanceForbidden
	}
	return nil
}

func authorizeGovernanceActor(tx *dbr.Tx, scopeType, scopeID, actorUID string) error {
	run := &GovernanceRun{ScopeType: scopeType, ScopeID: scopeID, CreatedByUID: actorUID}
	switch GovernanceScopeType(scopeType) {
	case GovernanceScopeSpace:
		var roles []int
		if _, err := tx.Select("role").From("space_member").Where("space_id=? AND uid=? AND status=1", scopeID, actorUID).Suffix("FOR UPDATE").Load(&roles); err != nil || len(roles) != 1 || roles[0] < 1 {
			return ErrGovernanceForbidden
		}
	case GovernanceScopeAgent:
		var owners []string
		if _, err := tx.Select("r.creator_uid").From(dbr.I("agent_memory_profile").As("p")).Join(dbr.I("robot").As("r"), "r.robot_id=p.robot_id").Where("p.robot_id=? AND p.enabled=1 AND r.status=1", scopeID).Suffix("FOR UPDATE").Load(&owners); err != nil || len(owners) != 1 || owners[0] != actorUID {
			return ErrGovernanceForbidden
		}
	default:
		return ErrGovernanceForbidden
	}
	_ = run
	return nil
}

// ApplyLifecycleTransition advances one knowledge item to a new state under its
// expected version and appends the transition. Idempotent on the transition
// idempotency key: a duplicate returns the already-applied item without a second
// mutation. Concurrency is single-winner via the optimistic version guard.
// History is only ever appended, never erased.
func (store *Store) ApplyLifecycleTransition(transition *KnowledgeTransition, expectedVersion int64) (*KnowledgeItem, error) {
	var applied *KnowledgeItem
	err := store.WithTx(func(tx *dbr.Tx) error {
		if existing, dup, dupErr := loadTransitionByIdempotencyKey(tx, transition.IdempotencyKey); dupErr != nil {
			return dupErr
		} else if dup {
			if existing.KnowledgeItemID != transition.KnowledgeItemID || existing.ToState != transition.ToState {
				return ErrIdempotencyConflict
			}
			item, loadErr := loadPersistence(tx, transition.KnowledgeItemID)
			if loadErr != nil {
				return loadErr
			}
			applied = item
			return nil
		}
		result, err := tx.Update("knowledge_item").SetMap(map[string]interface{}{
			"state":   transition.ToState,
			"version": dbr.Expr("version + 1"),
		}).Where(
			"id=? AND state=? AND version=?",
			transition.KnowledgeItemID, transition.FromState, expectedVersion,
		).Exec()
		if err != nil {
			return err
		}
		if err := requireUpdated(result); err != nil {
			return err
		}
		if err := store.CreateTransition(tx, transition); err != nil {
			return err
		}
		item, loadErr := loadPersistence(tx, transition.KnowledgeItemID)
		if loadErr != nil {
			return loadErr
		}
		applied = item
		return nil
	})
	return applied, err
}

// RollbackGovernanceRun reverses the applied transitions of one governance run,
// restoring each item to its prior state under optimistic version and appending a
// rollback transition that references the original. Prior transition rows are never
// deleted. Idempotent per (run, item, version) and single-winner on version drift.
func (store *Store) RollbackGovernanceRun(runID string, expectedVersion int64, idempotencyKey, actorUID string, newID func() string, now time.Time) (*GovernanceRun, error) {
	var rolledBack *GovernanceRun
	err := store.WithTx(func(tx *dbr.Tx) error {
		var run GovernanceRun
		if err := tx.Select("*").From("governance_run").Where("id=?", runID).Suffix("FOR UPDATE").LoadOne(&run); err != nil {
			if errors.Is(err, dbr.ErrNotFound) {
				return ErrGovernanceRunNotFound
			}
			return err
		}
		if run.Mode != GovernanceModeApply {
			return ErrGovernanceNotReversible
		}
		if run.Status == "rolled_back" {
			if run.RollbackIdempotencyKey != nil && *run.RollbackIdempotencyKey != idempotencyKey {
				return ErrIdempotencyConflict
			}
			rolledBack = &run
			return nil
		}
		if run.Version != expectedVersion {
			return ErrVersionConflict
		}
		if err := authorizeGovernanceActor(tx, run.ScopeType, run.ScopeID, actorUID); err != nil {
			return err
		}
		var policy MemoryPolicyVersion
		if err := tx.Select("*").From("memory_policy_version").Where("id=?", run.PolicyVersionID).Suffix("FOR UPDATE").LoadOne(&policy); err != nil || policy.Status != "active" {
			return ErrGovernanceConflict
		}
		var transitions []KnowledgeTransition
		if _, err := tx.Select("*").From("knowledge_transition").
			Where("governance_run_id=? AND rollback_of IS NULL", runID).
			OrderAsc("created_at").OrderAsc("id").Load(&transitions); err != nil {
			return err
		}
		for index := range transitions {
			original := transitions[index]
			item, loadErr := loadPersistenceForUpdate(tx, original.KnowledgeItemID)
			if loadErr == nil {
				var bank MemoryBank
				if err := tx.Select("*").From("memory_bank").Where("id=?", item.BankID).Suffix("FOR UPDATE").LoadOne(&bank); err != nil || bank.Status != BankStatusActive || bank.PolicyVersionID != run.PolicyVersionID || string(bank.ScopeType) != run.ScopeType || bank.ScopeID != run.ScopeID || item.PolicyVersionID != run.PolicyVersionID {
					return ErrGovernanceConflict
				}
			}
			if loadErr != nil {
				return loadErr
			}
			if item.State != original.ToState {
				return ErrVersionConflict
			}
			result, err := tx.Update("knowledge_item").SetMap(map[string]interface{}{
				"state":   original.FromState,
				"version": dbr.Expr("version + 1"),
			}).Where("id=? AND state=? AND version=?", item.ID, original.ToState, item.Version).Exec()
			if err != nil {
				return err
			}
			if err := requireUpdated(result); err != nil {
				return err
			}
			rollbackID := original.ID
			reverse := KnowledgeTransition{
				ID: newID(), KnowledgeItemID: original.KnowledgeItemID, FromState: original.ToState, ToState: original.FromState,
				ActorType: "governance_job", ActorID: original.ActorID, ReasonCode: "GOVERNANCE_ROLLBACK",
				PolicyVersionID: original.PolicyVersionID, GovernanceRunID: &run.ID, RollbackOf: &rollbackID,
				IdempotencyKey: fmt.Sprintf("%s:rollback:%s", run.IdempotencyKey, original.ID),
			}
			if err := store.CreateTransition(tx, &reverse); err != nil {
				return err
			}
		}
		result, err := tx.Update("governance_run").Set("status", "rolled_back").Set("finished_at", now).
			Set("rollback_idempotency_key", idempotencyKey).Set("version", dbr.Expr("version + 1")).
			Where("id=? AND status=? AND version=?", runID, run.Status, expectedVersion).Exec()
		if err != nil {
			return err
		}
		if err := requireUpdated(result); err != nil {
			return ErrVersionConflict
		}
		run.Status, run.FinishedAt, run.RollbackIdempotencyKey, run.Version = "rolled_back", &now, &idempotencyKey, run.Version+1
		rolledBack = &run
		return nil
	})
	return rolledBack, err
}

func loadPersistenceForUpdate(exec dbr.SessionRunner, id string) (*KnowledgeItem, error) {
	var item KnowledgeItem
	err := exec.Select("*").From("knowledge_item").Where("id=?", id).Suffix("FOR UPDATE").LoadOne(&item)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrPersistenceNotFound
	}
	return &item, err
}

// loadTransitionByIdempotencyKey returns the existing transition for an idempotency
// key, reporting whether one was found so callers can treat a replay as a no-op.
func loadTransitionByIdempotencyKey(exec dbr.SessionRunner, key string) (*KnowledgeTransition, bool, error) {
	var transition KnowledgeTransition
	err := exec.Select("*").From("knowledge_transition").Where("idempotency_key=?", key).LoadOne(&transition)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &transition, true, nil
}

func (store *Store) CreateTransition(tx *dbr.Tx, transition *KnowledgeTransition) error {
	_, err := tx.InsertInto("knowledge_transition").Columns(
		"id", "knowledge_item_id", "from_state", "to_state", "actor_type", "actor_id", "reason_code",
		"reason_redacted", "policy_version_id", "governance_run_id", "rollback_of", "idempotency_key",
	).Values(
		transition.ID, transition.KnowledgeItemID, transition.FromState, transition.ToState, transition.ActorType,
		transition.ActorID, transition.ReasonCode, transition.ReasonRedacted, transition.PolicyVersionID,
		transition.GovernanceRunID, transition.RollbackOf, transition.IdempotencyKey,
	).Exec()
	return classifyInsertError(err, "uk_knowledge_transition_idempotency")
}

func classifyInsertError(err error, idempotencyConstraint string) error {
	if err == nil {
		return nil
	}
	if !isDuplicateKeyError(err) {
		return err
	}
	if idempotencyConstraint != "" && strings.Contains(strings.ToLower(err.Error()), strings.ToLower(idempotencyConstraint)) {
		return ErrIdempotencyConflict
	}
	return ErrDuplicate
}

func requireUpdated(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrVersionConflict
	}
	return nil
}

func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate entry") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "error 1062")
}
