package agent_memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

var (
	ErrOfferNotFound      = errors.New("agent memory offer not found")
	ErrAcceptanceNotFound = errors.New("agent memory acceptance not found")
	ErrCollaborationScope = errors.New("agent memory collaboration scope forbidden")
	ErrOfferRevision      = errors.New("agent memory offer revision conflict")
)

type CapabilityOffer struct {
	ID                  string           `db:"id" json:"id"`
	Revision            int64            `db:"revision" json:"revision"`
	BankID              string           `db:"bank_id" json:"bank_id"`
	TargetRobotID       string           `db:"target_robot_id" json:"target_robot_id"`
	AudienceSpaceID     string           `db:"audience_space_id" json:"audience_space_id"`
	OfferedJSON         []byte           `db:"offered_capabilities_json" json:"-"`
	OfferedCapabilities []Capability     `db:"-" json:"offered_capabilities"`
	Status              string           `db:"status" json:"status"`
	ValidFrom           time.Time        `db:"valid_from" json:"valid_from"`
	ValidUntil          *time.Time       `db:"valid_until" json:"valid_until,omitempty"`
	IssuerUID           string           `db:"issuer_uid" json:"issuer_uid"`
	Reason              string           `db:"reason" json:"reason"`
	PolicyVersionID     string           `db:"policy_version_id" json:"policy_version_id"`
	Version             int64            `db:"version" json:"version"`
	CreatedAt           time.Time        `db:"created_at" json:"-"`
	UpdatedAt           time.Time        `db:"updated_at" json:"-"`
	RevokedAt           *time.Time       `db:"revoked_at" json:"revoked_at,omitempty"`
	ExpiredAt           *time.Time       `db:"expired_at" json:"expired_at,omitempty"`
	Acceptance          *OfferAcceptance `db:"-" json:"acceptance,omitempty"`
}

type OfferAcceptance struct {
	ID                   string       `db:"id" json:"id"`
	OfferID              string       `db:"offer_id" json:"offer_id"`
	OfferRevision        int64        `db:"offer_revision" json:"offer_revision"`
	RobotID              string       `db:"robot_id" json:"robot_id"`
	AcceptedJSON         []byte       `db:"accepted_capabilities_json" json:"-"`
	AcceptedCapabilities []Capability `db:"-" json:"accepted_capabilities"`
	Status               string       `db:"status" json:"status"`
	AcceptedByOwnerUID   string       `db:"accepted_by_owner_uid" json:"accepted_by_owner_uid"`
	Reason               string       `db:"reason" json:"reason"`
	Version              int64        `db:"version" json:"version"`
	DecidedAt            *time.Time   `db:"decided_at" json:"decided_at,omitempty"`
	UpdatedAt            time.Time    `db:"updated_at" json:"-"`
	InvalidatedAt        *time.Time   `db:"invalidated_at" json:"invalidated_at,omitempty"`
}

type EffectivePermission struct {
	BankID     string     `json:"bank_id"`
	Capability Capability `json:"capability"`
	State      string     `json:"state"`
	SourceKind string     `json:"source_kind"`
	ReasonCode string     `json:"reason_code,omitempty"`
}

type RobotCollaborationFacts struct {
	RobotID  string `db:"robot_id"`
	OwnerUID string `db:"creator_uid"`
	SpaceID  string `db:"space_id"`
	Status   int    `db:"status"`
}

type OfferCreate struct {
	BankID        string
	TargetRobotID string
	Capabilities  []Capability
	ValidUntil    *time.Time
	IssuerUID     string
	Reason        string
}

type OfferUpdate struct {
	Capabilities    []Capability
	ValidUntil      *time.Time
	Status          string
	ExpectedVersion int64
	IssuerUID       string
	Reason          string
}

type OfferDecision struct {
	OfferRevision   int64
	Decision        string
	Capabilities    []Capability
	ExpectedVersion int64
	OwnerUID        string
	Reason          string
}

type CollaborationService struct {
	store *Store
	now   func() time.Time
	newID func() string
}

