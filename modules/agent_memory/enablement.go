package agent_memory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

var (
	ErrEnablementNotFound = errors.New("agent memory enablement attempt not found")
	ErrEnablementBlocked  = errors.New("agent memory enablement blocked")
)

type EnablementAttempt struct {
	ID                     string     `db:"id" json:"id"`
	RobotID                string     `db:"robot_id" json:"robot_id"`
	InitiatedByUID         string     `db:"initiated_by_uid" json:"-"`
	State                  string     `db:"state" json:"state"`
	ProfileVersion         int64      `db:"profile_version" json:"-"`
	CredentialEpoch        int64      `db:"credential_epoch" json:"-"`
	AuthorizationEpoch     int64      `db:"authorization_epoch" json:"-"`
	PolicyVersionID        string     `db:"policy_version_id" json:"-"`
	DependencyVersionsJSON []byte     `db:"dependency_versions_json" json:"-"`
	ResultReasonCode       string     `db:"result_reason_code" json:"-"`
	ProbeStepsJSON         []byte     `db:"probe_steps_json" json:"-"`
	ProbeExpiresAt         *time.Time `db:"probe_expires_at" json:"probe_expires_at"`
	IdempotencyKey         string     `db:"idempotency_key" json:"-"`
	Version                int64      `db:"version" json:"version"`
	CreatedAt              time.Time  `db:"created_at" json:"-"`
	UpdatedAt              time.Time  `db:"updated_at" json:"-"`
}

type ProbeStep struct {
	Code       string `json:"code"`
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type EnablementView struct {
	ID             string      `json:"id"`
	RobotID        string      `json:"robot_id"`
	State          string      `json:"state"`
	Readiness      string      `json:"readiness"`
	Blockers       []string    `json:"blockers"`
	ProbeSteps     []ProbeStep `json:"probe_steps"`
	ProbeExpiresAt *time.Time  `json:"probe_expires_at"`
	Version        int64       `json:"version"`
}

type EnablementService struct {
	store *Store
	now   func() time.Time
	newID func() string
	probe EnablementReadinessProbe
}

type EnablementReadinessProbe interface {
	Probe(ReadinessProbeRequest) error
}

type ReadinessProbeRequest struct {
	RobotToken, AttemptID, BankID string
	AttemptVersion                int64
	Capability                    Capability
}

func NewEnablementService(store *Store) *EnablementService {
	return &EnablementService{store: store, now: time.Now, newID: uuid.NewString}
}

func (service *EnablementService) Start(robotID, actorUID, key string) (*EnablementAttempt, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 120 || !persistenceKeyPattern.MatchString(key) {
		return nil, ErrEnablementBlocked
	}
	if existing, err := service.store.FindEnablementAttemptByKey(robotID, key); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrEnablementNotFound) {
		return nil, err
	}
	profile, err := service.store.FindProfileByRobotID(robotID)
	if err != nil {
		return nil, err
	}
	facts, err := service.store.FindRobotCollaborationFacts(robotID)
	if err != nil {
		return nil, err
	}
	if facts.Status != 1 || facts.SpaceID == "" || profile.Status != "active" {
		return nil, ErrEnablementBlocked
	}
	dependencies, err := service.store.EnablementDependencyVersions(robotID)
	if err != nil {
		return nil, err
	}
	encodedDependencies, err := json.Marshal(dependencies)
	if err != nil {
		return nil, err
	}
	attempt := &EnablementAttempt{ID: service.newID(), RobotID: robotID, InitiatedByUID: actorUID, State: "draft", ProfileVersion: profile.Version, CredentialEpoch: profile.CredentialEpoch, AuthorizationEpoch: profile.AuthorizationEpoch, PolicyVersionID: profile.PolicyVersionID, DependencyVersionsJSON: encodedDependencies, ProbeStepsJSON: []byte("[]"), IdempotencyKey: key}
	if err := service.store.CreateEnablementAttempt(attempt); err != nil {
		return nil, err
	}
	return attempt, nil
}

