package agent_memory

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/google/uuid"
)

type typedBankCreateRequest struct {
	DisplayName    string `json:"display_name"`
	BankType       string `json:"bank_type"`
	Sensitivity    string `json:"sensitivity"`
	RetentionClass string `json:"retention_class"`
}

type capabilityOfferCreateRequest struct {
	TargetRobotID       string       `json:"target_robot_id"`
	OfferedCapabilities []Capability `json:"offered_capabilities"`
	ValidUntil          *time.Time   `json:"valid_until"`
	Reason              string       `json:"reason"`
}

type capabilityOfferUpdateRequest struct {
	OfferedCapabilities []Capability `json:"offered_capabilities"`
	ValidUntil          *time.Time   `json:"valid_until"`
	Status              string       `json:"status"`
	ExpectedVersion     *int64       `json:"expected_version"`
	Reason              string       `json:"reason"`
}

type offerDecisionRequest struct {
	OfferRevision        int64        `json:"offer_revision"`
	Decision             string       `json:"decision"`
	AcceptedCapabilities []Capability `json:"accepted_capabilities"`
	ExpectedVersion      *int64       `json:"expected_version"`
	Reason               string       `json:"reason"`
}

type acceptanceWithdrawRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Reason          string `json:"reason"`
}

func (runtime *Runtime) createAgentBank(c *wkhttp.Context) {
	record, _, ok := runtime.authorizedProfile(c)
	if !ok {
		return
	}
	caller := managementCaller(c)
	if caller.UID != record.OwnerUID && !isPlatformAdmin(caller) {
		respondForbidden(c)
		return
	}
	runtime.createTypedBank(c, BankScopeAgent, record.AgentUID, record.OwnerUID, []string{"private", "tentative"})
}

func (runtime *Runtime) createSpaceBank(c *wkhttp.Context) {
	spaceID := strings.TrimSpace(c.Param("space_id"))
	decision, err := runtime.Access.AuthorizeSpace(c.Request.Context(), managementCaller(c), spaceID)
	if err != nil {
		runtime.respondInternal(c, "authorize Space Bank create", err)
		return
	}
	if !decision.CanManage && !isPlatformAdmin(managementCaller(c)) {
		respondForbidden(c)
		return
	}
	ownerUID, err := runtime.SpaceFacts.SpaceOwnerUID(c.Request.Context(), spaceID)
	if err != nil {
		runtime.respondInternal(c, "load Space owner", err)
		return
	}
	runtime.createTypedBank(c, BankScopeSpace, spaceID, ownerUID, []string{"shared", "verified"})
}

func (runtime *Runtime) createTypedBank(c *wkhttp.Context, scope BankScopeType, scopeID, ownerUID string, allowed []string) {
	var request typedBankCreateRequest
	if decodeStrictJSON(c, &request) != nil || !containsString(allowed, request.BankType) || strings.TrimSpace(request.DisplayName) == "" || strings.TrimSpace(request.RetentionClass) == "" || !containsString([]string{"internal", "confidential", "restricted"}, request.Sensitivity) {
		respondInvalid(c)
		return
	}
	bank, err := runtime.BankCreator.CreateLogicalBank(c.Request.Context(), LogicalBankCreate{ID: uuid.NewString(), DisplayName: strings.TrimSpace(request.DisplayName), BankType: request.BankType, ScopeType: scope, ScopeID: scopeID, OwnerUID: ownerUID, CreatedByUID: managementCaller(c).UID, Sensitivity: request.Sensitivity, RetentionClass: strings.TrimSpace(request.RetentionClass)})
	if err != nil {
		runtime.respondInternal(c, "create typed Bank", err)
		return
	}
	c.ResponseWithStatus(http.StatusCreated, newBankResponse(*bank))
}

func (runtime *Runtime) listBankOffers(c *wkhttp.Context) {
	bank, decision, ok := runtime.authorizedBank(c)
	if !ok {
		return
	}
	if !decision.CanManage || bank.ScopeType != BankScopeSpace {
		respondForbidden(c)
		return
	}
	offers, err := runtime.Collaboration.store.ListBankOffers(bank.ID)
	if err != nil {
		runtime.respondInternal(c, "list Bank offers", err)
		return
	}
	c.Response(map[string]interface{}{"offers": offers})
}

func (runtime *Runtime) createBankOffer(c *wkhttp.Context) {
	bank, decision, ok := runtime.authorizedBank(c)
	if !ok {
		return
	}
	if !decision.CanManage || bank.ScopeType != BankScopeSpace {
		respondForbidden(c)
		return
	}
	var request capabilityOfferCreateRequest
	if decodeStrictJSON(c, &request) != nil {
		respondInvalid(c)
		return
	}
	offer, err := runtime.Collaboration.CreateOffer(c.Request.Context(), OfferCreate{BankID: bank.ID, TargetRobotID: strings.TrimSpace(request.TargetRobotID), Capabilities: request.OfferedCapabilities, ValidUntil: request.ValidUntil, IssuerUID: managementCaller(c).UID, Reason: request.Reason})
	runtime.finishCollaboration(c, offer, err, http.StatusCreated)
}