func NewCollaborationService(store *Store) *CollaborationService {
	return &CollaborationService{store: store, now: time.Now, newID: uuid.NewString}
}

func normalizeCapabilities(values []Capability) ([]Capability, error) {
	seen := map[Capability]bool{}
	result := make([]Capability, 0, len(values))
	for _, value := range values {
		if !knownCapability(value) || seen[value] {
			return nil, ErrInvalidGrant
		}
		seen[value] = true
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, ErrInvalidGrant
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func capabilitySubset(subset, ceiling []Capability) bool {
	allowed := map[Capability]bool{}
	for _, capability := range ceiling {
		allowed[capability] = true
	}
	for _, capability := range subset {
		if !allowed[capability] {
			return false
		}
	}
	return true
}

func (service *CollaborationService) CreateOffer(ctx context.Context, input OfferCreate) (*CapabilityOffer, error) {
	capabilities, err := normalizeCapabilities(input.Capabilities)
	if err != nil || strings.TrimSpace(input.Reason) == "" || strings.TrimSpace(input.IssuerUID) == "" {
		return nil, ErrInvalidGrant
	}
	bank, err := service.store.FindBankByID(input.BankID)
	if err != nil {
		return nil, err
	}
	policy, err := service.store.FindPolicyVersionByID(bank.PolicyVersionID)
	if err != nil {
		return nil, err
	}
	ceiling, err := policyCeiling(policy)
	if err != nil || !capabilitySubset(capabilities, ceiling) {
		return nil, ErrInvalidGrant
	}
	robot, err := service.store.FindRobotCollaborationFacts(input.TargetRobotID)
	if err != nil {
		return nil, err
	}
	if bank.Status != BankStatusActive || bank.ScopeType != BankScopeSpace || (bank.BankType != "shared" && bank.BankType != "verified") || robot.Status != 1 || robot.SpaceID == "" || robot.SpaceID != bank.ScopeID {
		return nil, ErrCollaborationScope
	}
	now := service.now().UTC()
	if input.ValidUntil != nil && !now.Before(*input.ValidUntil) {
		return nil, ErrInvalidGrant
	}
	offered, _ := json.Marshal(capabilities)
	offer := &CapabilityOffer{ID: service.newID(), Revision: 1, BankID: bank.ID, TargetRobotID: robot.RobotID, AudienceSpaceID: robot.SpaceID, OfferedJSON: offered, OfferedCapabilities: capabilities, Status: "active", ValidFrom: now, ValidUntil: cloneTimePointer(input.ValidUntil), IssuerUID: input.IssuerUID, Reason: strings.TrimSpace(input.Reason), PolicyVersionID: bank.PolicyVersionID}
	if err := service.store.CreateOfferAndFence(offer); err != nil {
		return nil, err
	}
	return offer, nil
}

func (service *CollaborationService) ReviseOffer(ctx context.Context, offerID string, input OfferUpdate) (*CapabilityOffer, error) {
	current, err := service.store.FindCurrentOffer(offerID)
	if err != nil {
		return nil, err
	}
	if current.Version != input.ExpectedVersion || current.Status != "active" {
		return nil, ErrVersionConflict
	}
	if strings.TrimSpace(input.IssuerUID) == "" || strings.TrimSpace(input.Reason) == "" {
		return nil, ErrInvalidGrant
	}
	if input.Status == "revoked" {
		if err := service.store.RevokeOfferAndFence(current, input.ExpectedVersion, service.now().UTC()); err != nil {
			return nil, err
		}
		current.Status, current.Version = "revoked", current.Version+1
		return current, nil
	}
	capabilities, err := normalizeCapabilities(input.Capabilities)
	if err != nil {
		return nil, err
	}
	policy, err := service.store.FindPolicyVersionByID(current.PolicyVersionID)
	if err != nil {
		return nil, err
	}
	ceiling, err := policyCeiling(policy)
	if err != nil || !capabilitySubset(capabilities, ceiling) {
		return nil, ErrInvalidGrant
	}
	offered, _ := json.Marshal(capabilities)
	next := *current
	next.Revision++
	next.Version = 0
	next.OfferedCapabilities, next.OfferedJSON = capabilities, offered
	next.ValidUntil, next.IssuerUID, next.Reason = cloneTimePointer(input.ValidUntil), input.IssuerUID, strings.TrimSpace(input.Reason)
	next.ValidFrom, next.CreatedAt, next.UpdatedAt, next.Acceptance = service.now().UTC(), time.Time{}, time.Time{}, nil
	if err := service.store.ReviseOfferAndFence(current, &next, input.ExpectedVersion); err != nil {
		return nil, err
	}
	return &next, nil
}

func (service *CollaborationService) DecideOffer(ctx context.Context, offerID string, input OfferDecision) (*OfferAcceptance, error) {
	offer, err := service.store.FindOfferRevision(offerID, input.OfferRevision)
	if err != nil {
		return nil, err
	}
	robot, err := service.store.FindRobotCollaborationFacts(offer.TargetRobotID)
	if err != nil {
		return nil, err
	}
	bank, err := service.store.FindBankByID(offer.BankID)
	if err != nil {
		return nil, err
	}
	if input.ExpectedVersion != offer.Version || offer.Status != "active" || robot.Status != 1 || robot.OwnerUID != input.OwnerUID || robot.SpaceID != offer.AudienceSpaceID || bank.Status != BankStatusActive || bank.ScopeID != robot.SpaceID {
		return nil, ErrOfferRevision
	}
	if offer.ValidUntil != nil && !service.now().UTC().Before(*offer.ValidUntil) {
		return nil, ErrOfferRevision
	}
	policy, err := service.store.FindPolicyVersionByID(offer.PolicyVersionID)
	if err != nil {
		return nil, err
	}
	ceiling, err := policyCeiling(policy)
	if err != nil {
		return nil, ErrInvalidGrant
	}
	capabilities := []Capability{}
	status := "declined"
	if input.Decision == "accept" {
		capabilities, err = normalizeCapabilities(input.Capabilities)
		if err != nil || !capabilitySubset(capabilities, offer.OfferedCapabilities) || !capabilitySubset(capabilities, ceiling) {
			return nil, ErrInvalidGrant
		}
		status = "accepted"
	} else if input.Decision != "decline" {
		return nil, ErrInvalidGrant
	}
	accepted, _ := json.Marshal(capabilities)
	now := service.now().UTC()
	acceptance := &OfferAcceptance{ID: service.newID(), OfferID: offer.ID, OfferRevision: offer.Revision, RobotID: offer.TargetRobotID, AcceptedJSON: accepted, AcceptedCapabilities: capabilities, Status: status, AcceptedByOwnerUID: input.OwnerUID, Reason: strings.TrimSpace(input.Reason), DecidedAt: &now}
	if err := service.store.DecideOfferAndFence(offer, acceptance); err != nil {
		return nil, err
	}
	return acceptance, nil
}

func (service *CollaborationService) WithdrawAcceptance(ctx context.Context, acceptanceID, ownerUID string, expectedVersion int64, reason string) (*OfferAcceptance, error) {
	acceptance, err := service.store.FindAcceptance(acceptanceID)
	if err != nil {
		return nil, err
	}
	robot, err := service.store.FindRobotCollaborationFacts(acceptance.RobotID)
	if err != nil {
		return nil, err
	}
	if robot.OwnerUID != ownerUID || acceptance.Status != "accepted" || acceptance.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	if err := service.store.WithdrawAcceptanceAndFence(acceptance, strings.TrimSpace(reason)); err != nil {
		return nil, err
	}
	acceptance.Status, acceptance.Version, acceptance.Reason = "withdrawn", acceptance.Version+1, strings.TrimSpace(reason)
	return acceptance, nil
}

func (service *CollaborationService) EffectivePermissions(robotID string) ([]EffectivePermission, error) {
	profile, err := service.store.FindProfileByRobotID(robotID)
	if err != nil {
		return nil, err
	}
	grants, err := service.store.ListAgentGrants(robotID)
	if err != nil {
		return nil, err
	}
	offers, err := service.store.ListRobotOffers(robotID)
	if err != nil {
		return nil, err
	}
	robot, err := service.store.FindRobotCollaborationFacts(robotID)
	if err != nil {
		return nil, err
	}
	policy, err := service.store.FindPolicyVersionByID(profile.PolicyVersionID)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	robotActive := robot.Status == 1 && robot.OwnerUID == profile.OwnerUID && robot.SpaceID != ""
	result := make([]EffectivePermission, 0)

	// Explanations are projections of the same authoritative decision engine used
	// by runtime. They never reproduce grant/deny/scope/policy rules.
	seenOwnership := map[string]bool{}
	for _, grant := range grants {
		if grant.SourceKind != "ownership" || grant.Effect != string(GrantEffectAllow) || grant.RevokedAt != nil {
			continue
		}
		key := grant.BankID + ":" + grant.Capability
		if seenOwnership[key] {
			continue
		}
		seenOwnership[key] = true
		bank, bankErr := service.store.FindBankByID(grant.BankID)
		if bankErr != nil {
			return nil, bankErr
		}
		decision := ResolveCapability(CapabilityRequest{Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: profile.AgentUID}, Profile: *profile, Bank: *bank, Capability: Capability(grant.Capability), Grants: grantsForPolicy(grants, profile.PolicyVersionID), Policy: policy, Now: now, RobotActive: &robotActive})
		result = append(result, explainDecision(grant.BankID, Capability(grant.Capability), "ownership", decision))
	}
	for _, offer := range offers {
		bank, bankErr := service.store.FindBankByID(offer.BankID)
		if bankErr != nil {
			return nil, bankErr
		}
		for _, capability := range offer.OfferedCapabilities {
			decision := ResolveCapability(CapabilityRequest{Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: profile.AgentUID}, Profile: *profile, Bank: *bank, Capability: capability, Grants: grantsForPolicy(grants, profile.PolicyVersionID), Policy: policy, Now: now, RobotActive: &robotActive})
			permission := explainDecision(offer.BankID, capability, "offer_acceptance", decision)
			if !decision.Allowed && decision.Cause == "NO_ALLOW" {
				switch {
				case offer.Status != "active" || now.Before(offer.ValidFrom) || (offer.ValidUntil != nil && !now.Before(*offer.ValidUntil)):
					permission.State, permission.ReasonCode = "expired", "OFFER_INACTIVE"
				case offer.Acceptance == nil || offer.Acceptance.Status != "accepted":
					permission.State, permission.ReasonCode = "offered", ""
				case offer.Acceptance.OfferRevision != offer.Revision || offer.Acceptance.RobotID != robot.RobotID || offer.Acceptance.AcceptedByOwnerUID != robot.OwnerUID || !capabilitySubset([]Capability{capability}, offer.Acceptance.AcceptedCapabilities):
					permission.State, permission.ReasonCode = "unavailable", "ACCEPTANCE_STALE"
				}
			}
			result = append(result, permission)
		}
	}
	return result, nil
}

