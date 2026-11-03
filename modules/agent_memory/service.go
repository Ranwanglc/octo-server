package agent_memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

const DefaultProfileTrustTier = "normal"

const (
	BankStatusProvisioning = "provisioning"
	BankStatusActive       = "active"
	BankStatusDisabled     = "disabled"
	BankIntentProvision    = "provision"
	BankIntentDisable      = "disable"
)

type RobotLifecycle string

const (
	RobotLifecycleActive   RobotLifecycle = "active"
	RobotLifecycleDisabled RobotLifecycle = "disabled"
	RobotLifecycleRevoked  RobotLifecycle = "revoked"
)

var (
	ErrRobotAuthorityMismatch  = errors.New("robot authority returned mismatched identity")
	ErrCredentialEpochRollback = errors.New("authoritative credential epoch regressed")
	ErrInvalidRobotState       = errors.New("invalid authoritative robot state")
)

type BankService struct {
	store *Store
}

func NewBankService(store *Store) *BankService {
	if store == nil || store.session == nil {
		return nil
	}
	return &BankService{store: store}
}

func (service *BankService) FindBankByID(bankID string) (*MemoryBank, error) {
	return service.store.FindBankByID(bankID)
}

func (service *BankService) ListBanks() ([]MemoryBank, error) {
	return service.store.ListBanks()
}

func (service *BankService) UpdateBank(bank *MemoryBank, expectedVersion int64) error {
	updated, err := service.UpdateLogicalBank(context.Background(), LogicalBankUpdate{Bank: *bank, ExpectedVersion: expectedVersion})
	if err == nil {
		*bank = *updated
	}
	return err
}

func (service *BankService) CreateLogicalBank(_ context.Context, input LogicalBankCreate) (*MemoryBank, error) {
	template, ok := BankTemplateV1(input.ScopeType, BankClass(input.BankType))
	if !ok || template.SystemOnly {
		return nil, ErrUnknownBankClass
	}
	canonicalKey := bankProvisionCanonicalKey(input.ScopeType, input.ScopeID, input.BankType)
	if existing, err := service.findIdempotentBank(canonicalKey, input); err != nil || existing != nil {
		return existing, err
	}

	bank := &MemoryBank{
		ID: input.ID, DisplayName: input.DisplayName, BankType: template.BankType,
		ScopeType: input.ScopeType, ScopeID: input.ScopeID, OwnerUID: input.OwnerUID, CreatedByUID: input.CreatedByUID,
		Sensitivity: template.Sensitivity, RetentionClass: template.Defaults.RetentionClass,
		Status: BankStatusProvisioning,
	}
	intent := BankProvisioningIntent{
		ID: uuid.NewString(), BankID: bank.ID, Operation: BankIntentProvision,
		CanonicalKey: canonicalKey, BankVersion: bank.Version, Status: "pending",
	}

	err := service.store.WithTx(func(tx *dbr.Tx) error {
		policyVersionID, err := activePolicyVersionID(tx)
		if err != nil {
			return err
		}
		bank.PolicyVersionID = policyVersionID
		intent.PayloadJSON, err = bankIntentPayload(*bank, template)
		if err != nil {
			return err
		}
		if err := insertBank(tx, bank); err != nil {
			return err
		}
		return insertBankIntent(tx, intent)
	})
	if errors.Is(err, ErrIdempotencyConflict) {
		if existing, findErr := service.findIdempotentBank(canonicalKey, input); findErr != nil || existing != nil {
			return existing, findErr
		}
	}
	if err != nil {
		return nil, err
	}
	return bank, nil
}

