package agent_memory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Governance scope and mode enums mirror the OpenAPI contract.
type GovernanceScopeType string

const (
	GovernanceScopeAgent        GovernanceScopeType = "agent"
	GovernanceScopeSpace        GovernanceScopeType = "space"
	GovernanceScopeOrganization GovernanceScopeType = "organization"
)

const (
	GovernanceModeDryRun = "dry_run"
	GovernanceModeApply  = "apply"

	// governanceProposalLimit bounds how many items a single run evaluates.
	governanceProposalLimit = 500
)

var (
	ErrGovernanceForbidden = errors.New("agent memory governance forbidden")
	ErrGovernanceScope     = errors.New("agent memory governance scope unavailable")
	ErrGovernanceConflict  = errors.New("agent memory governance conflict")
)

// GovernanceProposal pairs a knowledge item with the soft transition governance
// intends to record. In dry-run mode the transition is persisted as a proposal only.
type GovernanceProposal struct {
	BankID          string
	ExpectedVersion int64
	Transition      KnowledgeTransition
}

// GovernanceStore is the persistence surface the governance service depends on.
type GovernanceStore interface {
	FindGovernanceRun(string) (*GovernanceRun, error)
	FindGovernanceRunByIdempotencyKey(string) (*GovernanceRun, error)
	ListGovernanceRuns(scopeType, scopeID string, limit int) ([]GovernanceRun, error)
	ListGovernanceRunTransitions(string) ([]KnowledgeTransition, error)
	ListBanks() ([]MemoryBank, error)
	ListGovernableKnowledgeItems(bankIDs []string, limit int) ([]KnowledgeItem, error)
	PersistGovernanceRun(run *GovernanceRun, proposals []GovernanceProposal, apply bool) error
}

// GovernancePolicyResolver returns the active policy version used to stamp runs and transitions.
type GovernancePolicyResolver interface {
	ActivePolicyVersionID(context.Context) (string, error)
}

type GovernanceService struct {
	store  GovernanceStore
	policy GovernancePolicyResolver
	now    func() time.Time
	newID  func() string
}

func NewGovernanceService(store GovernanceStore, policy GovernancePolicyResolver) *GovernanceService {
	if store == nil || policy == nil {
		return nil
	}
	return &GovernanceService{store: store, policy: policy, now: time.Now, newID: uuid.NewString}
}

// activePolicyResolver reads the active policy version from the store session.
type activePolicyResolver struct {
	store *Store
}

func NewActivePolicyResolver(store *Store) GovernancePolicyResolver {
	if store == nil || store.session == nil {
		return nil
	}
	return &activePolicyResolver{store: store}
}

func (resolver *activePolicyResolver) ActivePolicyVersionID(ctx context.Context) (string, error) {
	var policyVersionID string
	err := resolver.store.session.Select("id").From("memory_policy_version").
		Where("status=?", "active").OrderDesc("activated_at").OrderDesc("created_at").OrderDesc("id").
		Limit(1).LoadOneContext(ctx, &policyVersionID)
	if errors.Is(err, dbr.ErrNotFound) {
		return "", ErrPolicyNotFound
	}
	return policyVersionID, err
}

// GovernanceRunRequest is the validated create input.
type GovernanceRunRequest struct {
	Mode           string
	ScopeType      GovernanceScopeType
	ScopeID        string
	IdempotencyKey string
	ActorUID       string
}

// GovernanceRunView is the read model returned to authorized administrators.
type GovernanceRunView struct {
	Run         GovernanceRun
	Transitions []KnowledgeTransition
}