func explainDecision(bankID string, capability Capability, source string, decision PolicyDecision) EffectivePermission {
	permission := EffectivePermission{BankID: bankID, Capability: capability, SourceKind: source}
	if decision.Allowed {
		permission.State = "effective"
		return permission
	}
	permission.State, permission.ReasonCode = "denied", decision.Cause
	if decision.Cause == "PROFILE_DISABLED" || decision.Cause == "ROBOT_INACTIVE" || decision.Cause == "BANK_UNAVAILABLE" || decision.Cause == "SCOPE_INELIGIBLE" {
		permission.State = "unavailable"
	}
	if decision.Cause == "EXPLICIT_DENY" {
		permission.SourceKind = "explicit_deny"
	}
	return permission
}

func decodeOffer(row *CapabilityOffer) error {
	if err := json.Unmarshal(row.OfferedJSON, &row.OfferedCapabilities); err != nil {
		return fmt.Errorf("decode offer capabilities: %w", err)
	}
	return nil
}

func decodeAcceptance(row *OfferAcceptance) error {
	if err := json.Unmarshal(row.AcceptedJSON, &row.AcceptedCapabilities); err != nil {
		return fmt.Errorf("decode accepted capabilities: %w", err)
	}
	return nil
}

func (store *Store) FindRobotCollaborationFacts(robotID string) (*RobotCollaborationFacts, error) {
	var facts RobotCollaborationFacts
	err := store.session.Select("robot_id", "creator_uid", "COALESCE(space_id, '') AS space_id", "status").From("robot").Where("robot_id=?", robotID).LoadOne(&facts)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrProfileNotFound
	}
	return &facts, err
}

