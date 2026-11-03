package agent_memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/Mininglamp-OSS/octo-server/pkg/i18n"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type ProfileCatalog interface {
	FindProfileRecord(string) (*AgentMemoryProfileRecord, error)
	ListProfileRecords(ProfileScopeType, string) ([]AgentMemoryProfileRecord, error)
}

type agentMemoryProfileResponse struct {
	AgentUID            string           `json:"agent_uid"`
	RobotID             string           `json:"robot_id"`
	DisplayName         string           `json:"display_name"`
	OwnerUID            string           `json:"owner_uid"`
	ScopeType           ProfileScopeType `json:"scope_type"`
	SpaceID             *string          `json:"space_id,omitempty"`
	Enabled             bool             `json:"enabled"`
	Status              string           `json:"status"`
	TrustTier           string           `json:"trust_tier"`
	CanManage           bool             `json:"can_manage"`
	Version             int64            `json:"version"`
	IncludeOwnTentative bool             `json:"include_own_tentative"`
	Eligibility         string           `json:"eligibility"`
	Readiness           string           `json:"readiness"`
	BlockerReasonCodes  []string         `json:"blocker_reason_codes"`
	AllowedActions      []string         `json:"allowed_actions"`
	PendingOfferCount   int              `json:"pending_offer_count"`
}

type updateProfileRequest struct {
	Enabled             *bool  `json:"enabled"`
	IncludeOwnTentative *bool  `json:"include_own_tentative"`
	ExpectedVersion     *int64 `json:"expected_version"`
}

type replaceGrantsRequest struct {
	ExpectedVersion *int64              `json:"expected_version"`
	AuditReason     string              `json:"audit_reason"`
	Grants          []grantInputRequest `json:"grants"`
}

type grantInputRequest struct {
	PrincipalType PrincipalType `json:"principal_type"`
	PrincipalID   string        `json:"principal_id"`
	BankID        string        `json:"bank_id"`
	Capability    Capability    `json:"capability"`
	Effect        GrantEffect   `json:"effect"`
	ValidFrom     *time.Time    `json:"valid_from"`
	ValidUntil    *time.Time    `json:"valid_until"`
}

type grantResponse struct {
	ID            string        `json:"id"`
	PrincipalType PrincipalType `json:"principal_type"`
	PrincipalID   string        `json:"principal_id"`
	BankID        string        `json:"bank_id"`
	Capability    Capability    `json:"capability"`
	Effect        GrantEffect   `json:"effect"`
	ValidFrom     *time.Time    `json:"valid_from,omitempty"`
	ValidUntil    *time.Time    `json:"valid_until,omitempty"`
}

type BankManagementStore interface {
	FindBankByID(string) (*MemoryBank, error)
	ListBanks() ([]MemoryBank, error)
	UpdateBank(*MemoryBank, int64) error
}

type LogicalBankCreator interface {
	CreateLogicalBank(context.Context, LogicalBankCreate) (*MemoryBank, error)
}

type OrganizationFacts interface {
	OrganizationExists(context.Context, string) (bool, error)
}

type SpaceOwnershipFacts interface {
	SpaceOwnerUID(context.Context, string) (string, error)
}

type LogicalBankCreate struct {
	ID             string
	DisplayName    string
	BankType       string
	ScopeType      BankScopeType
	ScopeID        string
	OwnerUID       string
	CreatedByUID   string
	Sensitivity    string
	RetentionClass string
}

type LogicalBankUpdate struct {
	Bank            MemoryBank
	ExpectedVersion int64
}

type LogicalBankUpdater interface {
	UpdateLogicalBank(context.Context, LogicalBankUpdate) (*MemoryBank, error)
}

type bankResponse struct {
	ID             string        `json:"id"`
	DisplayName    string        `json:"display_name"`
	BankType       string        `json:"bank_type"`
	ScopeType      BankScopeType `json:"scope_type"`
	ScopeID        string        `json:"scope_id"`
	Status         string        `json:"status"`
	Sensitivity    string        `json:"sensitivity"`
	RetentionClass string        `json:"retention_class"`
	Legacy         bool          `json:"legacy"`
	Version        int64         `json:"version"`
	CreatedByUID   string        `json:"created_by_uid"`
	AllowedActions []string      `json:"allowed_actions"`
}