func (service *BankService) UpdateLogicalBank(_ context.Context, input LogicalBankUpdate) (*MemoryBank, error) {
	bank := input.Bank
	if err := bank.ValidateScope(); err != nil {
		return nil, err
	}
	return &bank, service.store.WithTx(func(tx *dbr.Tx) error {
		current, err := findBankForUpdate(tx, bank.ID)
		if err != nil {
			return err
		}
		if current.Version != input.ExpectedVersion || current.ScopeType != bank.ScopeType || current.ScopeID != bank.ScopeID {
			return ErrVersionConflict
		}

		operation := ""
		if bank.Status == BankStatusDisabled && current.Status != BankStatusDisabled {
			operation = BankIntentDisable
		}
		if bank.Status == BankStatusActive && current.Status == BankStatusDisabled {
			operation = BankIntentProvision
			if bank.HindsightBankID == "" {
				bank.Status = BankStatusProvisioning
			}
			bank.DisabledAt = nil
		}
		result, err := tx.Update("memory_bank").SetMap(map[string]interface{}{
			"display_name": bank.DisplayName, "bank_type": bank.BankType, "sensitivity": bank.Sensitivity,
			"retention_class": bank.RetentionClass, "policy_version_id": bank.PolicyVersionID,
			"status": bank.Status, "disabled_at": bank.DisabledAt, "version": dbr.Expr("version + 1"),
		}).Where("id=? AND scope_type=? AND scope_id=? AND version=?", bank.ID, bank.ScopeType, bank.ScopeID, input.ExpectedVersion).Exec()
		if err != nil {
			return err
		}
		if err := requireUpdated(result); err != nil {
			return err
		}
		if current.Status != bank.Status {
			var fence *dbr.UpdateStmt
			switch bank.ScopeType {
			case BankScopeAgent:
				fence = tx.Update("agent_memory_profile").Set("authorization_epoch", dbr.Expr("authorization_epoch+1")).Where("robot_id=?", bank.ScopeID)
			case BankScopeSpace:
				fence = tx.Update("agent_memory_profile").Set("authorization_epoch", dbr.Expr("authorization_epoch+1")).Where("robot_id IN (SELECT robot_id FROM robot WHERE space_id=?)", bank.ScopeID)
			}
			if fence != nil {
				if _, err := fence.Exec(); err != nil {
					return err
				}
			}
		}
		bank.Version = input.ExpectedVersion + 1
		if operation == "" {
			return nil
		}
		payload, err := json.Marshal(map[string]interface{}{"bank_id": bank.ID, "operation": operation, "bank_version": bank.Version})
		if err != nil {
			return err
		}
		return insertBankIntent(tx, BankProvisioningIntent{
			ID: uuid.NewString(), BankID: bank.ID, Operation: operation, BankVersion: bank.Version,
			CanonicalKey: fmt.Sprintf("bank:%s:%s:%d", bank.ID, operation, bank.Version), PayloadJSON: payload, Status: "pending",
		})
	})
}

func (service *BankService) findIdempotentBank(canonicalKey string, input LogicalBankCreate) (*MemoryBank, error) {
	intent, err := service.store.FindBankProvisioningIntent(canonicalKey)
	if errors.Is(err, ErrBankIntentNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	bank, err := service.store.FindBankByID(intent.BankID)
	if err != nil {
		return nil, err
	}
	if bank.ScopeType != input.ScopeType || bank.ScopeID != input.ScopeID || bank.BankType != input.BankType || bank.OwnerUID != input.OwnerUID {
		return nil, ErrIdempotencyConflict
	}
	return bank, nil
}

func bankProvisionCanonicalKey(scopeType BankScopeType, scopeID, bankType string) string {
	return fmt.Sprintf("bank:%s:%s:%s:%s", BankPolicyDefaultsVersionV1, scopeType, strings.TrimSpace(scopeID), bankType)
}

func activePolicyVersionID(tx *dbr.Tx) (string, error) {
	var policyVersionID string
	err := tx.Select("id").From("memory_policy_version").Where("status=?", "active").OrderDesc("activated_at").OrderDesc("created_at").OrderDesc("id").Limit(1).LoadOne(&policyVersionID)
	if errors.Is(err, dbr.ErrNotFound) {
		return "", ErrPolicyNotFound
	}
	return policyVersionID, err
}

func insertBank(tx *dbr.Tx, bank *MemoryBank) error {
	_, err := tx.InsertInto("memory_bank").Columns(
		"id", "hindsight_bank_id", "display_name", "bank_type", "scope_type", "scope_id", "owner_uid",
		"sensitivity", "retention_class", "policy_version_id", "status", "legacy", "version", "disabled_at",
	).Values(
		bank.ID, nil, bank.DisplayName, bank.BankType, bank.ScopeType, bank.ScopeID, bank.OwnerUID,
		bank.Sensitivity, bank.RetentionClass, bank.PolicyVersionID, bank.Status, bank.Legacy, bank.Version, bank.DisabledAt,
	).Exec()
	return classifyInsertError(err, "")
}

func insertBankIntent(tx *dbr.Tx, intent BankProvisioningIntent) error {
	_, err := tx.InsertInto("memory_bank_provisioning_intent").Columns(
		"id", "bank_id", "operation", "canonical_key", "bank_version", "payload_json", "status",
	).Values(intent.ID, intent.BankID, intent.Operation, intent.CanonicalKey, intent.BankVersion, intent.PayloadJSON, intent.Status).Exec()
	return classifyInsertError(err, "uk_memory_bank_intent")
}

func findBankForUpdate(tx *dbr.Tx, bankID string) (*MemoryBank, error) {
	var bank MemoryBank
	err := tx.Select("*").From("memory_bank").Where("id=?", bankID).Suffix("FOR UPDATE").LoadOne(&bank)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrBankNotFound
	}
	return &bank, err
}