func (store *Store) CreateOfferAndFence(offer *CapabilityOffer) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		if _, err := tx.InsertInto("memory_capability_offer").Columns("id", "revision", "bank_id", "target_robot_id", "audience_space_id", "offered_capabilities_json", "status", "valid_from", "valid_until", "issuer_uid", "reason", "policy_version_id", "version").Record(offer).Exec(); err != nil {
			return err
		}
		if err := fenceProfile(tx, offer.TargetRobotID); err != nil {
			return err
		}
		return appendCollaborationAudit(tx, AuditEventOfferChange, offer.IssuerUID, offer.AudienceSpaceID, "capability_offer", offer.ID, "offer_created", offer.PolicyVersionID)
	})
}

func (store *Store) FindOfferRevision(id string, revision int64) (*CapabilityOffer, error) {
	var offer CapabilityOffer
	err := store.session.Select("*").From("memory_capability_offer").Where("id=? AND revision=?", id, revision).LoadOne(&offer)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrOfferNotFound
	}
	if err != nil {
		return nil, err
	}
	return &offer, decodeOffer(&offer)
}

func (store *Store) FindCurrentOffer(id string) (*CapabilityOffer, error) {
	var offer CapabilityOffer
	err := store.session.Select("*").From("memory_capability_offer").Where("id=?", id).OrderDesc("revision").Limit(1).LoadOne(&offer)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrOfferNotFound
	}
	if err != nil {
		return nil, err
	}
	return &offer, decodeOffer(&offer)
}