type createBankRequest struct {
	DisplayName    string        `json:"display_name"`
	BankType       string        `json:"bank_type"`
	ScopeType      BankScopeType `json:"scope_type"`
	ScopeID        string        `json:"scope_id"`
	Sensitivity    string        `json:"sensitivity"`
	RetentionClass string        `json:"retention_class"`
}

type updateBankRequest struct {
	DisplayName     *string `json:"display_name"`
	Status          *string `json:"status"`
	RetentionClass  *string `json:"retention_class"`
	Sensitivity     *string `json:"sensitivity"`
	ExpectedVersion *int64  `json:"expected_version"`
}

func (runtime *Runtime) listBanks(c *wkhttp.Context) {
	if runtime.Banks == nil {
		runtime.respondInternal(c, "list banks", errors.New("bank management store unavailable"))
		return
	}
	caller := managementCaller(c)
	spaceID := strings.TrimSpace(c.Query("space_id"))
	if spaceID != "" {
		decision, err := runtime.Access.AuthorizeSpace(c.Request.Context(), caller, spaceID)
		if err != nil {
			runtime.respondInternal(c, "authorize bank list", err)
			return
		}
		if !decision.Visible {
			respondForbidden(c)
			return
		}
		if !decision.CanManage {
			profiles, listErr := runtime.Catalog.ListProfileRecords(ProfileScopeSpace, spaceID)
			if listErr != nil {
				runtime.respondInternal(c, "authorize bank list profiles", listErr)
				return
			}
			for _, profile := range profiles {
				profileDecision, authErr := runtime.Access.AuthorizeProfile(c.Request.Context(), caller, profile.AgentMemoryProfile)
				if authErr != nil {
					runtime.respondInternal(c, "authorize bank list profile", authErr)
					return
				}
				if profileDecision.CanManage {
					decision.CanManage = true
					break
				}
			}
			if !decision.CanManage {
				respondForbidden(c)
				return
			}
		}
	} else if !isPlatformAdmin(caller) {
		respondForbidden(c)
		return
	}
	banks, err := runtime.Banks.ListBanks()
	if err != nil {
		runtime.respondInternal(c, "list banks", err)
		return
	}
	responses := make([]bankResponse, 0, len(banks))
	for _, bank := range banks {
		visible, _, err := runtime.authorizeBank(c, caller, bank)
		if err != nil {
			runtime.respondInternal(c, "authorize listed bank", err)
			return
		}
		if !visible || (spaceID != "" && !runtime.bankBelongsToSpace(bank, spaceID)) {
			continue
		}
		responses = append(responses, newBankResponse(bank))
	}
	c.Response(map[string]interface{}{"banks": responses})
}

func (runtime *Runtime) createBank(c *wkhttp.Context) {
	var request createBankRequest
	if err := decodeStrictJSON(c, &request); err != nil || !validBankCreate(request) {
		respondInvalid(c)
		return
	}
	caller := managementCaller(c)
	ownerUID, visible, canManage, err := runtime.authorizeBankCreate(c, caller, request)
	if err != nil {
		runtime.respondInternal(c, "authorize bank create", err)
		return
	}
	if !visible {
		respondNotFound(c)
		return
	}
	if !canManage {
		respondForbidden(c)
		return
	}
	if runtime.BankCreator == nil {
		runtime.respondInternal(c, "create logical bank", errors.New("logical bank creation boundary unavailable"))
		return
	}
	bank, err := runtime.BankCreator.CreateLogicalBank(c.Request.Context(), LogicalBankCreate{
		ID: uuid.NewString(), DisplayName: strings.TrimSpace(request.DisplayName), BankType: request.BankType,
		ScopeType: request.ScopeType, ScopeID: strings.TrimSpace(request.ScopeID), OwnerUID: ownerUID, CreatedByUID: caller.UID,
		Sensitivity: request.Sensitivity, RetentionClass: strings.TrimSpace(request.RetentionClass),
	})
	if err != nil {
		runtime.respondInternal(c, "create logical bank", err)
		return
	}
	c.ResponseWithStatus(http.StatusCreated, newBankResponse(*bank))
}