func (runtime *Runtime) reviseOffer(c *wkhttp.Context) {
	offer, err := runtime.Collaboration.store.FindCurrentOffer(strings.TrimSpace(c.Param("offer_id")))
	if err != nil {
		runtime.finishCollaboration(c, nil, err, http.StatusOK)
		return
	}
	bank, err := runtime.Banks.FindBankByID(offer.BankID)
	if err != nil {
		runtime.finishCollaboration(c, nil, err, http.StatusOK)
		return
	}
	visible, canManage, err := runtime.authorizeBank(c, managementCaller(c), *bank)
	if err != nil {
		runtime.respondInternal(c, "authorize offer", err)
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
	var request capabilityOfferUpdateRequest
	if decodeStrictJSON(c, &request) != nil || request.ExpectedVersion == nil {
		respondInvalid(c)
		return
	}
	updated, err := runtime.Collaboration.ReviseOffer(c.Request.Context(), offer.ID, OfferUpdate{Capabilities: request.OfferedCapabilities, ValidUntil: request.ValidUntil, Status: request.Status, ExpectedVersion: *request.ExpectedVersion, IssuerUID: managementCaller(c).UID, Reason: request.Reason})
	runtime.finishCollaboration(c, updated, err, http.StatusOK)
}

func (runtime *Runtime) listRobotOffers(c *wkhttp.Context) {
	record, _, ok := runtime.authorizedProfile(c)
	if !ok {
		return
	}
	caller := managementCaller(c)
	if caller.UID != record.OwnerUID && !isPlatformAdmin(caller) {
		respondForbidden(c)
		return
	}
	offers, err := runtime.Collaboration.store.ListRobotOffers(record.RobotID)
	if err != nil {
		runtime.respondInternal(c, "list Robot offers", err)
		return
	}
	c.Response(map[string]interface{}{"offers": offers})
}

func (runtime *Runtime) decideOffer(c *wkhttp.Context) {
	var request offerDecisionRequest
	if decodeStrictJSON(c, &request) != nil || request.ExpectedVersion == nil {
		respondInvalid(c)
		return
	}
	acceptance, err := runtime.Collaboration.DecideOffer(c.Request.Context(), strings.TrimSpace(c.Param("offer_id")), OfferDecision{OfferRevision: request.OfferRevision, Decision: request.Decision, Capabilities: request.AcceptedCapabilities, ExpectedVersion: *request.ExpectedVersion, OwnerUID: managementCaller(c).UID, Reason: request.Reason})
	runtime.finishCollaboration(c, acceptance, err, http.StatusOK)
}

func (runtime *Runtime) withdrawAcceptance(c *wkhttp.Context) {
	var request acceptanceWithdrawRequest
	if decodeStrictJSON(c, &request) != nil || request.ExpectedVersion == nil {
		respondInvalid(c)
		return
	}
	acceptance, err := runtime.Collaboration.WithdrawAcceptance(c.Request.Context(), strings.TrimSpace(c.Param("acceptance_id")), managementCaller(c).UID, *request.ExpectedVersion, request.Reason)
	runtime.finishCollaboration(c, acceptance, err, http.StatusOK)
}

func (runtime *Runtime) effectivePermissions(c *wkhttp.Context) {
	record, _, ok := runtime.authorizedProfile(c)
	if !ok {
		return
	}
	caller := managementCaller(c)
	if caller.UID != record.OwnerUID && !isPlatformAdmin(caller) {
		decision, err := runtime.Access.AuthorizeSpace(c.Request.Context(), caller, valueOrEmpty(record.SpaceID))
		if err != nil {
			runtime.respondInternal(c, "authorize effective permissions", err)
			return
		}
		if !decision.CanManage {
			respondForbidden(c)
			return
		}
	}
	permissions, err := runtime.Collaboration.EffectivePermissions(record.RobotID)
	if err != nil {
		runtime.respondInternal(c, "effective permissions", err)
		return
	}
	c.Response(map[string]interface{}{"permissions": permissions})
}

func (runtime *Runtime) finishCollaboration(c *wkhttp.Context, value interface{}, err error, success int) {
	switch {
	case err == nil:
		c.ResponseWithStatus(success, value)
	case errors.Is(err, ErrOfferNotFound), errors.Is(err, ErrAcceptanceNotFound), errors.Is(err, ErrBankNotFound):
		respondNotFound(c)
	case errors.Is(err, ErrVersionConflict), errors.Is(err, ErrOfferRevision), errors.Is(err, ErrDuplicate):
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
	case errors.Is(err, ErrInvalidGrant):
		respondInvalid(c)
	case errors.Is(err, ErrCollaborationScope), errors.Is(err, ErrGrantScope):
		respondForbidden(c)
	default:
		runtime.respondInternal(c, "collaboration operation", err)
	}
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