func (store *Store) ListBankOffers(bankID string) ([]CapabilityOffer, error) {
	return store.listOffers("bank_id", bankID)
}
func (store *Store) ListRobotOffers(robotID string) ([]CapabilityOffer, error) {
	return store.listOffers("target_robot_id", robotID)
}

func (store *Store) listOffers(column, value string) ([]CapabilityOffer, error) {
	var offers []CapabilityOffer
	query := fmt.Sprintf("SELECT o.* FROM memory_capability_offer o JOIN (SELECT id, MAX(revision) revision FROM memory_capability_offer GROUP BY id) current ON current.id=o.id AND current.revision=o.revision WHERE o.%s=?", column)
	if _, err := store.session.SelectBySql(query, value).Load(&offers); err != nil {
		return nil, err
	}
	for index := range offers {
		if err := decodeOffer(&offers[index]); err != nil {
			return nil, err
		}
		acceptance, err := store.findAcceptanceForOffer(offers[index].ID, offers[index].Revision)
		if err == nil {
			offers[index].Acceptance = acceptance
		} else if !errors.Is(err, ErrAcceptanceNotFound) {
			return nil, err
		}
	}
	return offers, nil
}

func (store *Store) ReviseOfferAndFence(current, next *CapabilityOffer, expected int64) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		result, err := tx.Update("memory_capability_offer").Set("status", "superseded").Set("version", dbr.Expr("version+1")).Where("id=? AND revision=? AND version=? AND status='active'", current.ID, current.Revision, expected).Exec()
		if err != nil {
			return err
		}
		if err = requireUpdated(result); err != nil {
			return err
		}
		if _, err = tx.InsertInto("memory_capability_offer").Columns("id", "revision", "bank_id", "target_robot_id", "audience_space_id", "offered_capabilities_json", "status", "valid_from", "valid_until", "issuer_uid", "reason", "policy_version_id", "version").Record(next).Exec(); err != nil {
			return err
		}
		if _, err = tx.Update("memory_grant").Set("revoked_at", dbr.Expr("CURRENT_TIMESTAMP")).Where("source_kind='offer_acceptance' AND source_offer_id=? AND revoked_at IS NULL", current.ID).Exec(); err != nil {
			return err
		}
		if err := fenceProfile(tx, current.TargetRobotID); err != nil {
			return err
		}
		return appendCollaborationAudit(tx, AuditEventOfferChange, next.IssuerUID, next.AudienceSpaceID, "capability_offer", next.ID, "offer_revised", next.PolicyVersionID)
	})
}

