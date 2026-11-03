package agent_memory

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/pkg/errcode"
	"github.com/Mininglamp-OSS/octo-server/pkg/httperr"
	"github.com/gocraft/dbr/v2"
	"go.uber.org/zap"
)

const agentContextTTL = 5 * time.Second

var (
	ErrAgentContextAuthentication = errors.New("agent context authentication failed")
	ErrAgentContextForbidden      = errors.New("agent context forbidden")
)

type agentContextStore interface {
	FindProfileByAgentUID(string) (*AgentMemoryProfile, error)
	FindBankByID(string) (*MemoryBank, error)
	ListBanks() ([]MemoryBank, error)
	ListAgentGrants(string) ([]MemoryGrant, error)
	FindPolicyVersionByID(string) (*MemoryPolicyVersion, error)
}

type AgentContextService struct {
	session *dbr.Session
	store   agentContextStore
	now     func() time.Time
}

type agentContextRequest struct {
	BearerToken     string     `json:"bearer_token"`
	Operation       Capability `json:"operation"`
	RequestedBankID string     `json:"requested_bank_id"`
}

type effectiveAgentBank struct {
	BankID          string `json:"bank_id"`
	HindsightBankID string `json:"hindsight_bank_id"`
	BankType        string `json:"bank_type"`
	Sensitivity     string `json:"sensitivity,omitempty"`
}

type effectiveAgentContext struct {
	AgentUID           string               `json:"agent_uid"`
	RobotID            string               `json:"robot_id"`
	OwnerUID           string               `json:"owner_uid"`
	ScopeType          ProfileScopeType     `json:"scope_type"`
	SpaceID            *string              `json:"space_id"`
	ProfileStatus      string               `json:"profile_status"`
	RobotStatus        string               `json:"robot_status"`
	TrustTier          string               `json:"trust_tier"`
	CredentialEpoch    int64                `json:"credential_epoch"`
	AuthorizationEpoch int64                `json:"authorization_epoch"`
	Operation          Capability           `json:"operation"`
	PolicyVersionID    string               `json:"policy_version_id"`
	AllowedBanks       []effectiveAgentBank `json:"allowed_banks"`
	IssuedAt           time.Time            `json:"issued_at"`
	ExpiresAt          time.Time            `json:"expires_at"`
}

func (context effectiveAgentContext) validAt(now time.Time) bool {
	return !now.Before(context.IssuedAt) && now.Before(context.ExpiresAt)
}

type agentContextRobot struct {
	RobotID         string `db:"robot_id"`
	CreatorUID      string `db:"creator_uid"`
	SpaceID         string `db:"space_id"`
	Status          int    `db:"status"`
	CredentialEpoch int64  `db:"credential_epoch"`
}

func NewAgentContextService(session *dbr.Session, store agentContextStore) *AgentContextService {
	return &AgentContextService{session: session, store: store, now: time.Now}
}