func (runtime *Runtime) updateBank(c *wkhttp.Context) {
	bank, decision, ok := runtime.authorizedBank(c)
	if !ok {
		return
	}
	if !decision.CanManage {
		respondForbidden(c)
		return
	}
	var request updateBankRequest
	if err := decodeStrictJSON(c, &request); err != nil || !validBankUpdate(request, bank.Status) {
		respondInvalid(c)
		return
	}
	if bank.Version != *request.ExpectedVersion {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	}
	applyBankUpdate(bank, request)
	updated := bank
	var err error
	if updater, ok := runtime.Banks.(LogicalBankUpdater); ok {
		updated, err = updater.UpdateLogicalBank(c.Request.Context(), LogicalBankUpdate{Bank: *bank, ExpectedVersion: *request.ExpectedVersion})
	} else {
		err = runtime.Banks.UpdateBank(bank, *request.ExpectedVersion)
		bank.Version++
	}
	if errors.Is(err, ErrVersionConflict) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	} else if err != nil {
		runtime.respondInternal(c, "update bank", err)
		return
	}
	c.Response(newBankResponse(*updated))
}

func (runtime *Runtime) authorizedBank(c *wkhttp.Context) (*MemoryBank, ManagementDecision, bool) {
	if runtime.Banks == nil {
		runtime.respondInternal(c, "load bank", errors.New("bank management store unavailable"))
		return nil, ManagementDecision{}, false
	}
	bankID := strings.TrimSpace(c.Param("bank_id"))
	if bankID == "" {
		respondNotFound(c)
		return nil, ManagementDecision{}, false
	}
	bank, err := runtime.Banks.FindBankByID(bankID)
	if errors.Is(err, ErrBankNotFound) {
		respondNotFound(c)
		return nil, ManagementDecision{}, false
	}
	if err != nil {
		runtime.respondInternal(c, "load bank", err)
		return nil, ManagementDecision{}, false
	}
	visible, canManage, err := runtime.authorizeBank(c, managementCaller(c), *bank)
	if err != nil {
		runtime.respondInternal(c, "authorize bank", err)
		return nil, ManagementDecision{}, false
	}
	if !visible {
		respondNotFound(c)
		return nil, ManagementDecision{}, false
	}
	return bank, ManagementDecision{Visible: true, CanManage: canManage}, true
}

func (runtime *Runtime) authorizeBank(c *wkhttp.Context, caller ManagementCaller, bank MemoryBank) (bool, bool, error) {
	if isPlatformAdmin(caller) {
		return true, true, nil
	}
	switch bank.ScopeType {
	case BankScopeAgent:
		record, err := runtime.Catalog.FindProfileRecord(bank.ScopeID)
		if errors.Is(err, ErrProfileNotFound) {
			return false, false, nil
		}
		if err != nil {
			return false, false, err
		}
		decision, err := runtime.Access.AuthorizeProfile(c.Request.Context(), caller, record.AgentMemoryProfile)
		return decision.Visible, decision.CanManage, err
	case BankScopeSpace:
		decision, err := runtime.Access.AuthorizeSpace(c.Request.Context(), caller, bank.ScopeID)
		return decision.Visible, decision.CanManage, err
	case BankScopeOrganization:
		admin := isPlatformAdmin(caller)
		return admin, admin, nil
	default:
		return false, false, nil
	}
}