// Probe is deliberately read-only: it validates current authority and effective
// capabilities without retaining, recalling, or otherwise mutating Hindsight.
func (service *EnablementService) Probe(id, actorUID string, expectedVersion int64) (*EnablementAttempt, error) {
	attempt, err := service.store.FindEnablementAttempt(id)
	if err != nil {
		return nil, err
	}
	if attempt.InitiatedByUID != actorUID || attempt.State != "draft" || attempt.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	if err := service.snapshotsCurrent(attempt); err != nil {
		return nil, err
	}
	permissions, err := NewCollaborationService(service.store).EffectivePermissions(attempt.RobotID)
	if err != nil {
		return nil, err
	}
	if service.probe == nil {
		return nil, ErrEnablementBlocked
	}
	robotToken, err := service.store.FindRobotToken(attempt.RobotID)
	effective := false
	var probeBank string
	var probeCapability Capability
	for _, permission := range permissions {
		if permission.State == "effective" || (permission.State == "unavailable" && permission.ReasonCode == "PROFILE_DISABLED") {
			effective, probeBank, probeCapability = true, permission.BankID, permission.Capability
			break
		}
	}
	if err != nil || !effective || service.probe.Probe(ReadinessProbeRequest{RobotToken: robotToken, AttemptID: attempt.ID, AttemptVersion: attempt.Version, BankID: probeBank, Capability: probeCapability}) != nil {
		return nil, ErrEnablementBlocked
	}
	now := service.now().UTC()
	state, reason, expires := "failed", "NO_EFFECTIVE_CAPABILITY", (*time.Time)(nil)
	steps := []ProbeStep{{Code: "authority", Status: "passed"}, {Code: "write_gate_hindsight", Status: "passed"}, {Code: "effective_capability", Status: "failed", ReasonCode: reason}}
	if effective {
		state, reason = "ready", ""
		until := now.Add(5 * time.Minute)
		expires = &until
		steps[2] = ProbeStep{Code: "effective_capability", Status: "passed"}
	}
	encoded, _ := json.Marshal(steps)
	if err := service.store.UpdateEnablementProbe(attempt.ID, expectedVersion, state, reason, encoded, expires); err != nil {
		return nil, err
	}
	attempt.State, attempt.ResultReasonCode, attempt.ProbeStepsJSON, attempt.ProbeExpiresAt, attempt.Version = state, reason, encoded, expires, attempt.Version+1
	return attempt, nil
}

func (service *EnablementService) Commit(id, actorUID string, expectedVersion int64) (*EnablementAttempt, error) {
	attempt, err := service.store.FindEnablementAttempt(id)
	if err != nil {
		return nil, err
	}
	if attempt.InitiatedByUID != actorUID || attempt.State != "ready" || attempt.Version != expectedVersion || attempt.ProbeExpiresAt == nil || !service.now().UTC().Before(*attempt.ProbeExpiresAt) {
		return nil, ErrVersionConflict
	}
	if err := service.snapshotsCurrent(attempt); err != nil {
		return nil, err
	}
	dependencies, err := advanceCommittedProfileSnapshot(attempt.DependencyVersionsJSON, attempt.ProfileVersion+1)
	if err != nil {
		return nil, ErrVersionConflict
	}
	if err := service.store.CommitEnablement(attempt, dependencies); err != nil {
		return nil, err
	}
	attempt.State, attempt.Version, attempt.ProfileVersion, attempt.DependencyVersionsJSON = "committed", attempt.Version+1, attempt.ProfileVersion+1, dependencies
	return attempt, nil
}

func advanceCommittedProfileSnapshot(raw []byte, version int64) ([]byte, error) {
	var dependencies map[string]interface{}
	if err := json.Unmarshal(raw, &dependencies); err != nil {
		return nil, err
	}
	profile, ok := dependencies["profile"].(map[string]interface{})
	if !ok {
		return nil, errors.New("missing profile dependency")
	}
	profile["version"] = float64(version)
	profile["enabled"] = true
	return json.Marshal(dependencies)
}

// ProfileReadiness revalidates the newest committed proof against all current
// dependencies and the same effective-permission resolver used by runtime.
// Errors are deliberately collapsed to stable, non-sensitive blocker codes.
func (service *EnablementService) ProfileReadiness(robotID string) (string, []string) {
	if service == nil || service.store == nil {
		return "blocked", []string{"READINESS_UNAVAILABLE"}
	}
	attempt, err := service.store.FindLatestCommittedEnablement(robotID)
	if err != nil {
		return "blocked", []string{"FRESH_PROBE_REQUIRED"}
	}
	profile, profileErr := service.store.FindProfileByRobotID(robotID)
	dependencies, dependencyErr := service.store.EnablementDependencyVersions(robotID)
	permissions, permissionErr := NewCollaborationService(service.store).EffectivePermissions(robotID)
	return determineProfileReadiness(service.now().UTC(), attempt, profile, dependencies, permissions, errors.Join(profileErr, dependencyErr, permissionErr))
}