func (service *AgentContextService) Resolve(request agentContextRequest) (*effectiveAgentContext, error) {
	if service == nil || service.session == nil || service.store == nil || strings.TrimSpace(request.BearerToken) == "" {
		return nil, ErrAgentContextAuthentication
	}

	var robot agentContextRobot
	err := service.session.Select("robot_id", "creator_uid", "space_id", "status", "credential_epoch").From("robot").
		Where("bot_token=? AND bot_token<>'' AND status=1 AND space_id IS NOT NULL AND space_id<>''", request.BearerToken).LoadOne(&robot)
	if err != nil {
		return nil, ErrAgentContextAuthentication
	}

	profile, err := service.store.FindProfileByAgentUID(robot.RobotID)
	if err != nil || profile == nil || profile.RobotID != robot.RobotID || profile.AgentUID != robot.RobotID ||
		profile.OwnerUID != robot.CreatorUID || profile.ScopeType != ProfileScopeSpace ||
		profile.CredentialEpoch != robot.CredentialEpoch || !profile.Enabled || profile.Status != "active" || profile.PolicyVersionID == "" {
		return nil, ErrAgentContextAuthentication
	}
	if profile.SpaceID == nil || *profile.SpaceID != robot.SpaceID {
		return nil, ErrAgentContextAuthentication
	}
	policy, err := service.store.FindPolicyVersionByID(profile.PolicyVersionID)
	if err != nil || policy == nil || policy.ID != profile.PolicyVersionID || policy.Status != "active" {
		return nil, ErrAgentContextAuthentication
	}
	if !knownCapability(request.Operation) {
		return nil, ErrAgentContextForbidden
	}

	grants, err := service.store.ListAgentGrants(profile.AgentUID)
	if err != nil {
		return nil, ErrAgentContextAuthentication
	}
	var banks []MemoryBank
	if request.RequestedBankID != "" {
		bank, err := service.store.FindBankByID(request.RequestedBankID)
		if err != nil || bank == nil {
			return nil, ErrAgentContextForbidden
		}
		banks = []MemoryBank{*bank}
	} else {
		banks, err = service.store.ListBanks()
		if err != nil {
			return nil, ErrAgentContextAuthentication
		}
	}

	policyGrants := grantsForPolicy(grants, profile.PolicyVersionID)
	allowed := make([]effectiveAgentBank, 0, len(banks))
	for _, bank := range banks {
		if bank.HindsightBankID == "" || bank.PolicyVersionID != profile.PolicyVersionID {
			continue
		}
		decision := ResolveCapability(CapabilityRequest{
			Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: profile.AgentUID},
			Profile:   *profile, Bank: bank, Capability: request.Operation, Grants: policyGrants, Policy: policy, Now: service.now().UTC(),
		})
		if decision.Allowed {
			allowed = append(allowed, effectiveAgentBank{BankID: bank.ID, HindsightBankID: string(bank.HindsightBankID), BankType: bank.BankType, Sensitivity: bank.Sensitivity})
		}
	}
	if request.RequestedBankID != "" && len(allowed) != 1 {
		return nil, ErrAgentContextForbidden
	}
	if len(allowed) == 0 {
		return nil, ErrAgentContextForbidden
	}
	if err := service.revalidate(request.BearerToken, robot, *profile); err != nil {
		return nil, err
	}
	now := service.now().UTC()
	return &effectiveAgentContext{
		AgentUID: profile.AgentUID, RobotID: profile.RobotID, OwnerUID: profile.OwnerUID,
		ScopeType: profile.ScopeType, SpaceID: profile.SpaceID, ProfileStatus: profile.Status, RobotStatus: "active", TrustTier: profile.TrustTier,
		CredentialEpoch: profile.CredentialEpoch, AuthorizationEpoch: profile.AuthorizationEpoch, Operation: request.Operation, PolicyVersionID: profile.PolicyVersionID,
		AllowedBanks: allowed, IssuedAt: now, ExpiresAt: now.Add(agentContextTTL),
	}, nil
}

func (service *AgentContextService) revalidate(token string, expectedRobot agentContextRobot, expectedProfile AgentMemoryProfile) error {
	var currentRobot agentContextRobot
	err := service.session.Select("robot_id", "creator_uid", "space_id", "status", "credential_epoch").From("robot").
		Where("bot_token=? AND bot_token<>'' AND status=1 AND space_id IS NOT NULL AND space_id<>''", token).LoadOne(&currentRobot)
	if err != nil || currentRobot != expectedRobot {
		return ErrAgentContextAuthentication
	}
	currentProfile, err := service.store.FindProfileByAgentUID(expectedProfile.AgentUID)
	if err != nil || currentProfile == nil || currentProfile.RobotID != expectedProfile.RobotID ||
		currentProfile.AgentUID != expectedProfile.AgentUID || currentProfile.OwnerUID != expectedProfile.OwnerUID ||
		currentProfile.ScopeType != expectedProfile.ScopeType || !sameStringPointer(currentProfile.SpaceID, expectedProfile.SpaceID) ||
		!currentProfile.Enabled || currentProfile.Status != "active" || currentProfile.TrustTier != expectedProfile.TrustTier ||
		currentProfile.CredentialEpoch != expectedProfile.CredentialEpoch ||
		currentProfile.AuthorizationEpoch != expectedProfile.AuthorizationEpoch ||
		currentProfile.PolicyVersionID != expectedProfile.PolicyVersionID || currentProfile.Version != expectedProfile.Version {
		return ErrAgentContextAuthentication
	}
	policy, err := service.store.FindPolicyVersionByID(expectedProfile.PolicyVersionID)
	if err != nil || policy == nil || policy.ID != expectedProfile.PolicyVersionID || policy.Status != "active" {
		return ErrAgentContextAuthentication
	}
	return nil
}