func (runtime *Runtime) authorizeBankCreate(c *wkhttp.Context, caller ManagementCaller, request createBankRequest) (string, bool, bool, error) {
	scopeID := strings.TrimSpace(request.ScopeID)
	switch request.ScopeType {
	case BankScopeAgent:
		record, err := runtime.Catalog.FindProfileRecord(scopeID)
		if errors.Is(err, ErrProfileNotFound) {
			return "", false, false, nil
		}
		if err != nil {
			return "", false, false, err
		}
		if isPlatformAdmin(caller) {
			return record.OwnerUID, true, true, nil
		}
		decision, err := runtime.Access.AuthorizeProfile(c.Request.Context(), caller, record.AgentMemoryProfile)
		return record.OwnerUID, decision.Visible, decision.CanManage, err
	case BankScopeSpace:
		if isPlatformAdmin(caller) {
			if runtime.SpaceFacts == nil {
				return "", false, false, ErrManagementFactsUnavailable
			}
			ownerUID, err := runtime.SpaceFacts.SpaceOwnerUID(c.Request.Context(), scopeID)
			return ownerUID, err == nil, err == nil, err
		}
		decision, err := runtime.Access.AuthorizeSpace(c.Request.Context(), caller, scopeID)
		if err != nil || !decision.Visible {
			return "", decision.Visible, decision.CanManage, err
		}
		if runtime.SpaceFacts == nil {
			return "", false, false, ErrManagementFactsUnavailable
		}
		ownerUID, err := runtime.SpaceFacts.SpaceOwnerUID(c.Request.Context(), scopeID)
		return ownerUID, decision.Visible, decision.CanManage, err
	case BankScopeOrganization:
		if !isPlatformAdmin(caller) {
			return "", false, false, nil
		}
		if runtime.OrganizationFacts == nil {
			return "", false, false, ErrManagementFactsUnavailable
		}
		exists, err := runtime.OrganizationFacts.OrganizationExists(c.Request.Context(), scopeID)
		return caller.UID, exists, exists, err
	default:
		return "", false, false, nil
	}
}

func (runtime *Runtime) bankBelongsToSpace(bank MemoryBank, spaceID string) bool {
	if bank.ScopeType == BankScopeSpace {
		return bank.ScopeID == spaceID
	}
	if bank.ScopeType != BankScopeAgent {
		return false
	}
	record, err := runtime.Catalog.FindProfileRecord(bank.ScopeID)
	return err == nil && record.SpaceID != nil && *record.SpaceID == spaceID
}

func validBankCreate(request createBankRequest) bool {
	displayName := strings.TrimSpace(request.DisplayName)
	retentionClass := strings.TrimSpace(request.RetentionClass)
	if displayName == "" || len(displayName) > 120 || retentionClass == "" || len(retentionClass) > 40 || strings.TrimSpace(request.ScopeID) == "" {
		return false
	}
	if request.Sensitivity != "internal" && request.Sensitivity != "confidential" && request.Sensitivity != "restricted" {
		return false
	}
	switch request.ScopeType {
	case BankScopeAgent:
		return request.BankType == "private" || request.BankType == "tentative"
	case BankScopeSpace:
		return request.BankType == "shared" || request.BankType == "verified"
	case BankScopeOrganization:
		return request.BankType == "verified"
	default:
		return false
	}
}

func validBankUpdate(request updateBankRequest, currentStatus string) bool {
	if request.ExpectedVersion == nil || *request.ExpectedVersion < 0 || (request.DisplayName == nil && request.Status == nil && request.RetentionClass == nil && request.Sensitivity == nil) {
		return false
	}
	if request.DisplayName != nil && (strings.TrimSpace(*request.DisplayName) == "" || len(strings.TrimSpace(*request.DisplayName)) > 120) {
		return false
	}
	if request.RetentionClass != nil && (strings.TrimSpace(*request.RetentionClass) == "" || len(strings.TrimSpace(*request.RetentionClass)) > 40) {
		return false
	}
	if request.Sensitivity != nil && *request.Sensitivity != "internal" && *request.Sensitivity != "confidential" && *request.Sensitivity != "restricted" {
		return false
	}
	return request.Status == nil || validBankStatusTransition(currentStatus, *request.Status)
}

func validBankStatusTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case "provisioning":
		return to == "disabled"
	case "active":
		return to == "disabled"
	case "disabled":
		return to == "active" || to == "archived"
	case "archived":
		return to == "disabled"
	default:
		return false
	}
}