func (store *Store) RevokeOfferAndFence(offer *CapabilityOffer, expected int64, now time.Time) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		result, err := tx.Update("memory_capability_offer").Set("status", "revoked").Set("revoked_at", now).Set("version", dbr.Expr("version+1")).Where("id=? AND revision=? AND version=? AND status='active'", offer.ID, offer.Revision, expected).Exec()
		if err != nil {
			return err
		}
		if err = requireUpdated(result); err != nil {
			return err
		}
		if _, err = tx.Update("memory_grant").Set("revoked_at", now).Where("source_kind='offer_acceptance' AND source_offer_id=? AND revoked_at IS NULL", offer.ID).Exec(); err != nil {
			return err
		}
		if err := fenceProfile(tx, offer.TargetRobotID); err != nil {
			return err
		}
		return appendCollaborationAudit(tx, AuditEventOfferChange, offer.IssuerUID, offer.AudienceSpaceID, "capability_offer", offer.ID, "offer_revoked", offer.PolicyVersionID)
	})
}

func (store *Store) DecideOfferAndFence(offer *CapabilityOffer, acceptance *OfferAcceptance) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		var lockedOffer CapabilityOffer
		if err := tx.Select("*").From("memory_capability_offer").Where("id=? AND revision=?", offer.ID, offer.Revision).Suffix("FOR UPDATE").LoadOne(&lockedOffer); err != nil || decodeOffer(&lockedOffer) != nil || lockedOffer.Version != offer.Version || lockedOffer.Status != "active" || lockedOffer.BankID != offer.BankID || lockedOffer.TargetRobotID != offer.TargetRobotID || lockedOffer.AudienceSpaceID != offer.AudienceSpaceID || lockedOffer.PolicyVersionID != offer.PolicyVersionID {
			return ErrOfferRevision
		}
		var robot RobotCollaborationFacts
		if err := tx.Select("robot_id", "creator_uid", "space_id", "status").From("robot").Where("robot_id=?", lockedOffer.TargetRobotID).Suffix("FOR UPDATE").LoadOne(&robot); err != nil || robot.Status != 1 || robot.OwnerUID != acceptance.AcceptedByOwnerUID || robot.SpaceID != lockedOffer.AudienceSpaceID {
			return ErrOfferRevision
		}
		var bank MemoryBank
		if err := tx.Select("*").From("memory_bank").Where("id=?", lockedOffer.BankID).Suffix("FOR UPDATE").LoadOne(&bank); err != nil || bank.Status != BankStatusActive || bank.ScopeType != BankScopeSpace || bank.ScopeID != robot.SpaceID || bank.PolicyVersionID != lockedOffer.PolicyVersionID {
			return ErrOfferRevision
		}
		var policy MemoryPolicyVersion
		if err := tx.Select("*").From("memory_policy_version").Where("id=?", lockedOffer.PolicyVersionID).Suffix("FOR UPDATE").LoadOne(&policy); err != nil {
			return ErrOfferRevision
		}
		ceiling, err := policyCeiling(&policy)
		if err != nil || !capabilitySubset(acceptance.AcceptedCapabilities, lockedOffer.OfferedCapabilities) || !capabilitySubset(acceptance.AcceptedCapabilities, ceiling) || !policyAllowsSensitivity(policy.DocumentJSON, bank.Sensitivity) {
			return ErrInvalidGrant
		}
		if lockedOffer.ValidUntil != nil && !time.Now().UTC().Before(*lockedOffer.ValidUntil) {
			return ErrOfferRevision
		}
		if _, err := tx.InsertInto("memory_offer_acceptance").Columns("id", "offer_id", "offer_revision", "robot_id", "accepted_capabilities_json", "status", "accepted_by_owner_uid", "reason", "version", "decided_at").Record(acceptance).Exec(); err != nil {
			return classifyInsertError(err, "")
		}
		if acceptance.Status == "accepted" {
			for _, capability := range acceptance.AcceptedCapabilities {
				_, err := tx.InsertInto("memory_grant").Columns("id", "principal_type", "principal_id", "bank_id", "capability", "effect", "valid_from", "valid_until", "issued_by_uid", "reason", "policy_version_id", "source_kind", "source_offer_id", "source_acceptance_id", "grant_set_version").Values(uuid.NewString(), string(PrincipalTypeAgent), offer.TargetRobotID, offer.BankID, string(capability), string(GrantEffectAllow), offer.ValidFrom, offer.ValidUntil, offer.IssuerUID, offer.Reason, offer.PolicyVersionID, "offer_acceptance", offer.ID, acceptance.ID, offer.Revision).Exec()
				if err != nil {
					return err
				}
			}
		}
		if err := fenceProfile(tx, offer.TargetRobotID); err != nil {
			return err
		}
		reason := "offer_declined"
		if acceptance.Status == "accepted" {
			reason = "offer_accepted"
		}
		return appendCollaborationAudit(tx, AuditEventAcceptance, acceptance.AcceptedByOwnerUID, offer.AudienceSpaceID, "offer_acceptance", acceptance.ID, reason, offer.PolicyVersionID)
	})
}