func bankIntentPayload(bank MemoryBank, template BankTemplate) ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"bank_id": bank.ID, "display_name": bank.DisplayName, "bank_type": bank.BankType,
		"scope_type": bank.ScopeType, "scope_id": bank.ScopeID, "owner_uid": bank.OwnerUID,
		"sensitivity": bank.Sensitivity, "retention_class": bank.RetentionClass,
		"policy_version_id": bank.PolicyVersionID, "template_id": template.ID, "template_version": template.Version,
	})
}

type AuthoritativeRobotState struct {
	RobotID         string
	AgentUID        string
	OwnerUID        string
	ScopeType       ProfileScopeType
	SpaceID         *string
	Lifecycle       RobotLifecycle
	CredentialEpoch int64
	RobotStatus     int
}

func (state AuthoritativeRobotState) Validate() error {
	if strings.TrimSpace(state.RobotID) == "" || strings.TrimSpace(state.AgentUID) == "" || strings.TrimSpace(state.OwnerUID) == "" {
		return fmt.Errorf("%w: identity and owner are required", ErrInvalidRobotState)
	}
	if state.CredentialEpoch < 0 {
		return fmt.Errorf("%w: credential epoch must not be negative", ErrInvalidRobotState)
	}
	profile := AgentMemoryProfile{ScopeType: state.ScopeType, SpaceID: cloneStringPointer(state.SpaceID)}
	if err := profile.ValidateScope(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRobotState, err)
	}
	if state.AgentUID != state.RobotID {
		return fmt.Errorf("%w: agent_uid must equal robot_id", ErrInvalidRobotState)
	}
	switch state.Lifecycle {
	case RobotLifecycleActive, RobotLifecycleDisabled, RobotLifecycleRevoked:
		return nil
	default:
		return fmt.Errorf("%w: unsupported lifecycle", ErrInvalidRobotState)
	}
}

type RobotAuthority interface {
	LoadAuthoritativeRobot(context.Context, string) (AuthoritativeRobotState, error)
}

type ProfileStore interface {
	FindProfileByRobotID(string) (*AgentMemoryProfile, error)
	FindProfileByAgentUID(string) (*AgentMemoryProfile, error)
	CreateProfile(*AgentMemoryProfile) error
	SyncProfile(*AgentMemoryProfile, int64) error
}

type auditedProfileStore interface {
	ProfileStore
	CreateProfileWithAudit(*AgentMemoryProfile, MemoryAuditEvent) error
	SyncProfileWithAudit(*AgentMemoryProfile, int64, MemoryAuditEvent) error
}

type ProfileService struct {
	authority        RobotAuthority
	store            ProfileStore
	defaultTrustTier string
	now              func() time.Time
}

func NewProfileService(authority RobotAuthority, store ProfileStore) *ProfileService {
	return &ProfileService{
		authority:        authority,
		store:            store,
		defaultTrustTier: DefaultProfileTrustTier,
		now:              time.Now,
	}
}

// SynchronizeProfile derives identity, ownership, scope, lifecycle, trust tier,
// and credential epoch from server-owned state. The candidate supplies only
// the requested enablement and policy version.
func (service *ProfileService) SynchronizeProfile(ctx context.Context, candidate AgentMemoryProfile) (*AgentMemoryProfile, error) {
	return service.synchronizeProfile(ctx, candidate, nil)
}