func applyBankUpdate(bank *MemoryBank, request updateBankRequest) {
	if request.DisplayName != nil {
		bank.DisplayName = strings.TrimSpace(*request.DisplayName)
	}
	if request.RetentionClass != nil {
		bank.RetentionClass = strings.TrimSpace(*request.RetentionClass)
	}
	if request.Sensitivity != nil {
		bank.Sensitivity = *request.Sensitivity
	}
	if request.Status != nil {
		bank.Status = *request.Status
		if bank.Status == "active" {
			bank.DisabledAt = nil
		} else {
			now := time.Now().UTC()
			bank.DisabledAt = &now
		}
	}
}

func newBankResponse(bank MemoryBank) bankResponse {
	return bankResponse{
		ID: bank.ID, DisplayName: bank.DisplayName, BankType: bank.BankType, ScopeType: bank.ScopeType,
		ScopeID: bank.ScopeID, Status: bank.Status, Sensitivity: bank.Sensitivity,
		RetentionClass: bank.RetentionClass, Legacy: bank.Legacy, Version: bank.Version,
		CreatedByUID: bank.CreatedByUID, AllowedActions: []string{"view"},
	}
}

func (runtime *Runtime) listProfiles(c *wkhttp.Context) {
	caller := managementCaller(c)
	spaceID := strings.TrimSpace(c.Query("space_id"))
	scopeType := ProfileScopePlatform
	if spaceID != "" {
		scopeType = ProfileScopeSpace
		decision, err := runtime.Access.AuthorizeSpace(c.Request.Context(), caller, spaceID)
		if err != nil {
			runtime.respondInternal(c, "authorize profile list", err)
			return
		}
		if !decision.Visible {
			respondForbidden(c)
			return
		}
	} else if !isPlatformAdmin(caller) {
		respondForbidden(c)
		return
	}

	records, err := runtime.Catalog.ListProfileRecords(scopeType, spaceID)
	if err != nil {
		runtime.respondInternal(c, "list profiles", err)
		return
	}
	responses := make([]agentMemoryProfileResponse, 0, len(records))
	for _, record := range records {
		decision, err := runtime.Access.AuthorizeProfile(c.Request.Context(), caller, record.AgentMemoryProfile)
		if err != nil {
			runtime.respondInternal(c, "authorize listed profile", err)
			return
		}
		if decision.Visible {
			responses = append(responses, runtime.profileResponse(record, decision.CanManage))
		}
	}
	c.Response(map[string]interface{}{"agents": responses})
}

func (runtime *Runtime) getProfile(c *wkhttp.Context) {
	record, decision, ok := runtime.authorizedProfile(c)
	if !ok {
		return
	}
	c.Response(runtime.profileResponse(*record, decision.CanManage))
}

func (runtime *Runtime) updateProfile(c *wkhttp.Context) {
	record, decision, ok := runtime.authorizedProfile(c)
	if !ok {
		return
	}
	if !decision.CanManage {
		if !runtime.recordManagementDecision(c, AuditEventProfileUpdate, record.AgentMemoryProfile, AuditDecisionDeny, "management_forbidden", nil) {
			return
		}
		respondForbidden(c)
		return
	}
	var request updateProfileRequest
	if err := decodeStrictJSON(c, &request); err != nil || request.ExpectedVersion == nil || *request.ExpectedVersion < 0 {
		if !runtime.recordManagementDecision(c, AuditEventProfileUpdate, record.AgentMemoryProfile, AuditDecisionDeny, "invalid_request", nil) {
			return
		}
		respondInvalid(c)
		return
	}
	if request.Enabled != nil && *request.Enabled {
		respondInvalidField(c, "enabled")
		return
	}
	metadata := map[string]string{"operation": "update_profile"}
	if changedFields := profileChangedFields(request); changedFields != "" {
		metadata["changed_fields"] = changedFields
	}
	if runtime.Audits != nil {
		event, err := runtime.profileAuditEvent(c, record.AgentMemoryProfile, AuditDecisionSuccess, "profile_updated", metadata)
		if err != nil {
			runtime.respondInternal(c, "prepare profile audit", err)
			return
		}
		profile, err := runtime.Profiles.UpdateProfileWithAudit(c.Request.Context(), record.AgentMemoryProfile, request.Enabled, request.IncludeOwnTentative, *request.ExpectedVersion, event)
		runtime.finishProfileUpdate(c, profile, err, record.AgentMemoryProfile)
		return
	}
	profile, err := runtime.Profiles.UpdateProfile(c.Request.Context(), record.AgentMemoryProfile, request.Enabled, request.IncludeOwnTentative, *request.ExpectedVersion)
	runtime.finishProfileUpdate(c, profile, err, record.AgentMemoryProfile)
}

