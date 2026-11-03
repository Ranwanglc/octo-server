package agent_memory

import (
	"errors"
	"time"

	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

var (
	ErrLifecycleForbidden   = errors.New("agent memory lifecycle forbidden")
	ErrLifecycleNotFound    = errors.New("agent memory lifecycle item not found")
	ErrLifecycleUnsupported = errors.New("agent memory lifecycle action unsupported")
)

// lifecycleActions maps each curator action to the target state and reason code.
// Actions never physically delete; deleted/revoked source items cannot be re-verified.
var lifecycleActions = map[string]struct {
	toState string
	reason  string
}{
	"verify":     {"verified", "CURATE_VERIFY"},
	"dispute":    {"disputed", "CURATE_DISPUTE"},
	"mark_stale": {"stale", "CURATE_MARK_STALE"},
	"revoke":     {"revoked", "CURATE_REVOKE"},
	"restore":    {"tentative", "CURATE_RESTORE"},
	"supersede":  {"superseded", "CURATE_SUPERSEDE"},
}

// lifecycleAllowedFrom bounds which prior states each action may transition from so a
// replayed or racing apply cannot resurrect a terminal item into an active state.
var lifecycleAllowedFrom = map[string]map[string]struct{}{
	"verify":     {"tentative": {}, "corroborated": {}, "disputed": {}},
	"dispute":    {"tentative": {}, "corroborated": {}, "verified": {}},
	"mark_stale": {"tentative": {}, "corroborated": {}, "verified": {}, "disputed": {}},
	"revoke":     {"tentative": {}, "corroborated": {}, "verified": {}, "disputed": {}, "stale": {}},
	"restore":    {"stale": {}, "disputed": {}},
	"supersede":  {"tentative": {}, "corroborated": {}, "verified": {}, "disputed": {}},
}

type LifecycleService struct {
	store    *Store
	contexts persistenceContextResolver
	now      func() time.Time
	newID    func() string
}

func NewLifecycleService(store *Store, contexts persistenceContextResolver) *LifecycleService {
	if store == nil || contexts == nil {
		return nil
	}
	return &LifecycleService{store: store, contexts: contexts, now: time.Now, newID: uuid.NewString}
}

type LifecycleTransitionInput struct {
	BearerToken     string
	KnowledgeItemID string
	Action          string
	Reason          string
	ExpectedVersion int64
	IdempotencyKey  string
}

type LifecycleRollbackInput struct {
	BearerToken        string
	RunID              string
	ExpectedRunVersion int64
	IdempotencyKey     string
}

// LifecycleRecord is the redacted view returned to the gateway.
type LifecycleRecord struct {
	KnowledgeItemID string `json:"knowledge_item_id,omitempty"`
	State           string `json:"state,omitempty"`
	Version         int64  `json:"version,omitempty"`
	ReasonCode      string `json:"reason_code,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	RunStatus       string `json:"run_status,omitempty"`
}

// ApplyTransition performs one curator lifecycle transition under optimistic version and
// idempotency. Only items within the curator's authorized banks may be transitioned.
func (service *LifecycleService) ApplyTransition(input LifecycleTransitionInput) (*LifecycleRecord, error) {
	if service == nil || service.store == nil || service.contexts == nil {
		return nil, ErrLifecycleForbidden
	}
	action, known := lifecycleActions[input.Action]
	if !known {
		return nil, ErrLifecycleUnsupported
	}
	if !persistenceKeyPattern.MatchString(input.KnowledgeItemID) || !persistenceKeyPattern.MatchString(input.IdempotencyKey) || input.ExpectedVersion < 0 {
		return nil, ErrLifecycleForbidden
	}
	context, err := service.contexts.Resolve(agentContextRequest{BearerToken: input.BearerToken, Operation: CapabilityCurate})
	if err != nil {
		return nil, ErrLifecycleForbidden
	}
	item, err := loadPersistence(service.store.session, input.KnowledgeItemID)
	if errors.Is(err, ErrPersistenceNotFound) {
		return nil, ErrLifecycleNotFound
	}
	if err != nil {
		return nil, err
	}
	if !contextHasBank(context, item.BankID) || item.PolicyVersionID != context.PolicyVersionID {
		return nil, ErrLifecycleForbidden
	}
	if _, ok := lifecycleAllowedFrom[input.Action][item.State]; !ok {
		return nil, ErrVersionConflict
	}
	transition := &KnowledgeTransition{
		ID: service.newID(), KnowledgeItemID: item.ID, FromState: item.State, ToState: action.toState,
		ActorType: "user", ActorID: context.OwnerUID, ReasonCode: action.reason,
		PolicyVersionID: context.PolicyVersionID, IdempotencyKey: input.IdempotencyKey,
	}
	applied, err := service.store.ApplyLifecycleTransition(transition, input.ExpectedVersion)
	if err != nil {
		return nil, err
	}
	service.audit(context, "lifecycle.transition", item.ID, action.reason)
	return &LifecycleRecord{KnowledgeItemID: applied.ID, State: applied.State, Version: applied.Version, ReasonCode: action.reason}, nil
}

// Rollback reverses an apply governance run within the curator's scope, restoring the
// prior state of each applied transition and appending rollback transitions.
func (service *LifecycleService) Rollback(input LifecycleRollbackInput) (*LifecycleRecord, error) {
	if service == nil || service.store == nil || service.contexts == nil {
		return nil, ErrLifecycleForbidden
	}
	if !persistenceKeyPattern.MatchString(input.RunID) || input.ExpectedRunVersion < 0 {
		return nil, ErrLifecycleForbidden
	}
	context, err := service.contexts.Resolve(agentContextRequest{BearerToken: input.BearerToken, Operation: CapabilityCurate})
	if err != nil {
		return nil, ErrLifecycleForbidden
	}
	run, err := service.store.FindGovernanceRun(input.RunID)
	if errors.Is(err, ErrGovernanceRunNotFound) {
		return nil, ErrLifecycleNotFound
	}
	if err != nil {
		return nil, err
	}
	if !service.runInScope(context, *run) {
		return nil, ErrLifecycleNotFound
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = "legacy:" + input.RunID
		input.ExpectedRunVersion = run.Version
	}
	rolledBack, err := service.store.RollbackGovernanceRun(input.RunID, input.ExpectedRunVersion, input.IdempotencyKey, context.OwnerUID, service.newID, service.now().UTC())
	if errors.Is(err, ErrGovernanceRunNotFound) {
		return nil, ErrLifecycleNotFound
	}
	if errors.Is(err, ErrGovernanceNotReversible) {
		return nil, ErrLifecycleUnsupported
	}
	if err != nil {
		return nil, err
	}
	service.audit(context, "lifecycle.rollback", run.ID, "GOVERNANCE_ROLLBACK")
	return &LifecycleRecord{RunID: rolledBack.ID, RunStatus: rolledBack.Status, ReasonCode: "GOVERNANCE_ROLLBACK", Version: rolledBack.Version}, nil
}

// runInScope confirms the curator's authorized banks intersect the run scope so a
// curator cannot roll back runs outside its scope. Agent-scope runs match by owner.
func (service *LifecycleService) runInScope(context *effectiveAgentContext, run GovernanceRun) bool {
	switch run.ScopeType {
	case string(GovernanceScopeAgent):
		return run.ScopeID == context.AgentUID
	case string(GovernanceScopeSpace):
		return context.ScopeType == ProfileScopeSpace && context.SpaceID != nil && *context.SpaceID == run.ScopeID
	default:
		return false
	}
}

func (service *LifecycleService) audit(context *effectiveAgentContext, eventType, resourceID, reason string) {
	_ = service.store.WithTx(func(tx *dbr.Tx) error {
		return appendPersistenceAudit(tx, service.newID(), eventType, context, resourceID, "success", reason, "", "")
	})
}