func determineProfileReadiness(now time.Time, attempt *EnablementAttempt, profile *AgentMemoryProfile, dependencies map[string]interface{}, permissions []EffectivePermission, resolverErr error) (string, []string) {
	if attempt == nil {
		return "blocked", []string{"FRESH_PROBE_REQUIRED"}
	}
	if attempt.ProbeExpiresAt == nil || !now.Before(*attempt.ProbeExpiresAt) {
		return "blocked", []string{"PROBE_EXPIRED"}
	}
	if profile == nil || !profile.Enabled || profile.Status != "active" {
		return "blocked", []string{"PROFILE_NOT_ENABLED"}
	}
	var expected map[string]interface{}
	if resolverErr != nil || json.Unmarshal(attempt.DependencyVersionsJSON, &expected) != nil || !equalDependencyVersions(expected, dependencies) ||
		profile.Version != attempt.ProfileVersion || profile.CredentialEpoch != attempt.CredentialEpoch || profile.AuthorizationEpoch != attempt.AuthorizationEpoch || profile.PolicyVersionID != attempt.PolicyVersionID {
		return "blocked", []string{"DEPENDENCY_DRIFT"}
	}
	for _, permission := range permissions {
		if permission.State == "effective" {
			return "operational", []string{}
		}
	}
	return "blocked", []string{"RUNTIME_DENIED"}
}

func (service *EnablementService) Cancel(id, actorUID string, expectedVersion int64) (*EnablementAttempt, error) {
	attempt, err := service.store.FindEnablementAttempt(id)
	if err != nil {
		return nil, err
	}
	if attempt.InitiatedByUID != actorUID || attempt.Version != expectedVersion || attempt.State == "committed" || attempt.State == "cancelled" {
		return nil, ErrVersionConflict
	}
	if err := service.store.CancelEnablement(attempt); err != nil {
		return nil, err
	}
	attempt.State, attempt.Version = "cancelled", attempt.Version+1
	return attempt, nil
}

func (service *EnablementService) snapshotsCurrent(attempt *EnablementAttempt) error {
	profile, err := service.store.FindProfileByRobotID(attempt.RobotID)
	if err != nil {
		return err
	}
	dependencies, depErr := service.store.EnablementDependencyVersions(attempt.RobotID)
	var expected map[string]interface{}
	if depErr != nil || json.Unmarshal(attempt.DependencyVersionsJSON, &expected) != nil || !equalDependencyVersions(expected, dependencies) {
		return ErrVersionConflict
	}
	if profile.Version != attempt.ProfileVersion || profile.CredentialEpoch != attempt.CredentialEpoch || profile.AuthorizationEpoch != attempt.AuthorizationEpoch || profile.PolicyVersionID != attempt.PolicyVersionID || profile.Status != "active" {
		return ErrVersionConflict
	}
	return nil
}

func (attempt EnablementAttempt) View(now time.Time) EnablementView {
	steps := []ProbeStep{}
	_ = json.Unmarshal(attempt.ProbeStepsJSON, &steps)
	readiness, blockers := "setup_required", []string{}
	switch attempt.State {
	case "ready":
		if attempt.ProbeExpiresAt != nil && now.Before(*attempt.ProbeExpiresAt) {
			readiness = "ready"
		} else {
			readiness, blockers = "blocked", []string{"PROBE_EXPIRED"}
		}
	case "committed":
		readiness = "operational"
	case "failed":
		readiness, blockers = "blocked", []string{attempt.ResultReasonCode}
	case "expired", "cancelled":
		readiness, blockers = "blocked", []string{strings.ToUpper(attempt.State)}
	}
	return EnablementView{ID: attempt.ID, RobotID: attempt.RobotID, State: attempt.State, Readiness: readiness, Blockers: blockers, ProbeSteps: steps, ProbeExpiresAt: attempt.ProbeExpiresAt, Version: attempt.Version}
}