// Create persists a dry-run or apply governance run. Dry-run records proposal
// transitions without mutating any knowledge item; apply advances items under
// their optimistic version. Idempotent on idempotency_key.
func (service *GovernanceService) Create(ctx context.Context, request GovernanceRunRequest) (*GovernanceRun, error) {
	if service == nil {
		return nil, ErrGovernanceForbidden
	}
	if existing, err := service.store.FindGovernanceRunByIdempotencyKey(request.IdempotencyKey); err == nil {
		if existing.Mode != request.Mode || existing.ScopeType != string(request.ScopeType) || existing.ScopeID != request.ScopeID {
			return nil, ErrGovernanceConflict
		}
		return existing, nil
	} else if !errors.Is(err, ErrGovernanceRunNotFound) {
		return nil, err
	}

	policyVersionID, err := service.policy.ActivePolicyVersionID(ctx)
	if err != nil {
		return nil, ErrGovernanceScope
	}

	bankIDs, err := service.scopeBankIDs(request.ScopeType, request.ScopeID, policyVersionID)
	if err != nil {
		return nil, err
	}

	items, err := service.store.ListGovernableKnowledgeItems(bankIDs, governanceProposalLimit)
	if err != nil {
		return nil, err
	}

	apply := request.Mode == GovernanceModeApply
	now := service.now().UTC()
	proposals := service.evaluate(items, request, policyVersionID, apply, now)

	run := &GovernanceRun{
		ID: service.newID(), Mode: request.Mode, ScopeType: string(request.ScopeType), ScopeID: request.ScopeID,
		PolicyVersionID: policyVersionID, Status: "completed", IdempotencyKey: request.IdempotencyKey,
		ProposedCount: int64(len(proposals)), CreatedByUID: request.ActorUID,
		StartedAt: &now, FinishedAt: &now,
	}
	if apply {
		run.AppliedCount = int64(len(proposals))
	}

	if err := service.store.PersistGovernanceRun(run, proposals, apply); err != nil {
		if errors.Is(err, ErrIdempotencyConflict) {
			if existing, findErr := service.store.FindGovernanceRunByIdempotencyKey(request.IdempotencyKey); findErr == nil {
				return existing, nil
			}
		}
		if errors.Is(err, ErrVersionConflict) {
			return nil, ErrGovernanceConflict
		}
		return nil, err
	}
	return run, nil
}

// Get returns a governance run and the transitions it produced.
func (service *GovernanceService) Get(_ context.Context, runID string) (*GovernanceRunView, error) {
	if service == nil {
		return nil, ErrGovernanceRunNotFound
	}
	run, err := service.store.FindGovernanceRun(runID)
	if err != nil {
		return nil, err
	}
	transitions, err := service.store.ListGovernanceRunTransitions(runID)
	if err != nil {
		return nil, err
	}
	return &GovernanceRunView{Run: *run, Transitions: transitions}, nil
}

// List returns recent runs for a scope.
func (service *GovernanceService) List(_ context.Context, scopeType GovernanceScopeType, scopeID string, limit int) ([]GovernanceRun, error) {
	if service == nil {
		return nil, ErrGovernanceForbidden
	}
	return service.store.ListGovernanceRuns(string(scopeType), scopeID, limit)
}

// scopeBankIDs resolves the active governed banks that belong to the run scope.
func (service *GovernanceService) scopeBankIDs(scopeType GovernanceScopeType, scopeID, policyVersionID string) ([]string, error) {
	banks, err := service.store.ListBanks()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(banks))
	for _, bank := range banks {
		if bank.Status != BankStatusActive || bank.PolicyVersionID != policyVersionID {
			continue
		}
		if string(bank.ScopeType) != string(scopeType) || bank.ScopeID != scopeID {
			continue
		}
		ids = append(ids, bank.ID)
	}
	return ids, nil
}

// evaluate produces deterministic soft-transition proposals. Governance defaults to
// soft transitions and never proposes physical deletion (FR-031). Ordering is stable
// so dry-run and apply produce identical proposal sets.
func (service *GovernanceService) evaluate(items []KnowledgeItem, request GovernanceRunRequest, policyVersionID string, apply bool, now time.Time) []GovernanceProposal {
	proposals := make([]GovernanceProposal, 0, len(items))
	for index := range items {
		item := items[index]
		toState, reason, ok := proposeSoftTransition(item, now)
		if !ok {
			continue
		}
		suffix := "dry_run"
		if apply {
			suffix = "apply"
		}
		proposals = append(proposals, GovernanceProposal{
			BankID:          item.BankID,
			ExpectedVersion: item.Version,
			Transition: KnowledgeTransition{
				ID: service.newID(), KnowledgeItemID: item.ID, FromState: item.State, ToState: toState,
				ActorType: "governance_job", ActorID: governanceActor(request.ActorUID), ReasonCode: reason,
				PolicyVersionID: policyVersionID,
				IdempotencyKey:  fmt.Sprintf("%s:%s:%s:%d", request.IdempotencyKey, suffix, item.ID, item.Version),
			},
		})
	}
	return proposals
}