func sameStringPointer(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func grantsForPolicy(grants []MemoryGrant, policyVersionID string) []MemoryGrant {
	current := make([]MemoryGrant, 0, len(grants))
	for _, grant := range grants {
		if grant.PolicyVersionID == policyVersionID {
			current = append(current, grant)
		}
	}
	return current
}

func (runtime *Runtime) agentContext(c *wkhttp.Context) {
	if !validServiceSecret(c.GetHeader("Authorization"), runtime.gatewayServiceSecret) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	var request agentContextRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	context, err := runtime.Contexts.Resolve(request)
	if err != nil {
		if errors.Is(err, ErrAgentContextForbidden) {
			httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextForbidden, nil, nil)
			return
		}
		if runtime.ctx != nil {
			zap.L().Warn("agent memory context denied", zap.Error(err))
		}
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	c.Response(context)
}

func validServiceSecret(authorization, configured string) bool {
	if configured == "" || !strings.HasPrefix(authorization, "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(authorization, "Bearer ")
	wantHash := sha256.Sum256([]byte(configured))
	gotHash := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(wantHash[:], gotHash[:]) == 1
}

type persistencePrepareRequest struct {
	BearerToken       string   `json:"bearer_token"`
	BankID            string   `json:"bank_id"`
	IdempotencyKey    string   `json:"idempotency_key"`
	CanonicalKey      string   `json:"canonical_key"`
	LifecycleState    string   `json:"lifecycle_state"`
	FactType          string   `json:"fact_type"`
	AssertionKind     string   `json:"assertion_kind"`
	Confidence        float64  `json:"confidence"`
	ContentHash       string   `json:"content_hash"`
	OriginFingerprint string   `json:"origin_fingerprint"`
	EvidenceIDs       []string `json:"evidence_ids"`
	SourceInstanceID  string   `json:"source_instance_id"`
	GateRunID         string   `json:"gate_run_id"`
	ExpectedVersion   int64    `json:"expected_version"`
}

type persistenceMutationRequest struct {
	BearerToken       string `json:"bearer_token"`
	ControlID         string `json:"control_id"`
	ExpectedVersion   int64  `json:"expected_version"`
	HindsightMemoryID string `json:"hindsight_memory_id"`
	LifecycleState    string `json:"lifecycle_state"`
	ReasonCode        string `json:"reason_code"`
	Retryable         bool   `json:"retryable"`
	Reconciliation    bool   `json:"reconciliation"`
}

func (runtime *Runtime) preparePersistence(c *wkhttp.Context) {
	var request persistencePrepareRequest
	if !runtime.persistenceRequest(c, &request) {
		return
	}
	result, err := runtime.Persistence.Prepare(PersistencePrepare{
		BearerToken: request.BearerToken, BankID: request.BankID, IdempotencyKey: request.IdempotencyKey,
		CanonicalKey: request.CanonicalKey, LifecycleState: request.LifecycleState, FactType: request.FactType,
		AssertionKind: request.AssertionKind, Confidence: request.Confidence, ContentHash: request.ContentHash,
		OriginFingerprint: request.OriginFingerprint, EvidenceIDs: request.EvidenceIDs, SourceInstanceID: request.SourceInstanceID,
		GateRunID: request.GateRunID, ExpectedVersion: request.ExpectedVersion,
	})
	runtime.persistenceResponse(c, result, err)
}

func (runtime *Runtime) completePersistence(c *wkhttp.Context) {
	var request persistenceMutationRequest
	if !runtime.persistenceRequest(c, &request) {
		return
	}
	result, err := runtime.Persistence.Complete(persistenceMutation(request))
	runtime.persistenceResponse(c, result, err)
}

func (runtime *Runtime) failPersistence(c *wkhttp.Context) {
	var request persistenceMutationRequest
	if !runtime.persistenceRequest(c, &request) {
		return
	}
	result, err := runtime.Persistence.Fail(persistenceMutation(request))
	runtime.persistenceResponse(c, result, err)
}

func (runtime *Runtime) persistenceStatus(c *wkhttp.Context) {
	var request persistenceMutationRequest
	if !runtime.persistenceRequest(c, &request) {
		return
	}
	result, err := runtime.Persistence.Status(persistenceMutation(request))
	runtime.persistenceResponse(c, result, err)
}

func (runtime *Runtime) persistenceRequest(c *wkhttp.Context, request any) bool {
	if runtime == nil || runtime.Persistence == nil || !validServiceSecret(c.GetHeader("Authorization"), runtime.gatewayServiceSecret) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return false
	}
	if err := decodeStrictJSON(c, request); err != nil {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return false
	}
	return true
}

func (runtime *Runtime) persistenceResponse(c *wkhttp.Context, result *PersistenceRecord, err error) {
	if err == nil {
		c.Response(result)
		return
	}
	if errors.Is(err, ErrPersistenceConflict) || errors.Is(err, ErrVersionConflict) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	}
	if runtime.ctx != nil {
		zap.L().Warn("agent memory persistence denied", zap.Error(err))
	}
	httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
}