func (service *ProfileService) UpdateProfile(ctx context.Context, candidate AgentMemoryProfile, enabled, includeOwnTentative *bool, expectedVersion int64) (*AgentMemoryProfile, error) {
	return service.updateProfile(ctx, candidate, enabled, includeOwnTentative, expectedVersion, nil)
}

func (service *ProfileService) UpdateProfileWithAudit(ctx context.Context, candidate AgentMemoryProfile, enabled, includeOwnTentative *bool, expectedVersion int64, event MemoryAuditEvent) (*AgentMemoryProfile, error) {
	return service.updateProfile(ctx, candidate, enabled, includeOwnTentative, expectedVersion, &event)
}

func (service *ProfileService) updateProfile(ctx context.Context, candidate AgentMemoryProfile, enabled, includeOwnTentative *bool, expectedVersion int64, event *MemoryAuditEvent) (*AgentMemoryProfile, error) {
	existing, err := service.store.FindProfileByAgentUID(candidate.AgentUID)
	if err != nil && !errors.Is(err, ErrProfileNotFound) {
		return nil, err
	}
	if existing != nil {
		candidate.RobotID = existing.RobotID
		candidate.Enabled = existing.Enabled
		candidate.IncludeOwnTentative = existing.IncludeOwnTentative
		candidate.PolicyVersionID = existing.PolicyVersionID
	}
	if enabled != nil {
		candidate.Enabled = *enabled
	}
	if includeOwnTentative != nil {
		candidate.IncludeOwnTentative = *includeOwnTentative
	}
	return service.synchronizeProfileWithAudit(ctx, candidate, &expectedVersion, event)
}

func (service *ProfileService) synchronizeProfile(ctx context.Context, candidate AgentMemoryProfile, expectedVersion *int64) (*AgentMemoryProfile, error) {
	return service.synchronizeProfileWithAudit(ctx, candidate, expectedVersion, nil)
}

func (service *ProfileService) synchronizeProfileWithAudit(ctx context.Context, candidate AgentMemoryProfile, expectedVersion *int64, event *MemoryAuditEvent) (*AgentMemoryProfile, error) {
	state, err := service.authority.LoadAuthoritativeRobot(ctx, candidate.RobotID)
	if err != nil {
		return nil, err
	}
	if state.RobotID != candidate.RobotID {
		return nil, ErrRobotAuthorityMismatch
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}

	existing, err := service.store.FindProfileByRobotID(state.RobotID)
	if err != nil && !errors.Is(err, ErrProfileNotFound) {
		return nil, err
	}

	profile := candidate
	profile.RobotID = state.RobotID
	profile.AgentUID = state.AgentUID
	profile.OwnerUID = state.OwnerUID
	profile.ScopeType = state.ScopeType
	profile.SpaceID = cloneStringPointer(state.SpaceID)
	profile.CredentialEpoch = state.CredentialEpoch
	profile.Status = ""
	profile.DisabledAt = nil

	if existing == nil {
		if expectedVersion != nil && *expectedVersion != 0 {
			return nil, ErrVersionConflict
		}
		profile.TrustTier = service.defaultTrustTier
		profile.Version = 0
	} else {
		if expectedVersion != nil && existing.Version != *expectedVersion {
			return nil, ErrVersionConflict
		}
		if state.CredentialEpoch < existing.CredentialEpoch {
			return nil, ErrCredentialEpochRollback
		}
		profile.TrustTier = existing.TrustTier
		profile.CreatedAt = existing.CreatedAt
		profile.Version = existing.Version
		profile.Status = existing.Status
		profile.DisabledAt = existing.DisabledAt
		if profile.PolicyVersionID == "" {
			profile.PolicyVersionID = existing.PolicyVersionID
		}
	}
	service.applyLifecycle(&profile, state.Lifecycle)
	if event != nil {
		event.ResourceID = profile.AgentUID
		event.PolicyVersionID = profile.PolicyVersionID
		event.SpaceID = ""
		if profile.SpaceID != nil {
			event.SpaceID = *profile.SpaceID
		}
	}

	if existing == nil {
		var err error
		if event == nil {
			err = service.store.CreateProfile(&profile)
		} else if store, ok := service.store.(auditedProfileStore); ok {
			err = store.CreateProfileWithAudit(&profile, *event)
		} else {
			err = ErrAuditPersistence
		}
		if err != nil {
			return nil, err
		}
		return &profile, nil
	}
	if event == nil {
		err = service.store.SyncProfile(&profile, existing.Version)
	} else if store, ok := service.store.(auditedProfileStore); ok {
		err = store.SyncProfileWithAudit(&profile, existing.Version, *event)
	} else {
		err = ErrAuditPersistence
	}
	if err != nil {
		return nil, err
	}
	profile.Version = existing.Version + 1
	return &profile, nil
}