func (store *Store) CreateEnablementAttempt(attempt *EnablementAttempt) error {
	_, err := store.session.InsertInto("memory_enablement_attempt").Columns("id", "robot_id", "initiated_by_uid", "state", "profile_version", "credential_epoch", "authorization_epoch", "policy_version_id", "dependency_versions_json", "result_reason_code", "probe_steps_json", "probe_expires_at", "idempotency_key", "version").Record(attempt).Exec()
	return classifyInsertError(err, "uk_memory_enablement_idempotency")
}

func (store *Store) FindEnablementAttempt(id string) (*EnablementAttempt, error) {
	var attempt EnablementAttempt
	err := store.session.Select("*").From("memory_enablement_attempt").Where("id=?", id).LoadOne(&attempt)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrEnablementNotFound
	}
	return &attempt, err
}

func (store *Store) FindEnablementAttemptByKey(robotID, key string) (*EnablementAttempt, error) {
	var attempt EnablementAttempt
	err := store.session.Select("*").From("memory_enablement_attempt").Where("robot_id=? AND idempotency_key=?", robotID, key).LoadOne(&attempt)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrEnablementNotFound
	}
	return &attempt, err
}

func (store *Store) UpdateEnablementProbe(id string, version int64, state, reason string, steps []byte, expires *time.Time) error {
	result, err := store.session.Update("memory_enablement_attempt").SetMap(map[string]interface{}{"state": state, "result_reason_code": reason, "probe_steps_json": steps, "probe_expires_at": expires, "version": dbr.Expr("version+1")}).Where("id=? AND version=? AND state='draft'", id, version).Exec()
	if err != nil {
		return err
	}
	return requireUpdated(result)
}

func (store *Store) CommitEnablement(attempt *EnablementAttempt, dependencies []byte) error {
	return store.WithTx(func(tx *dbr.Tx) error {
		result, err := tx.Update("memory_enablement_attempt").Set("state", "committed").Set("profile_version", attempt.ProfileVersion+1).Set("dependency_versions_json", dependencies).Set("version", dbr.Expr("version+1")).Where("id=? AND version=? AND state='ready'", attempt.ID, attempt.Version).Exec()
		if err != nil {
			return err
		}
		if err = requireUpdated(result); err != nil {
			return err
		}
		result, err = tx.Update("agent_memory_profile").Set("enabled", true).Set("version", dbr.Expr("version+1")).Where("robot_id=? AND version=? AND credential_epoch=? AND authorization_epoch=? AND policy_version_id=?", attempt.RobotID, attempt.ProfileVersion, attempt.CredentialEpoch, attempt.AuthorizationEpoch, attempt.PolicyVersionID).Exec()
		if err != nil {
			return err
		}
		return requireUpdated(result)
	})
}

func (store *Store) FindLatestCommittedEnablement(robotID string) (*EnablementAttempt, error) {
	var attempt EnablementAttempt
	err := store.session.Select("*").From("memory_enablement_attempt").Where("robot_id=? AND state='committed'", robotID).OrderDesc("updated_at").OrderDesc("id").Limit(1).LoadOne(&attempt)
	if errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrEnablementNotFound
	}
	return &attempt, err
}

func (store *Store) CancelEnablement(attempt *EnablementAttempt) error {
	result, err := store.session.Update("memory_enablement_attempt").Set("state", "cancelled").Set("version", dbr.Expr("version+1")).Where("id=? AND version=? AND state NOT IN ('committed','cancelled')", attempt.ID, attempt.Version).Exec()
	if err != nil {
		return err
	}
	return requireUpdated(result)
}