// proposeSoftTransition encodes the deterministic V1 lifecycle rules. It only proposes
// soft states (stale/corroborated/verified/superseded) and never proposes deleted/revoked.
func proposeSoftTransition(item KnowledgeItem, now time.Time) (string, string, bool) {
	if item.ValidUntil != nil && !now.Before(*item.ValidUntil) {
		switch item.State {
		case "tentative", "corroborated", "verified":
			return "stale", "GOVERNANCE_EXPIRED", true
		}
		return "", "", false
	}
	if item.State == "tentative" && item.ReviewAfter != nil && !now.Before(*item.ReviewAfter) && item.Confidence >= 0.75 {
		return "corroborated", "GOVERNANCE_CORROBORATED", true
	}
	return "", "", false
}

func governanceActor(actorUID string) string {
	if strings.TrimSpace(actorUID) == "" {
		return "governance"
	}
	return actorUID
}

// --- HTTP layer ---

type createGovernanceRunRequest struct {
	Mode           string              `json:"mode"`
	ScopeType      GovernanceScopeType `json:"scope_type"`
	ScopeID        string              `json:"scope_id"`
	IdempotencyKey string              `json:"idempotency_key"`
}

type governanceRunResponse struct {
	ID            string `json:"id"`
	Mode          string `json:"mode"`
	ScopeType     string `json:"scope_type"`
	ScopeID       string `json:"scope_id"`
	Status        string `json:"status"`
	ProposedCount int64  `json:"proposed_count"`
	AppliedCount  int64  `json:"applied_count"`
	FailedCount   int64  `json:"failed_count"`
	Version       int64  `json:"version"`
}

type governanceTransitionResponse struct {
	KnowledgeItemID string  `json:"knowledge_item_id"`
	FromState       string  `json:"from_state"`
	ToState         string  `json:"to_state"`
	ActorType       string  `json:"actor_type"`
	ReasonCode      string  `json:"reason_code"`
	RollbackOf      *string `json:"rollback_of,omitempty"`
}

type governanceRunDetailResponse struct {
	governanceRunResponse
	Transitions []governanceTransitionResponse `json:"transitions"`
}

func (runtime *Runtime) createGovernanceRun(c *wkhttp.Context) {
	if runtime.Governance == nil {
		runtime.respondInternal(c, "create governance run", errors.New("governance service unavailable"))
		return
	}
	var request createGovernanceRunRequest
	if err := decodeStrictJSON(c, &request); err != nil || !validGovernanceCreate(request) {
		respondInvalid(c)
		return
	}
	caller := managementCaller(c)
	if !runtime.authorizeGovernanceScope(c, caller, request.ScopeType, strings.TrimSpace(request.ScopeID)) {
		return
	}
	run, err := runtime.Governance.Create(c.Request.Context(), GovernanceRunRequest{
		Mode: request.Mode, ScopeType: request.ScopeType, ScopeID: strings.TrimSpace(request.ScopeID),
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), ActorUID: caller.UID,
	})
	if errors.Is(err, ErrGovernanceScope) {
		runtime.recordGovernanceDecision(c, request.ScopeType, request.ScopeID, AuditDecisionError, "governance_failed", request.Mode, nil)
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryGovernanceScope, nil, nil)
		return
	}
	if errors.Is(err, ErrGovernanceConflict) {
		runtime.recordGovernanceDecision(c, request.ScopeType, request.ScopeID, AuditDecisionDeny, "governance_conflict", request.Mode, nil)
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	}
	if err != nil {
		runtime.respondInternal(c, "create governance run", err)
		return
	}
	reason := "governance_dry_run"
	if run.Mode == GovernanceModeApply {
		reason = "governance_applied"
	}
	metadata := map[string]string{
		"operation": "governance_run", "mode": run.Mode,
		"proposed_count": fmt.Sprintf("%d", run.ProposedCount), "applied_count": fmt.Sprintf("%d", run.AppliedCount),
	}
	runtime.recordGovernanceDecision(c, request.ScopeType, request.ScopeID, AuditDecisionSuccess, reason, run.Mode, metadata)
	c.ResponseWithStatus(http.StatusAccepted, newGovernanceRunResponse(*run))
}

type governanceRollbackRequest struct {
	Reason             string `json:"reason"`
	ExpectedRunVersion *int64 `json:"expected_run_version"`
	IdempotencyKey     string `json:"idempotency_key"`
}