func (service *ProfileService) applyLifecycle(profile *AgentMemoryProfile, lifecycle RobotLifecycle) {
	switch lifecycle {
	case RobotLifecycleActive:
		if profile.Enabled {
			profile.Status = "active"
			profile.DisabledAt = nil
			return
		}
		profile.Status = "disabled"
	case RobotLifecycleDisabled:
		profile.Enabled = false
		profile.Status = "disabled"
	case RobotLifecycleRevoked:
		profile.Enabled = false
		profile.Status = "revoked"
	}
	if profile.DisabledAt == nil {
		disabledAt := service.now().UTC()
		profile.DisabledAt = &disabledAt
	}
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var (
	ErrInvalidGrant = errors.New("invalid agent memory grant")
	ErrGrantScope   = errors.New("agent memory grant outside profile scope")
)

type BankCatalog interface {
	FindBankByID(string) (*MemoryBank, error)
}

type GrantStore interface {
	ReplaceProfileGrants(string, int64, []MemoryGrant) error
}

type auditedGrantStore interface {
	GrantStore
	ReplaceProfileGrantsWithAudit(string, int64, []MemoryGrant, MemoryAuditEvent) error
}

type GrantInput struct {
	PrincipalType PrincipalType
	PrincipalID   string
	BankID        string
	Capability    Capability
	Effect        GrantEffect
	ValidFrom     *time.Time
	ValidUntil    *time.Time
}

type GrantService struct {
	banks BankCatalog
	store GrantStore
	now   func() time.Time
	newID func() string
}

func NewGrantService(banks BankCatalog, store GrantStore) *GrantService {
	return &GrantService{
		banks: banks,
		store: store,
		now:   time.Now,
		newID: func() string { return uuid.NewString() },
	}
}

func (service *GrantService) ReplaceProfileGrants(profile AgentMemoryProfile, issuerUID, reason string, expectedVersion int64, inputs []GrantInput) ([]MemoryGrant, error) {
	return service.replaceProfileGrants(profile, issuerUID, reason, expectedVersion, inputs, nil)
}

func (service *GrantService) ReplaceProfileGrantsWithAudit(profile AgentMemoryProfile, issuerUID, reason string, expectedVersion int64, inputs []GrantInput, event MemoryAuditEvent) ([]MemoryGrant, error) {
	return service.replaceProfileGrants(profile, issuerUID, reason, expectedVersion, inputs, &event)
}

func (service *GrantService) replaceProfileGrants(profile AgentMemoryProfile, issuerUID, reason string, expectedVersion int64, inputs []GrantInput, event *MemoryAuditEvent) ([]MemoryGrant, error) {
	if service == nil || service.banks == nil || service.store == nil {
		return nil, errors.New("agent memory grant service unavailable")
	}
	reason = strings.TrimSpace(reason)
	if expectedVersion < 0 || !validRedactedAuditReason(reason) || strings.TrimSpace(issuerUID) == "" {
		return nil, ErrInvalidGrant
	}
	if profile.AgentUID == "" || profile.PolicyVersionID == "" || profile.Version != expectedVersion {
		return nil, ErrVersionConflict
	}

	now := service.now().UTC()
	grants := make([]MemoryGrant, 0, len(inputs))
	for _, input := range inputs {
		grant, err := service.validateGrant(profile, issuerUID, reason, now, input)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	if event != nil {
		event.ResourceID = profile.AgentUID
		event.PolicyVersionID = profile.PolicyVersionID
		event.SpaceID = ""
		if profile.SpaceID != nil {
			event.SpaceID = *profile.SpaceID
		}
	}
	var err error
	if event == nil {
		err = service.store.ReplaceProfileGrants(profile.AgentUID, expectedVersion, grants)
	} else if store, ok := service.store.(auditedGrantStore); ok {
		err = store.ReplaceProfileGrantsWithAudit(profile.AgentUID, expectedVersion, grants, *event)
	} else {
		err = ErrAuditPersistence
	}
	if err != nil {
		return nil, err
	}
	return grants, nil
}

func validRedactedAuditReason(reason string) bool {
	if reason == "" || len(reason) > 120 || containsCredentialLikeValue(reason) || strings.ContainsAny(reason, "\r\n{}[]<>") {
		return false
	}
	for _, character := range reason {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func (service *GrantService) validateGrant(profile AgentMemoryProfile, issuerUID, reason string, now time.Time, input GrantInput) (MemoryGrant, error) {
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	input.BankID = strings.TrimSpace(input.BankID)
	if !knownPrincipalType(input.PrincipalType) || input.PrincipalID == "" || input.BankID == "" ||
		!knownCapability(input.Capability) || !knownGrantEffect(input.Effect) {
		return MemoryGrant{}, ErrInvalidGrant
	}
	if input.ValidFrom != nil && input.ValidUntil != nil && !input.ValidFrom.Before(*input.ValidUntil) {
		return MemoryGrant{}, fmt.Errorf("%w: invalid validity window", ErrInvalidGrant)
	}
	if input.ValidUntil != nil && !now.Before(*input.ValidUntil) {
		return MemoryGrant{}, fmt.Errorf("%w: valid_until must be in the future", ErrInvalidGrant)
	}
	if !principalWithinProfile(profile, input.PrincipalType, input.PrincipalID) {
		return MemoryGrant{}, ErrGrantScope
	}

	bank, err := service.banks.FindBankByID(input.BankID)
	if errors.Is(err, ErrBankNotFound) {
		return MemoryGrant{}, ErrGrantScope
	}
	if err != nil {
		return MemoryGrant{}, err
	}
	if bank.Status != "active" || !bankWithinProfile(profile, *bank) {
		return MemoryGrant{}, ErrGrantScope
	}
	// Space collaboration requires an exact-revision owner acceptance. This
	// legacy endpoint is only an ownership-entitlement and explicit-deny path.
	if input.Effect == GrantEffectAllow && (bank.ScopeType != BankScopeAgent || bank.ScopeID != profile.AgentUID || (bank.BankType != "private" && bank.BankType != "tentative")) {
		return MemoryGrant{}, ErrGrantScope
	}
	if input.Effect == GrantEffectAllow && issuerUID != profile.OwnerUID {
		return MemoryGrant{}, ErrGrantScope
	}
	sourceKind := "ownership"
	if input.Effect == GrantEffectDeny {
		sourceKind = "explicit_deny"
	}
	return MemoryGrant{
		ID: service.newID(), PrincipalType: string(input.PrincipalType), PrincipalID: input.PrincipalID,
		BankID: bank.ID, Capability: string(input.Capability), Effect: string(input.Effect),
		ValidFrom: cloneTimePointer(input.ValidFrom), ValidUntil: cloneTimePointer(input.ValidUntil),
		IssuedByUID: issuerUID, Reason: reason, PolicyVersionID: profile.PolicyVersionID, SourceKind: sourceKind,
	}, nil
}

func knownPrincipalType(principalType PrincipalType) bool {
	switch principalType {
	case PrincipalTypeAgent, PrincipalTypeSpaceRole, PrincipalTypeUser:
		return true
	default:
		return false
	}
}

func knownGrantEffect(effect GrantEffect) bool {
	return effect == GrantEffectAllow || effect == GrantEffectDeny
}

func principalWithinProfile(profile AgentMemoryProfile, principalType PrincipalType, principalID string) bool {
	return principalType == PrincipalTypeAgent && principalID == profile.AgentUID
}

func bankWithinProfile(profile AgentMemoryProfile, bank MemoryBank) bool {
	switch bank.ScopeType {
	case BankScopeAgent:
		return bank.ScopeID == profile.AgentUID
	case BankScopeSpace:
		return profile.ScopeType == ProfileScopeSpace && profile.SpaceID != nil && bank.ScopeID == *profile.SpaceID
	default:
		return false
	}
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