func (runtime *Runtime) finishProfileUpdate(c *wkhttp.Context, profile *AgentMemoryProfile, err error, original AgentMemoryProfile) {
	if errors.Is(err, ErrProfileNotFound) {
		if !runtime.recordManagementDecision(c, AuditEventProfileUpdate, original, AuditDecisionError, "profile_not_found", nil) {
			return
		}
		respondNotFound(c)
		return
	}
	if errors.Is(err, ErrVersionConflict) {
		if !runtime.recordManagementDecision(c, AuditEventProfileUpdate, original, AuditDecisionDeny, "version_conflict", nil) {
			return
		}
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	}
	if err != nil {
		if !errors.Is(err, ErrAuditPersistence) {
			if !runtime.recordManagementDecision(c, AuditEventProfileUpdate, original, AuditDecisionError, "profile_update_failed", nil) {
				return
			}
		}
		runtime.respondInternal(c, "update profile", err)
		return
	}
	updatedRecord, err := runtime.Catalog.FindProfileRecord(profile.AgentUID)
	if err != nil {
		runtime.respondInternal(c, "reload updated profile", err)
		return
	}
	c.Response(runtime.profileResponse(*updatedRecord, true))
}

func (runtime *Runtime) replaceGrants(c *wkhttp.Context) {
	record, decision, ok := runtime.authorizedProfile(c)
	if !ok {
		return
	}
	if !decision.CanManage {
		if !runtime.recordManagementDecision(c, AuditEventGrantReplace, record.AgentMemoryProfile, AuditDecisionDeny, "management_forbidden", nil) {
			return
		}
		respondForbidden(c)
		return
	}
	var request replaceGrantsRequest
	if err := decodeStrictJSON(c, &request); err != nil || request.ExpectedVersion == nil || request.Grants == nil || strings.TrimSpace(request.AuditReason) == "" {
		if !runtime.recordManagementDecision(c, AuditEventGrantReplace, record.AgentMemoryProfile, AuditDecisionDeny, "invalid_request", nil) {
			return
		}
		respondInvalid(c)
		return
	}
	inputs := make([]GrantInput, 0, len(request.Grants))
	for _, grant := range request.Grants {
		inputs = append(inputs, GrantInput{
			PrincipalType: grant.PrincipalType, PrincipalID: grant.PrincipalID, BankID: grant.BankID,
			Capability: grant.Capability, Effect: grant.Effect, ValidFrom: grant.ValidFrom, ValidUntil: grant.ValidUntil,
		})
	}
	metadata := map[string]string{"operation": "replace_grants", "grant_count": fmt.Sprintf("%d", len(inputs))}
	var grants []MemoryGrant
	var err error
	if runtime.Audits != nil {
		event, auditErr := runtime.grantAuditEvent(c, record.AgentMemoryProfile, AuditDecisionSuccess, "grants_replaced", metadata)
		if auditErr != nil {
			runtime.respondInternal(c, "prepare grant audit", auditErr)
			return
		}
		grants, err = runtime.Grants.ReplaceProfileGrantsWithAudit(record.AgentMemoryProfile, managementCaller(c).UID, request.AuditReason, *request.ExpectedVersion, inputs, event)
	} else {
		grants, err = runtime.Grants.ReplaceProfileGrants(record.AgentMemoryProfile, managementCaller(c).UID, request.AuditReason, *request.ExpectedVersion, inputs)
	}
	if errors.Is(err, ErrInvalidGrant) {
		if !runtime.recordManagementDecision(c, AuditEventGrantReplace, record.AgentMemoryProfile, AuditDecisionDeny, "invalid_grant", nil) {
			return
		}
		respondInvalid(c)
		return
	}
	if errors.Is(err, ErrGrantScope) {
		if !runtime.recordManagementDecision(c, AuditEventGrantReplace, record.AgentMemoryProfile, AuditDecisionDeny, "grant_scope_forbidden", nil) {
			return
		}
		respondForbidden(c)
		return
	}
	if errors.Is(err, ErrVersionConflict) {
		if !runtime.recordManagementDecision(c, AuditEventGrantReplace, record.AgentMemoryProfile, AuditDecisionDeny, "version_conflict", nil) {
			return
		}
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	}
	if err != nil {
		if !errors.Is(err, ErrAuditPersistence) {
			if !runtime.recordManagementDecision(c, AuditEventGrantReplace, record.AgentMemoryProfile, AuditDecisionError, "grant_replace_failed", nil) {
				return
			}
		}
		runtime.respondInternal(c, "replace profile grants", err)
		return
	}
	responses := make([]grantResponse, 0, len(grants))
	for _, grant := range grants {
		responses = append(responses, grantResponse{
			ID: grant.ID, PrincipalType: PrincipalType(grant.PrincipalType), PrincipalID: grant.PrincipalID,
			BankID: grant.BankID, Capability: Capability(grant.Capability), Effect: GrantEffect(grant.Effect),
			ValidFrom: cloneTimePointer(grant.ValidFrom), ValidUntil: cloneTimePointer(grant.ValidUntil),
		})
	}
	c.Response(map[string]interface{}{"grants": responses, "version": *request.ExpectedVersion + 1})
}