func equalDependencyVersions(a, b map[string]interface{}) bool {
	// JSON is the durable representation. Canonical encodings also normalize
	// numbers decoded from JSON against integer values from a fresh DB read.
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func (store *Store) FindRobotToken(robotID string) (string, error) {
	var token string
	err := store.session.Select("bot_token").From("robot").Where("robot_id=? AND status=1", robotID).LoadOne(&token)
	return token, err
}

func (store *Store) EnablementDependencyVersions(robotID string) (map[string]interface{}, error) {
	return enablementDependencyVersions(store.session, robotID, false)
}

// enablementDependencyVersions captures every mutable authority fact used by
// offer/probe/runtime authorization. The Robot's authoritative space_id is read
// first; the Bank set is exactly its Agent Banks plus its current Space Banks.
func enablementDependencyVersions(runner dbr.SessionRunner, robotID string, forUpdate bool) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	type robotRow struct {
		RobotID         string `db:"robot_id"`
		CreatorUID      string `db:"creator_uid"`
		SpaceID         string `db:"space_id"`
		Status          int    `db:"status"`
		CredentialEpoch int64  `db:"credential_epoch"`
	}
	robotQuery := runner.Select("robot_id", "creator_uid", "COALESCE(space_id, '') AS space_id", "status", "credential_epoch").From("robot").Where("robot_id=?", robotID)
	if forUpdate {
		robotQuery = robotQuery.Suffix("FOR UPDATE")
	}
	var robot robotRow
	if err := robotQuery.LoadOne(&robot); err != nil {
		return nil, err
	}
	out["robot"] = map[string]interface{}{"robot_id": robot.RobotID, "creator_uid": robot.CreatorUID, "space_id": robot.SpaceID, "status": robot.Status, "credential_epoch": robot.CredentialEpoch}

	type profileRow struct {
		Version            int64  `db:"version"`
		PolicyVersionID    string `db:"policy_version_id"`
		CredentialEpoch    int64  `db:"credential_epoch"`
		AuthorizationEpoch int64  `db:"authorization_epoch"`
		Enabled            bool   `db:"enabled"`
	}
	profileQuery := runner.Select("p.version", "p.policy_version_id", "r.credential_epoch", "p.authorization_epoch", "p.enabled").From(dbr.I("agent_memory_profile").As("p")).Join(dbr.I("robot").As("r"), "r.robot_id=p.robot_id").Where("p.robot_id=?", robotID)
	if forUpdate {
		profileQuery = profileQuery.Suffix("FOR UPDATE")
	}
	var profile profileRow
	if err := profileQuery.LoadOne(&profile); err != nil {
		return nil, err
	}
	out["profile"] = map[string]interface{}{"version": profile.Version, "policy_version_id": profile.PolicyVersionID, "credential_epoch": profile.CredentialEpoch, "authorization_epoch": profile.AuthorizationEpoch, "enabled": profile.Enabled}

	type bankRow struct {
		ID              string `db:"id"`
		ScopeType       string `db:"scope_type"`
		ScopeID         string `db:"scope_id"`
		Status          string `db:"status"`
		PolicyVersionID string `db:"policy_version_id"`
		Version         int64  `db:"version"`
	}
	bankQuery := runner.Select("id", "scope_type", "scope_id", "status", "policy_version_id", "version").From("memory_bank").Where("(scope_type='agent' AND scope_id=?) OR (scope_type='space' AND scope_id=?)", robotID, robot.SpaceID).OrderAsc("id")
	if forUpdate {
		bankQuery = bankQuery.Suffix("FOR UPDATE")
	}
	var banks []bankRow
	if _, err := bankQuery.Load(&banks); err != nil {
		return nil, err
	}
	bankFacts := map[string]interface{}{}
	for _, row := range banks {
		bankFacts[row.ID] = map[string]interface{}{"scope_type": row.ScopeType, "scope_id": row.ScopeID, "status": row.Status, "policy_version_id": row.PolicyVersionID, "version": row.Version}
	}
	out["banks"] = bankFacts

	type offerRow struct {
		ID              string     `db:"id"`
		BankID          string     `db:"bank_id"`
		TargetRobotID   string     `db:"target_robot_id"`
		AudienceSpaceID string     `db:"audience_space_id"`
		Status          string     `db:"status"`
		PolicyVersionID string     `db:"policy_version_id"`
		OfferedJSON     []byte     `db:"offered_capabilities_json"`
		ValidFrom       time.Time  `db:"valid_from"`
		ValidUntil      *time.Time `db:"valid_until"`
		Revision        int64      `db:"revision"`
		Version         int64      `db:"version"`
	}
	offerQuery := runner.Select("id", "revision", "bank_id", "target_robot_id", "audience_space_id", "status", "policy_version_id", "offered_capabilities_json", "valid_from", "valid_until", "version").From("memory_capability_offer").Where("target_robot_id=?", robotID).OrderAsc("id").OrderAsc("revision")
	if forUpdate {
		offerQuery = offerQuery.Suffix("FOR UPDATE")
	}
	var offers []offerRow
	if _, err := offerQuery.Load(&offers); err != nil {
		return nil, err
	}
	offerFacts := map[string]interface{}{}
	for _, row := range offers {
		offerFacts[fmt.Sprintf("%s:%d", row.ID, row.Revision)] = map[string]interface{}{"bank_id": row.BankID, "target_robot_id": row.TargetRobotID, "audience_space_id": row.AudienceSpaceID, "status": row.Status, "policy_version_id": row.PolicyVersionID, "offered_capabilities": row.OfferedJSON, "valid_from": row.ValidFrom, "valid_until": row.ValidUntil, "version": row.Version}
	}
	out["offers"] = offerFacts

	type acceptanceRow struct {
		ID                 string `db:"id"`
		OfferID            string `db:"offer_id"`
		RobotID            string `db:"robot_id"`
		Status             string `db:"status"`
		AcceptedByOwnerUID string `db:"accepted_by_owner_uid"`
		AcceptedJSON       []byte `db:"accepted_capabilities_json"`
		OfferRevision      int64  `db:"offer_revision"`
		Version            int64  `db:"version"`
	}
	acceptanceQuery := runner.Select("id", "offer_id", "offer_revision", "robot_id", "status", "accepted_by_owner_uid", "accepted_capabilities_json", "version").From("memory_offer_acceptance").Where("robot_id=?", robotID).OrderAsc("id")
	if forUpdate {
		acceptanceQuery = acceptanceQuery.Suffix("FOR UPDATE")
	}
	var acceptances []acceptanceRow
	if _, err := acceptanceQuery.Load(&acceptances); err != nil {
		return nil, err
	}
	acceptanceFacts := map[string]interface{}{}
	for _, row := range acceptances {
		acceptanceFacts[row.ID] = map[string]interface{}{"offer_id": row.OfferID, "offer_revision": row.OfferRevision, "robot_id": row.RobotID, "status": row.Status, "accepted_by_owner_uid": row.AcceptedByOwnerUID, "accepted_capabilities": row.AcceptedJSON, "version": row.Version}
	}
	out["acceptances"] = acceptanceFacts

	type grantRow struct {
		ID                 string     `db:"id"`
		BankID             string     `db:"bank_id"`
		Capability         string     `db:"capability"`
		Effect             string     `db:"effect"`
		SourceKind         string     `db:"source_kind"`
		PolicyVersionID    string     `db:"policy_version_id"`
		SourceOfferID      *string    `db:"source_offer_id"`
		SourceAcceptanceID *string    `db:"source_acceptance_id"`
		GrantSetVersion    int64      `db:"grant_set_version"`
		ValidFrom          *time.Time `db:"valid_from"`
		ValidUntil         *time.Time `db:"valid_until"`
		RevokedAt          *time.Time `db:"revoked_at"`
	}
	grantQuery := runner.Select("id", "bank_id", "capability", "effect", "source_kind", "source_offer_id", "source_acceptance_id", "grant_set_version", "policy_version_id", "valid_from", "valid_until", "revoked_at").From("memory_grant").Where("principal_type=? AND principal_id=?", PrincipalTypeAgent, robotID).OrderAsc("id")
	if forUpdate {
		grantQuery = grantQuery.Suffix("FOR UPDATE")
	}
	var grants []grantRow
	if _, err := grantQuery.Load(&grants); err != nil {
		return nil, err
	}
	grantFacts := map[string]interface{}{}
	for _, row := range grants {
		grantFacts[row.ID] = map[string]interface{}{"bank_id": row.BankID, "capability": row.Capability, "effect": row.Effect, "source_kind": row.SourceKind, "source_offer_id": row.SourceOfferID, "source_acceptance_id": row.SourceAcceptanceID, "grant_set_version": row.GrantSetVersion, "policy_version_id": row.PolicyVersionID, "valid_from": row.ValidFrom, "valid_until": row.ValidUntil, "revoked_at": row.RevokedAt}
	}
	out["grants"] = grantFacts

	policyQuery := runner.Select("id", "version", "document_hash", "status").From("memory_policy_version").Where("id=?", profile.PolicyVersionID)
	if forUpdate {
		policyQuery = policyQuery.Suffix("FOR UPDATE")
	}
	var policy struct {
		ID           string `db:"id"`
		Version      string `db:"version"`
		DocumentHash string `db:"document_hash"`
		Status       string `db:"status"`
	}
	if err := policyQuery.LoadOne(&policy); err != nil {
		return nil, err
	}
	out["policy"] = map[string]interface{}{"id": policy.ID, "version": policy.Version, "hash": policy.DocumentHash, "status": policy.Status}
	return out, nil
}