func (runtime *Runtime) rollbackGovernanceRun(c *wkhttp.Context) {
	if runtime.Governance == nil || runtime.Lifecycle == nil {
		runtime.respondInternal(c, "rollback governance run", errors.New("governance lifecycle unavailable"))
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	view, err := runtime.Governance.Get(c.Request.Context(), runID)
	if errors.Is(err, ErrGovernanceRunNotFound) {
		respondNotFound(c)
		return
	}
	if err != nil {
		runtime.respondInternal(c, "load governance rollback", err)
		return
	}
	if !runtime.authorizeGovernanceScope(c, managementCaller(c), GovernanceScopeType(view.Run.ScopeType), view.Run.ScopeID) {
		return
	}
	var request governanceRollbackRequest
	if decodeStrictJSON(c, &request) != nil || strings.TrimSpace(request.Reason) == "" || request.ExpectedRunVersion == nil || !persistenceKeyPattern.MatchString(request.IdempotencyKey) {
		respondInvalid(c)
		return
	}
	// Re-authorize every affected Bank and item against current scope before mutation.
	banks, err := runtime.Banks.ListBanks()
	if err != nil {
		runtime.respondInternal(c, "authorize governance rollback banks", err)
		return
	}
	allowed := map[string]bool{}
	for _, bank := range banks {
		if bank.Status == BankStatusActive && string(bank.ScopeType) == view.Run.ScopeType && bank.ScopeID == view.Run.ScopeID {
			allowed[bank.ID] = true
		}
	}
	for _, transition := range view.Transitions {
		item, loadErr := loadPersistence(runtime.Lifecycle.store.session, transition.KnowledgeItemID)
		if loadErr != nil || !allowed[item.BankID] || item.PolicyVersionID != view.Run.PolicyVersionID {
			respondForbidden(c)
			return
		}
	}
	rolledBack, err := runtime.Lifecycle.store.RollbackGovernanceRun(runID, *request.ExpectedRunVersion, request.IdempotencyKey, managementCaller(c).UID, runtime.Lifecycle.newID, runtime.Lifecycle.now().UTC())
	if errors.Is(err, ErrVersionConflict) || errors.Is(err, ErrGovernanceNotReversible) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	}
	if err != nil {
		runtime.respondInternal(c, "rollback governance run", err)
		return
	}
	runtime.recordGovernanceDecision(c, GovernanceScopeType(rolledBack.ScopeType), rolledBack.ScopeID, AuditDecisionSuccess, "governance_rollback", "rollback", map[string]string{"reason": "provided"})
	c.ResponseWithStatus(http.StatusAccepted, newGovernanceRunResponse(*rolledBack))
}

func (runtime *Runtime) getGovernanceRun(c *wkhttp.Context) {
	if runtime.Governance == nil {
		runtime.respondInternal(c, "get governance run", errors.New("governance service unavailable"))
		return
	}
	runID := strings.TrimSpace(c.Param("run_id"))
	if runID == "" {
		respondNotFound(c)
		return
	}
	view, err := runtime.Governance.Get(c.Request.Context(), runID)
	if errors.Is(err, ErrGovernanceRunNotFound) {
		respondNotFound(c)
		return
	}
	if err != nil {
		runtime.respondInternal(c, "get governance run", err)
		return
	}
	if !runtime.authorizeGovernanceScope(c, managementCaller(c), GovernanceScopeType(view.Run.ScopeType), view.Run.ScopeID) {
		return
	}
	c.Response(newGovernanceRunDetailResponse(*view))
}

func (runtime *Runtime) listGovernanceRuns(c *wkhttp.Context) {
	if runtime.Governance == nil {
		runtime.respondInternal(c, "list governance runs", errors.New("governance service unavailable"))
		return
	}
	scopeType := GovernanceScopeType(strings.TrimSpace(c.Query("scope_type")))
	scopeID := strings.TrimSpace(c.Query("scope_id"))
	if !knownGovernanceScope(scopeType) || scopeID == "" {
		respondInvalid(c)
		return
	}
	if !runtime.authorizeGovernanceScope(c, managementCaller(c), scopeType, scopeID) {
		return
	}
	runs, err := runtime.Governance.List(c.Request.Context(), scopeType, scopeID, 50)
	if err != nil {
		runtime.respondInternal(c, "list governance runs", err)
		return
	}
	responses := make([]governanceRunResponse, 0, len(runs))
	for _, run := range runs {
		responses = append(responses, newGovernanceRunResponse(run))
	}
	c.Response(map[string]interface{}{"runs": responses})
}