func (runtime *Runtime) profileAuditEvent(c *wkhttp.Context, profile AgentMemoryProfile, decision, reason string, metadata map[string]string) (MemoryAuditEvent, error) {
	return runtime.Audits.ProfileDecision(managementAuditRecord(c, profile, decision, reason, metadata))
}

func (runtime *Runtime) grantAuditEvent(c *wkhttp.Context, profile AgentMemoryProfile, decision, reason string, metadata map[string]string) (MemoryAuditEvent, error) {
	return runtime.Audits.GrantDecision(managementAuditRecord(c, profile, decision, reason, metadata))
}

func (runtime *Runtime) recordManagementDecision(c *wkhttp.Context, eventType string, profile AgentMemoryProfile, decision, reason string, metadata map[string]string) bool {
	if runtime.Audits == nil {
		return true
	}
	var event MemoryAuditEvent
	var err error
	if eventType == AuditEventGrantReplace {
		event, err = runtime.grantAuditEvent(c, profile, decision, reason, metadata)
	} else {
		event, err = runtime.profileAuditEvent(c, profile, decision, reason, metadata)
	}
	if err == nil {
		err = runtime.Audits.Record(event)
	}
	if err != nil {
		runtime.respondInternal(c, "record management audit", err)
		return false
	}
	return true
}

func managementAuditRecord(c *wkhttp.Context, profile AgentMemoryProfile, decision, reason string, metadata map[string]string) AuditRecord {
	spaceID := ""
	if profile.SpaceID != nil {
		spaceID = *profile.SpaceID
	}
	requestID := strings.TrimSpace(c.Request.Header.Get("X-Request-ID"))
	if requestID != "" && !validAuditOpaque(requestID, 80) {
		requestID = ""
	}
	return AuditRecord{
		PrincipalUserUID: managementCaller(c).UID, SpaceID: spaceID, ResourceID: profile.AgentUID,
		Decision: decision, ReasonCode: reason, RequestID: requestID, PolicyVersionID: profile.PolicyVersionID, Metadata: metadata,
	}
}

