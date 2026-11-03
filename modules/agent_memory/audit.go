package agent_memory

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	AuditEventProfileUpdate = "profile.update"
	AuditEventGrantReplace  = "grant.replace"
	AuditEventAuthDecision  = "auth.decision"
	AuditEventGovernanceRun = "governance.run"
	AuditEventOfferChange   = "offer.change"
	AuditEventAcceptance    = "offer.acceptance"

	AuditDecisionAllow   = "allow"
	AuditDecisionDeny    = "deny"
	AuditDecisionSuccess = "success"
	AuditDecisionError   = "error"
)

var (
	ErrInvalidAuditEvent = errors.New("invalid agent memory audit event")
	ErrAuditPersistence  = errors.New("agent memory audit persistence failed")
	auditOpaquePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:@./-]*$`)
	auditCodePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
)

var allowedAuditMetadata = map[string]int{
	"changed_fields": 120,
	"grant_count":    12,
	"operation":      40,
	"outcome":        40,
	"scope_type":     20,
	"mode":           12,
	"proposed_count": 12,
	"applied_count":  12,
}

var allowedAuditReasons = map[string]struct{}{
	"profile_updated": {}, "grants_replaced": {}, "management_forbidden": {},
	"profile_not_found": {}, "version_conflict": {}, "profile_update_failed": {},
	"invalid_grant": {}, "grant_scope_forbidden": {}, "grant_replace_failed": {},
	"invalid_request": {},
	"offer_created":   {}, "offer_revised": {}, "offer_revoked": {},
	"offer_accepted": {}, "offer_declined": {}, "acceptance_withdrawn": {},
	"governance_dry_run": {}, "governance_applied": {}, "governance_idempotent": {},
	"governance_forbidden": {}, "governance_conflict": {}, "governance_failed": {},
	string(PolicyReasonAllowed): {}, string(PolicyReasonForbidden): {},
}

type MemoryAuditEvent struct {
	ID                string
	EventType         string
	PrincipalAgentUID string
	PrincipalUserUID  string
	SpaceID           string
	ResourceType      string
	ResourceID        string
	Capability        Capability
	Decision          string
	ReasonCode        string
	RequestID         string
	CredentialJTIHash string
	PolicyVersionID   string
	ContentHash       string
	MetadataJSON      []byte
	CreatedAt         time.Time
}

type AuditRecord struct {
	PrincipalAgentUID string
	PrincipalUserUID  string
	SpaceID           string
	ResourceID        string
	Capability        Capability
	Decision          string
	ReasonCode        string
	RequestID         string
	CredentialJTIHash string
	PolicyVersionID   string
	ContentHash       string
	Metadata          map[string]string
}

type AuditStore interface {
	AppendAuditEvent(MemoryAuditEvent) error
}

type AuditService struct {
	store AuditStore
	now   func() time.Time
	newID func() string
}

func NewAuditService(store AuditStore) *AuditService {
	return &AuditService{
		store: store,
		now:   time.Now,
		newID: func() string { return uuid.NewString() },
	}
}

func (service *AuditService) ProfileDecision(record AuditRecord) (MemoryAuditEvent, error) {
	return service.newEvent(AuditEventProfileUpdate, "agent_profile", record)
}

func (service *AuditService) GrantDecision(record AuditRecord) (MemoryAuditEvent, error) {
	return service.newEvent(AuditEventGrantReplace, "agent_grants", record)
}

func (service *AuditService) AuthDecision(record AuditRecord) (MemoryAuditEvent, error) {
	return service.newEvent(AuditEventAuthDecision, "memory_bank", record)
}

func (service *AuditService) GovernanceDecision(record AuditRecord) (MemoryAuditEvent, error) {
	return service.newEvent(AuditEventGovernanceRun, "governance_run", record)
}

func (service *AuditService) Record(event MemoryAuditEvent) error {
	if service == nil || service.store == nil {
		return ErrAuditPersistence
	}
	if err := event.Validate(); err != nil {
		return err
	}
	if err := service.store.AppendAuditEvent(event); err != nil {
		return ErrAuditPersistence
	}
	return nil
}

func (service *AuditService) newEvent(eventType, resourceType string, record AuditRecord) (MemoryAuditEvent, error) {
	if service == nil {
		return MemoryAuditEvent{}, ErrInvalidAuditEvent
	}
	metadata, err := sanitizeAuditMetadata(record.Metadata)
	if err != nil {
		return MemoryAuditEvent{}, err
	}
	event := MemoryAuditEvent{
		ID: service.newID(), EventType: eventType,
		PrincipalAgentUID: strings.TrimSpace(record.PrincipalAgentUID), PrincipalUserUID: strings.TrimSpace(record.PrincipalUserUID),
		SpaceID: strings.TrimSpace(record.SpaceID), ResourceType: resourceType, ResourceID: strings.TrimSpace(record.ResourceID),
		Capability: record.Capability, Decision: strings.TrimSpace(record.Decision), ReasonCode: strings.TrimSpace(record.ReasonCode),
		RequestID: strings.TrimSpace(record.RequestID), CredentialJTIHash: strings.ToLower(strings.TrimSpace(record.CredentialJTIHash)),
		PolicyVersionID: strings.TrimSpace(record.PolicyVersionID), ContentHash: strings.ToLower(strings.TrimSpace(record.ContentHash)),
		MetadataJSON: metadata, CreatedAt: service.now().UTC(),
	}
	if err := event.Validate(); err != nil {
		return MemoryAuditEvent{}, err
	}
	return event, nil
}

func (event MemoryAuditEvent) Validate() error {
	if !validAuditCode(event.EventType, 80) || !validAuditOpaque(event.ID, 40) ||
		!validAuditCode(event.ResourceType, 40) || !validAuditOpaque(event.ResourceID, 120) ||
		!validAuditCode(event.Decision, 30) || !validAuditCode(event.ReasonCode, 80) {
		return ErrInvalidAuditEvent
	}
	if !knownAuditEventType(event.EventType) || !knownAuditResourceType(event.ResourceType) || !knownAuditDecision(event.Decision) {
		return ErrInvalidAuditEvent
	}
	if _, ok := allowedAuditReasons[event.ReasonCode]; !ok {
		return ErrInvalidAuditEvent
	}
	if event.PrincipalAgentUID == "" && event.PrincipalUserUID == "" {
		return ErrInvalidAuditEvent
	}
	for _, value := range []struct {
		value string
		limit int
	}{
		{event.PrincipalAgentUID, 40}, {event.PrincipalUserUID, 40}, {event.SpaceID, 40},
		{event.RequestID, 80}, {event.PolicyVersionID, 40},
	} {
		if value.value != "" && !validAuditOpaque(value.value, value.limit) {
			return ErrInvalidAuditEvent
		}
	}
	if event.Capability != "" && !knownCapability(event.Capability) {
		return ErrInvalidAuditEvent
	}
	if event.CredentialJTIHash != "" && !validSHA256(event.CredentialJTIHash) {
		return ErrInvalidAuditEvent
	}
	if event.ContentHash != "" && !validSHA256(event.ContentHash) {
		return ErrInvalidAuditEvent
	}
	if len(event.MetadataJSON) > 512 {
		return ErrInvalidAuditEvent
	}
	if len(event.MetadataJSON) > 0 {
		var metadata map[string]string
		if json.Unmarshal(event.MetadataJSON, &metadata) != nil {
			return ErrInvalidAuditEvent
		}
		if _, err := sanitizeAuditMetadata(metadata); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeAuditMetadata(metadata map[string]string) ([]byte, error) {
	if len(metadata) == 0 {
		return nil, nil
	}
	if len(metadata) > len(allowedAuditMetadata) {
		return nil, ErrInvalidAuditEvent
	}
	clean := make(map[string]string, len(metadata))
	for key, value := range metadata {
		limit, ok := allowedAuditMetadata[key]
		value = strings.TrimSpace(value)
		if !ok || value == "" || len(value) > limit || containsCredentialLikeValue(value) || !validAuditMetadataValue(key, value) {
			return nil, ErrInvalidAuditEvent
		}
		clean[key] = value
	}
	encoded, err := json.Marshal(clean)
	if err != nil || len(encoded) > 512 {
		return nil, ErrInvalidAuditEvent
	}
	return encoded, nil
}

func knownAuditEventType(value string) bool {
	return value == AuditEventProfileUpdate || value == AuditEventGrantReplace || value == AuditEventAuthDecision || value == AuditEventGovernanceRun
}

func knownAuditResourceType(value string) bool {
	return value == "agent_profile" || value == "agent_grants" || value == "memory_bank" || value == "governance_run"
}

func knownAuditDecision(value string) bool {
	return value == AuditDecisionAllow || value == AuditDecisionDeny || value == AuditDecisionSuccess || value == AuditDecisionError
}

func validAuditMetadataValue(key, value string) bool {
	switch key {
	case "changed_fields":
		return value == "enabled" || value == "include_own_tentative" || value == "enabled,include_own_tentative"
	case "grant_count":
		if len(value) > 6 {
			return false
		}
		for _, character := range value {
			if character < '0' || character > '9' {
				return false
			}
		}
		return true
	case "operation":
		return value == "update_profile" || value == "replace_grants" || value == "authorize" || value == "governance_run"
	case "outcome":
		return knownAuditDecision(value)
	case "mode":
		return value == "dry_run" || value == "apply"
	case "proposed_count", "applied_count":
		if len(value) == 0 || len(value) > 12 {
			return false
		}
		for _, character := range value {
			if character < '0' || character > '9' {
				return false
			}
		}
		return true
	case "scope_type":
		return value == string(ProfileScopePlatform) || value == string(ProfileScopeSpace) || value == string(BankScopeAgent) || value == string(BankScopeOrganization)
	default:
		return false
	}
}

func validAuditOpaque(value string, limit int) bool {
	return value != "" && len(value) <= limit && auditOpaquePattern.MatchString(value) && !containsCredentialLikeValue(value)
}

func validAuditCode(value string, limit int) bool {
	return value != "" && len(value) <= limit && auditCodePattern.MatchString(value)
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func containsCredentialLikeValue(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "bearer ") || strings.Contains(lower, "authorization:") || strings.Contains(lower, "password=") || strings.Contains(lower, "access_token") {
		return true
	}
	parts := strings.Split(value, ".")
	return len(parts) == 3 && len(parts[0]) >= 8 && len(parts[1]) >= 8 && len(parts[2]) >= 8
}