func (store *Store) FindAcceptance(id string) (*OfferAcceptance, error) {
	var acceptance OfferAcceptance
	err := store.session.Select("*").From("memory_offer_acceptance").Where("id=?", id).LoadOne(&acceptance)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrAcceptanceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &acceptance, decodeAcceptance(&acceptance)
}

func (store *Store) findAcceptanceForOffer(id string, revision int64) (*OfferAcceptance, error) {
	var acceptance OfferAcceptance
	err := store.session.Select("*").From("memory_offer_acceptance").Where("offer_id=? AND offer_revision=?", id, revision).LoadOne(&acceptance)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrAcceptanceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &acceptance, decodeAcceptance(&acceptance)
}

func (store *Store) WithdrawAcceptanceAndFence(acceptance *OfferAcceptance, reason string) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		result, err := tx.Update("memory_offer_acceptance").Set("status", "withdrawn").Set("reason", reason).Set("version", dbr.Expr("version+1")).Where("id=? AND version=? AND status='accepted'", acceptance.ID, acceptance.Version).Exec()
		if err != nil {
			return err
		}
		if err = requireUpdated(result); err != nil {
			return err
		}
		if _, err = tx.Update("memory_grant").Set("revoked_at", dbr.Expr("CURRENT_TIMESTAMP")).Where("source_acceptance_id=? AND revoked_at IS NULL", acceptance.ID).Exec(); err != nil {
			return err
		}
		if err := fenceProfile(tx, acceptance.RobotID); err != nil {
			return err
		}
		return appendCollaborationAudit(tx, AuditEventAcceptance, acceptance.AcceptedByOwnerUID, "", "offer_acceptance", acceptance.ID, "acceptance_withdrawn", "")
	})
}

func fenceProfile(tx *dbr.Tx, robotID string) error {
	result, err := tx.Update("agent_memory_profile").Set("authorization_epoch", dbr.Expr("authorization_epoch+1")).Set("updated_at", dbr.Expr("CURRENT_TIMESTAMP")).Where("robot_id=?", robotID).Exec()
	if err != nil {
		return err
	}
	return requireUpdated(result)
}

func appendCollaborationAudit(tx *dbr.Tx, eventType, actorUID, spaceID, resourceType, resourceID, reason, policyID string) error {
	return appendAuditEvent(tx, MemoryAuditEvent{ID: uuid.NewString(), EventType: eventType, PrincipalUserUID: actorUID, SpaceID: spaceID, ResourceType: resourceType, ResourceID: resourceID, Decision: AuditDecisionSuccess, ReasonCode: reason, PolicyVersionID: policyID, CreatedAt: time.Now().UTC()})
}