// authorizeGovernanceScope enforces the same Space/platform boundary as the rest of the
// management surface and hides scope-invisible runs behind NotFound (anti-enumeration).
func (runtime *Runtime) authorizeGovernanceScope(c *wkhttp.Context, caller ManagementCaller, scopeType GovernanceScopeType, scopeID string) bool {
	if isPlatformAdmin(caller) {
		if !knownGovernanceScope(scopeType) || scopeID == "" {
			respondNotFound(c)
			return false
		}
		return true
	}
	switch scopeType {
	case GovernanceScopeAgent:
		record, err := runtime.Catalog.FindProfileRecord(scopeID)
		if errors.Is(err, ErrProfileNotFound) {
			respondNotFound(c)
			return false
		}
		if err != nil {
			runtime.respondInternal(c, "authorize governance scope", err)
			return false
		}
		decision, err := runtime.Access.AuthorizeProfile(c.Request.Context(), caller, record.AgentMemoryProfile)
		if err != nil {
			runtime.respondInternal(c, "authorize governance scope", err)
			return false
		}
		if !decision.Visible {
			respondNotFound(c)
			return false
		}
		if !decision.CanManage {
			respondForbidden(c)
			return false
		}
		return true
	case GovernanceScopeSpace:
		decision, err := runtime.Access.AuthorizeSpace(c.Request.Context(), caller, scopeID)
		if err != nil {
			runtime.respondInternal(c, "authorize governance scope", err)
			return false
		}
		if !decision.Visible {
			respondNotFound(c)
			return false
		}
		if !decision.CanManage {
			respondForbidden(c)
			return false
		}
		return true
	case GovernanceScopeOrganization:
		if !isPlatformAdmin(caller) {
			respondForbidden(c)
			return false
		}
		return true
	default:
		respondNotFound(c)
		return false
	}
}

func (runtime *Runtime) recordGovernanceDecision(c *wkhttp.Context, scopeType GovernanceScopeType, scopeID, decision, reason, mode string, metadata map[string]string) {
	if runtime.Audits == nil {
		return
	}
	spaceID := ""
	if scopeType == GovernanceScopeSpace {
		spaceID = scopeID
	}
	requestID := strings.TrimSpace(c.Request.Header.Get("X-Request-ID"))
	if requestID != "" && !validAuditOpaque(requestID, 80) {
		requestID = ""
	}
	event, err := runtime.Audits.GovernanceDecision(AuditRecord{
		PrincipalUserUID: managementCaller(c).UID, SpaceID: spaceID, ResourceID: scopeID,
		Decision: decision, ReasonCode: reason, RequestID: requestID, Metadata: metadata,
	})
	if err == nil {
		err = runtime.Audits.Record(event)
	}
	if err != nil {
		zap.L().Warn("agent memory governance audit failed", zap.Error(err))
	}
}

func validGovernanceCreate(request createGovernanceRunRequest) bool {
	if request.Mode != GovernanceModeDryRun && request.Mode != GovernanceModeApply {
		return false
	}
	if !knownGovernanceScope(request.ScopeType) {
		return false
	}
	scopeID := strings.TrimSpace(request.ScopeID)
	key := strings.TrimSpace(request.IdempotencyKey)
	if scopeID == "" || len(scopeID) > 40 || key == "" || len(key) > 120 {
		return false
	}
	return persistenceKeyPattern.MatchString(key)
}

func knownGovernanceScope(scopeType GovernanceScopeType) bool {
	switch scopeType {
	case GovernanceScopeAgent, GovernanceScopeSpace, GovernanceScopeOrganization:
		return true
	default:
		return false
	}
}

func newGovernanceRunResponse(run GovernanceRun) governanceRunResponse {
	return governanceRunResponse{
		ID: run.ID, Mode: run.Mode, ScopeType: run.ScopeType, ScopeID: run.ScopeID, Status: run.Status,
		ProposedCount: run.ProposedCount, AppliedCount: run.AppliedCount, FailedCount: run.FailedCount, Version: run.Version,
	}
}

func newGovernanceRunDetailResponse(view GovernanceRunView) governanceRunDetailResponse {
	transitions := make([]governanceTransitionResponse, 0, len(view.Transitions))
	for _, transition := range view.Transitions {
		transitions = append(transitions, governanceTransitionResponse{
			KnowledgeItemID: transition.KnowledgeItemID, FromState: transition.FromState, ToState: transition.ToState,
			ActorType: transition.ActorType, ReasonCode: transition.ReasonCode, RollbackOf: transition.RollbackOf,
		})
	}
	return governanceRunDetailResponse{
		governanceRunResponse: newGovernanceRunResponse(view.Run),
		Transitions:           transitions,
	}
}