func persistenceMutation(request persistenceMutationRequest) PersistenceMutation {
	return PersistenceMutation{
		BearerToken: request.BearerToken, ControlID: request.ControlID, ExpectedVersion: request.ExpectedVersion,
		HindsightMemoryID: request.HindsightMemoryID, LifecycleState: request.LifecycleState, ReasonCode: request.ReasonCode,
		Retryable: request.Retryable, Reconciliation: request.Reconciliation,
	}
}

type lifecycleTransitionRequest struct {
	BearerToken     string `json:"bearer_token"`
	KnowledgeItemID string `json:"knowledge_item_id"`
	Action          string `json:"action"`
	Reason          string `json:"reason"`
	ExpectedVersion int64  `json:"expected_version"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type lifecycleRollbackRequest struct {
	BearerToken        string `json:"bearer_token"`
	RunID              string `json:"run_id"`
	ExpectedRunVersion int64  `json:"expected_run_version"`
	IdempotencyKey     string `json:"idempotency_key"`
}

func (runtime *Runtime) applyLifecycleTransition(c *wkhttp.Context) {
	if runtime == nil || runtime.Lifecycle == nil || !validServiceSecret(c.GetHeader("Authorization"), runtime.gatewayServiceSecret) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	var request lifecycleTransitionRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	record, err := runtime.Lifecycle.ApplyTransition(LifecycleTransitionInput{
		BearerToken: request.BearerToken, KnowledgeItemID: request.KnowledgeItemID, Action: request.Action,
		Reason: request.Reason, ExpectedVersion: request.ExpectedVersion, IdempotencyKey: request.IdempotencyKey,
	})
	runtime.lifecycleResponse(c, record, err)
}

func (runtime *Runtime) rollbackLifecycle(c *wkhttp.Context) {
	if runtime == nil || runtime.Lifecycle == nil || !validServiceSecret(c.GetHeader("Authorization"), runtime.gatewayServiceSecret) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	var request lifecycleRollbackRequest
	if err := decodeStrictJSON(c, &request); err != nil {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	record, err := runtime.Lifecycle.Rollback(LifecycleRollbackInput{BearerToken: request.BearerToken, RunID: request.RunID, ExpectedRunVersion: request.ExpectedRunVersion, IdempotencyKey: request.IdempotencyKey})
	runtime.lifecycleResponse(c, record, err)
}

func (runtime *Runtime) lifecycleResponse(c *wkhttp.Context, record *LifecycleRecord, err error) {
	if err == nil {
		c.Response(record)
		return
	}
	if errors.Is(err, ErrVersionConflict) || errors.Is(err, ErrIdempotencyConflict) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryVersionConflict, nil, nil)
		return
	}
	if errors.Is(err, ErrLifecycleUnsupported) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryGovernanceScope, nil, nil)
		return
	}
	if errors.Is(err, ErrLifecycleNotFound) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextForbidden, nil, nil)
		return
	}
	if runtime.ctx != nil {
		zap.L().Warn("agent memory lifecycle denied", zap.Error(err))
	}
	httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextForbidden, nil, nil)
}

type readinessContextRequest struct {
	BearerToken    string     `json:"bearer_token"`
	AttemptID      string     `json:"attempt_id"`
	AttemptVersion int64      `json:"attempt_version"`
	BankID         string     `json:"bank_id"`
	Capability     Capability `json:"capability"`
}

// ResolveReadiness is a separate, narrowly-scoped pre-enable resolver. It never
// participates in normal MCP/runtime resolution and authorizes exactly one Bank.
func (service *AgentContextService) ResolveReadiness(request readinessContextRequest) (*effectiveAgentContext, error) {
	if service == nil || service.session == nil || service.store == nil || request.BearerToken == "" || request.AttemptID == "" || request.BankID == "" || !knownCapability(request.Capability) {
		return nil, ErrAgentContextAuthentication
	}
	var robot agentContextRobot
	if err := service.session.Select("robot_id", "creator_uid", "space_id", "status", "credential_epoch").From("robot").Where("bot_token=? AND bot_token<>'' AND status=1 AND space_id IS NOT NULL AND space_id<>''", request.BearerToken).LoadOne(&robot); err != nil {
		return nil, ErrAgentContextAuthentication
	}
	var attempt EnablementAttempt
	if err := service.session.Select("*").From("memory_enablement_attempt").Where("id=? AND robot_id=? AND version=? AND state='draft'", request.AttemptID, robot.RobotID, request.AttemptVersion).LoadOne(&attempt); err != nil {
		return nil, ErrAgentContextAuthentication
	}
	profile, err := service.store.FindProfileByAgentUID(robot.RobotID)
	if err != nil || profile.Enabled || profile.Status != "disabled" || profile.OwnerUID != robot.CreatorUID || profile.SpaceID == nil || *profile.SpaceID != robot.SpaceID || profile.Version != attempt.ProfileVersion || profile.CredentialEpoch != attempt.CredentialEpoch || profile.AuthorizationEpoch != attempt.AuthorizationEpoch || profile.PolicyVersionID != attempt.PolicyVersionID {
		return nil, ErrAgentContextAuthentication
	}
	dependencyStore, ok := service.store.(interface {
		EnablementDependencyVersions(string) (map[string]interface{}, error)
	})
	if !ok {
		return nil, ErrAgentContextAuthentication
	}
	currentDependencies, dependencyErr := dependencyStore.EnablementDependencyVersions(robot.RobotID)
	var expectedDependencies map[string]interface{}
	if dependencyErr != nil || json.Unmarshal(attempt.DependencyVersionsJSON, &expectedDependencies) != nil || !equalDependencyVersions(expectedDependencies, currentDependencies) {
		return nil, ErrAgentContextAuthentication
	}
	policy, err := service.store.FindPolicyVersionByID(profile.PolicyVersionID)
	bank, bankErr := service.store.FindBankByID(request.BankID)
	grants, grantsErr := service.store.ListAgentGrants(profile.AgentUID)
	if err != nil || bankErr != nil || grantsErr != nil || policy.Status != "active" {
		return nil, ErrAgentContextAuthentication
	}
	preEnable := *profile
	preEnable.Enabled, preEnable.Status = true, "active"
	decision := ResolveCapability(CapabilityRequest{Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: profile.AgentUID}, Profile: preEnable, Bank: *bank, Capability: request.Capability, Grants: grantsForPolicy(grants, profile.PolicyVersionID), Policy: policy, Now: service.now().UTC()})
	if !decision.Allowed {
		return nil, ErrAgentContextForbidden
	}
	// The preliminary decision above is deliberately not sufficient to issue.
	// Re-read and lock the complete authority graph in one transaction so no
	// Bank/Space/grant/policy/profile/credential mutation can race token issue.
	return service.finalizeReadiness(request, attempt.DependencyVersionsJSON)
}

func (service *AgentContextService) finalizeReadiness(request readinessContextRequest, expectedDependenciesJSON []byte) (*effectiveAgentContext, error) {
	tx, err := service.session.Begin()
	if err != nil {
		return nil, ErrAgentContextAuthentication
	}
	defer tx.RollbackUnlessCommitted()

	var robot agentContextRobot
	if err := tx.Select("robot_id", "creator_uid", "space_id", "status", "credential_epoch").From("robot").Where("bot_token=? AND bot_token<>''", request.BearerToken).Suffix("FOR UPDATE").LoadOne(&robot); err != nil || robot.Status != 1 || robot.SpaceID == "" {
		return nil, ErrAgentContextAuthentication
	}
	var attempt EnablementAttempt
	if err := tx.Select("*").From("memory_enablement_attempt").Where("id=? AND robot_id=?", request.AttemptID, robot.RobotID).Suffix("FOR UPDATE").LoadOne(&attempt); err != nil || attempt.Version != request.AttemptVersion || attempt.State != "draft" {
		return nil, ErrAgentContextAuthentication
	}
	var expected, attemptExpected map[string]interface{}
	if json.Unmarshal(expectedDependenciesJSON, &expected) != nil || json.Unmarshal(attempt.DependencyVersionsJSON, &attemptExpected) != nil || !equalDependencyVersions(expected, attemptExpected) {
		return nil, ErrAgentContextAuthentication
	}
	current, err := enablementDependencyVersions(tx, robot.RobotID, true)
	if err != nil || !equalDependencyVersions(expected, current) {
		return nil, ErrAgentContextAuthentication
	}

	var profile AgentMemoryProfile
	if err := tx.Select("p.*", "r.robot_id AS agent_uid", "r.creator_uid AS owner_uid", "'space' AS scope_type", "NULLIF(r.space_id, '') AS space_id", "r.credential_epoch", "CASE WHEN r.status=1 AND r.space_id IS NOT NULL AND r.space_id<>'' AND p.enabled=1 THEN 'active' WHEN r.status=1 THEN 'disabled' ELSE 'revoked' END AS status").From(dbr.I("agent_memory_profile").As("p")).Join(dbr.I("robot").As("r"), "r.robot_id=p.robot_id").Where("p.robot_id=?", robot.RobotID).Suffix("FOR UPDATE").LoadOne(&profile); err != nil || profile.Enabled || profile.Status != "disabled" || profile.OwnerUID != robot.CreatorUID || profile.SpaceID == nil || *profile.SpaceID != robot.SpaceID || profile.Version != attempt.ProfileVersion || profile.CredentialEpoch != robot.CredentialEpoch || profile.CredentialEpoch != attempt.CredentialEpoch || profile.AuthorizationEpoch != attempt.AuthorizationEpoch || profile.PolicyVersionID != attempt.PolicyVersionID {
		return nil, ErrAgentContextAuthentication
	}
	var policy MemoryPolicyVersion
	if err := tx.Select("*").From("memory_policy_version").Where("id=?", profile.PolicyVersionID).Suffix("FOR UPDATE").LoadOne(&policy); err != nil || policy.Status != "active" {
		return nil, ErrAgentContextAuthentication
	}
	var bank MemoryBank
	if err := tx.Select("*").From("memory_bank").Where("id=?", request.BankID).Suffix("FOR UPDATE").LoadOne(&bank); err != nil {
		return nil, ErrAgentContextAuthentication
	}
	var grants []MemoryGrant
	if _, err := tx.Select("*").From("memory_grant").Where("principal_type=? AND principal_id=?", PrincipalTypeAgent, profile.AgentUID).OrderAsc("id").Suffix("FOR UPDATE").Load(&grants); err != nil {
		return nil, ErrAgentContextAuthentication
	}
	preEnable := profile
	preEnable.Enabled, preEnable.Status = true, "active"
	now := service.now().UTC()
	decision := ResolveCapability(CapabilityRequest{Principal: PolicyPrincipal{Type: PrincipalTypeAgent, ID: profile.AgentUID}, Profile: preEnable, Bank: bank, Capability: request.Capability, Grants: grantsForPolicy(grants, profile.PolicyVersionID), Policy: &policy, Now: now})
	if !decision.Allowed {
		return nil, ErrAgentContextForbidden
	}
	result := &effectiveAgentContext{AgentUID: profile.AgentUID, RobotID: profile.RobotID, OwnerUID: profile.OwnerUID, ScopeType: profile.ScopeType, SpaceID: profile.SpaceID, ProfileStatus: "active", RobotStatus: "active", TrustTier: profile.TrustTier, CredentialEpoch: profile.CredentialEpoch, AuthorizationEpoch: profile.AuthorizationEpoch, Operation: request.Capability, PolicyVersionID: profile.PolicyVersionID, AllowedBanks: []effectiveAgentBank{{BankID: bank.ID, HindsightBankID: string(bank.HindsightBankID), BankType: bank.BankType, Sensitivity: bank.Sensitivity}}, IssuedAt: now, ExpiresAt: now.Add(agentContextTTL)}
	if err := tx.Commit(); err != nil {
		return nil, ErrAgentContextAuthentication
	}
	return result, nil
}

func (runtime *Runtime) readinessContext(c *wkhttp.Context) {
	if !validServiceSecret(c.GetHeader("Authorization"), runtime.gatewayServiceSecret) {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	var request readinessContextRequest
	if decodeStrictJSON(c, &request) != nil {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	context, err := runtime.Contexts.ResolveReadiness(request)
	if err != nil {
		httperr.ResponseErrorLWithStatus(c, errcode.ErrAgentMemoryContextAuthFailed, nil, nil)
		return
	}
	c.Response(context)
}