func profileChangedFields(request updateProfileRequest) string {
	fields := make([]string, 0, 2)
	if request.Enabled != nil {
		fields = append(fields, "enabled")
	}
	if request.IncludeOwnTentative != nil {
		fields = append(fields, "include_own_tentative")
	}
	return strings.Join(fields, ",")
}

func decodeStrictJSON(c *wkhttp.Context, target interface{}) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func (runtime *Runtime) authorizedProfile(c *wkhttp.Context) (*AgentMemoryProfileRecord, ManagementDecision, bool) {
	agentUID := strings.TrimSpace(c.Param("agent_uid"))
	if agentUID == "" {
		respondNotFound(c)
		return nil, ManagementDecision{}, false
	}
	record, err := runtime.Catalog.FindProfileRecord(agentUID)
	if errors.Is(err, ErrProfileNotFound) {
		respondNotFound(c)
		return nil, ManagementDecision{}, false
	}
	if err != nil {
		runtime.respondInternal(c, "load profile", err)
		return nil, ManagementDecision{}, false
	}
	decision, err := runtime.Access.AuthorizeProfile(c.Request.Context(), managementCaller(c), record.AgentMemoryProfile)
	if err != nil {
		runtime.respondInternal(c, "authorize profile", err)
		return nil, ManagementDecision{}, false
	}
	if !decision.Visible {
		respondNotFound(c)
		return nil, ManagementDecision{}, false
	}
	return record, decision, true
}

func (runtime *Runtime) profileResponse(record AgentMemoryProfileRecord, canManage bool) agentMemoryProfileResponse {
	eligibility, readiness := "eligible", "setup_required"
	blockers := []string{}
	if record.Status == "revoked" || record.SpaceID == nil || strings.TrimSpace(valueOrEmpty(record.SpaceID)) == "" {
		eligibility, readiness, blockers = "blocked", "blocked", []string{"ROBOT_OR_SPACE_INELIGIBLE"}
	} else if record.Enabled {
		readiness, blockers = "blocked", []string{"FRESH_PROBE_REQUIRED"}
		if runtime != nil && runtime.Enablement != nil {
			readiness, blockers = runtime.Enablement.ProfileReadiness(record.RobotID)
		}
	}
	actions := []string{"view"}
	if canManage {
		actions = append(actions, "update_profile", "create_agent_bank", "review_offers", "explain_permissions")
	}
	return agentMemoryProfileResponse{
		AgentUID: record.AgentUID, RobotID: record.RobotID, DisplayName: record.DisplayName,
		OwnerUID: record.OwnerUID, ScopeType: record.ScopeType, SpaceID: cloneStringPointer(record.SpaceID),
		Enabled: record.Enabled, Status: record.Status, TrustTier: record.TrustTier,
		CanManage: canManage, Version: record.Version, IncludeOwnTentative: record.IncludeOwnTentative,
		Eligibility: eligibility, Readiness: readiness, BlockerReasonCodes: blockers, AllowedActions: actions, PendingOfferCount: 0,
	}
}

func managementCaller(c *wkhttp.Context) ManagementCaller {
	return ManagementCaller{UID: c.GetLoginUID(), PlatformRole: c.GetLoginRole()}
}

func isPlatformAdmin(caller ManagementCaller) bool {
	return caller.PlatformRole == "admin" || caller.PlatformRole == "superAdmin"
}

func respondInvalid(c *wkhttp.Context) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrSharedParamInvalid, nil, nil)
}

func respondInvalidField(c *wkhttp.Context, field string) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrSharedParamInvalid, nil, i18n.Details{"field": field})
}

func respondForbidden(c *wkhttp.Context) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrSharedForbidden, nil, nil)
}

func respondNotFound(c *wkhttp.Context) {
	httperr.ResponseErrorLWithStatus(c, errcode.ErrSharedNotFound, nil, nil)
}

func (runtime *Runtime) respondInternal(c *wkhttp.Context, operation string, err error) {
	zap.L().Error("agent memory profile API failed", zap.String("operation", operation), zap.Error(err))
	httperr.ResponseErrorLWithStatus(c, errcode.ErrSharedInternal, nil, nil)
}
