package agent_memory

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gocraft/dbr/v2"
	"github.com/google/uuid"
)

var (
	ErrPersistenceConflict  = errors.New("agent memory persistence conflict")
	ErrPersistenceForbidden = errors.New("agent memory persistence forbidden")
	ErrPersistenceNotFound  = errors.New("agent memory persistence record not found")
)

var (
	persistenceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,119}$`)
	hashPattern           = regexp.MustCompile(`^[a-f0-9]{64}$`)
	memoryIDPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,119}$`)
	reasonCodePattern     = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,79}$`)
)

type PersistenceService struct {
	store    *Store
	contexts persistenceContextResolver
	now      func() time.Time
	newID    func() string
}

type persistenceContextResolver interface {
	Resolve(agentContextRequest) (*effectiveAgentContext, error)
}

type PersistencePrepare struct {
	BearerToken       string
	BankID            string
	IdempotencyKey    string
	CanonicalKey      string
	LifecycleState    string
	FactType          string
	AssertionKind     string
	Confidence        float64
	ContentHash       string
	OriginFingerprint string
	EvidenceIDs       []string
	SourceInstanceID  string
	GateRunID         string
	ExpectedVersion   int64
}

type PersistenceMutation struct {
	BearerToken       string
	ControlID         string
	ExpectedVersion   int64
	HindsightMemoryID string
	LifecycleState    string
	ReasonCode        string
	Retryable         bool
	Reconciliation    bool
}

type PersistenceRecord struct {
	ControlID         string   `json:"control_id"`
	BankID            string   `json:"bank_id"`
	State             string   `json:"state"`
	PersistenceStatus string   `json:"persistence_status"`
	LifecycleState    string   `json:"lifecycle_state"`
	ReasonCode        string   `json:"reason_code,omitempty"`
	Version           int64    `json:"version"`
	EvidenceIDs       []string `json:"evidence_ids,omitempty"`
}

func NewPersistenceService(store *Store, contexts persistenceContextResolver) *PersistenceService {
	return &PersistenceService{store: store, contexts: contexts, now: time.Now, newID: uuid.NewString}
}

func (service *PersistenceService) Prepare(input PersistencePrepare) (*PersistenceRecord, error) {
	if service == nil || service.store == nil || service.contexts == nil || input.ExpectedVersion != 0 || !validPrepare(input) {
		return nil, ErrPersistenceForbidden
	}
	context, err := service.contexts.Resolve(agentContextRequest{BearerToken: input.BearerToken, Operation: CapabilityPropose, RequestedBankID: input.BankID})
	if err != nil || len(context.AllowedBanks) != 1 || context.AllowedBanks[0].BankID != input.BankID {
		return nil, ErrPersistenceForbidden
	}
	item := KnowledgeItem{
		ID: service.newID(), BankID: input.BankID, IdempotencyKey: input.IdempotencyKey,
		CanonicalKey: input.CanonicalKey, State: "persisting", PersistenceStatus: "persisting",
		LifecycleState: input.LifecycleState, FactType: input.FactType,
		AuthenticatedSourceAgentUID: context.AgentUID, OwnerUID: context.OwnerUID, SpaceID: context.SpaceID,
		CredentialEpoch: context.CredentialEpoch, AssertionKind: input.AssertionKind,
		OriginFingerprint: input.OriginFingerprint, Confidence: input.Confidence,
		Sensitivity: context.AllowedBanks[0].Sensitivity, PolicyVersionID: context.PolicyVersionID,
		ContentHash: input.ContentHash,
	}
	if input.SourceInstanceID != "" {
		item.SourceInstanceID = &input.SourceInstanceID
	}
	if input.GateRunID != "" {
		item.GateRunID = &input.GateRunID
	}
	var result *PersistenceRecord
	err = service.store.WithTx(func(tx *dbr.Tx) error {
		existing, loadErr := loadPersistenceByKeys(tx, input.BankID, input.IdempotencyKey, input.CanonicalKey)
		if loadErr == nil {
			existingEvidence, evidenceErr := loadPersistenceEvidence(tx, existing.ID)
			if evidenceErr != nil {
				return fmt.Errorf("evidence: %w", evidenceErr)
			}
			if !sameImmutable(existing, &item) {
				return fmt.Errorf("%w: immutable", ErrPersistenceConflict)
			}
			if !sameStrings(existingEvidence, normalizedEvidence(input.EvidenceIDs)) {
				return ErrPersistenceConflict
			}
			result = persistenceRecord(existing, existingEvidence)
			return nil
		}
		if !errors.Is(loadErr, ErrPersistenceNotFound) {
			return loadErr
		}
		_, insertErr := tx.InsertInto("knowledge_item").Columns(
			"id", "bank_id", "idempotency_key", "canonical_key", "state", "persistence_status", "lifecycle_state",
			"fact_type", "authenticated_source_agent_uid", "owner_uid", "space_id", "credential_epoch", "source_instance_id",
			"assertion_kind", "origin_fingerprint", "confidence", "sensitivity", "policy_version_id", "gate_run_id", "content_hash", "version",
		).Record(&item).Exec()
		if insertErr != nil {
			existing, reloadErr := loadPersistenceByKeys(tx, input.BankID, input.IdempotencyKey, input.CanonicalKey)
			if reloadErr == nil && sameImmutable(existing, &item) {
				result = persistenceRecord(existing, input.EvidenceIDs)
				return nil
			}
			if reloadErr == nil {
				return ErrPersistenceConflict
			}
			return fmt.Errorf("agent memory: insert persistence control: %w", insertErr)
		}
		for _, evidenceID := range normalizedEvidence(input.EvidenceIDs) {
			_, insertErr = tx.InsertInto("knowledge_evidence").Columns(
				"id", "knowledge_item_id", "evidence_item_id", "source_agent_uid", "origin_fingerprint", "relation", "independent",
			).Values(service.newID(), item.ID, evidenceID, context.AgentUID, input.OriginFingerprint, "supports", true).Exec()
			if insertErr != nil {
				return fmt.Errorf("agent memory: insert persistence evidence: %w", insertErr)
			}
		}
		transition := KnowledgeTransition{ID: service.newID(), KnowledgeItemID: item.ID, FromState: "candidate", ToState: "persisting", ActorType: "agent", ActorID: context.AgentUID, ReasonCode: "PERSISTENCE_PREPARED", PolicyVersionID: context.PolicyVersionID, IdempotencyKey: input.IdempotencyKey + ":prepare"}
		if err := service.store.CreateTransition(tx, &transition); err != nil {
			return err
		}
		if err := appendPersistenceAudit(tx, service.newID(), "persistence.prepare", context, item.ID, "success", "PERSISTENCE_PREPARED", input.IdempotencyKey, input.ContentHash); err != nil {
			return err
		}
		result = persistenceRecord(&item, input.EvidenceIDs)
		return nil
	})
	return result, err
}

func (service *PersistenceService) Complete(input PersistenceMutation) (*PersistenceRecord, error) {
	if !memoryIDPattern.MatchString(input.HindsightMemoryID) || !validLifecycle(input.LifecycleState) {
		return nil, ErrPersistenceForbidden
	}
	return service.mutate(input, "stored")
}

func (service *PersistenceService) Fail(input PersistenceMutation) (*PersistenceRecord, error) {
	if !reasonCodePattern.MatchString(input.ReasonCode) {
		return nil, ErrPersistenceForbidden
	}
	status := "nonretryable_failed"
	if input.Reconciliation {
		status = "reconciliation_required"
	} else if input.Retryable {
		status = "retryable_failed"
	}
	return service.mutate(input, status)
}

func (service *PersistenceService) Status(input PersistenceMutation) (*PersistenceRecord, error) {
	context, err := service.contexts.Resolve(agentContextRequest{BearerToken: input.BearerToken, Operation: CapabilityPropose})
	if err != nil {
		return nil, ErrPersistenceForbidden
	}
	item, err := loadPersistence(service.store.session, input.ControlID)
	if err != nil || item.AuthenticatedSourceAgentUID != context.AgentUID || item.OwnerUID != context.OwnerUID || item.PolicyVersionID != context.PolicyVersionID || item.CredentialEpoch != context.CredentialEpoch {
		return nil, ErrPersistenceForbidden
	}
	return persistenceRecord(item, nil), nil
}

func (service *PersistenceService) mutate(input PersistenceMutation, status string) (*PersistenceRecord, error) {
	if service == nil || service.contexts == nil || service.store == nil || !persistenceKeyPattern.MatchString(input.ControlID) || input.ExpectedVersion < 0 {
		return nil, ErrPersistenceForbidden
	}
	context, err := service.contexts.Resolve(agentContextRequest{BearerToken: input.BearerToken, Operation: CapabilityPropose})
	if err != nil {
		return nil, ErrPersistenceForbidden
	}
	var result *PersistenceRecord
	err = service.store.WithTx(func(tx *dbr.Tx) error {
		item, loadErr := loadPersistence(tx, input.ControlID)
		if loadErr != nil {
			return loadErr
		}
		if item.AuthenticatedSourceAgentUID != context.AgentUID || item.OwnerUID != context.OwnerUID || item.PolicyVersionID != context.PolicyVersionID || item.CredentialEpoch != context.CredentialEpoch || !contextHasBank(context, item.BankID) {
			return ErrPersistenceForbidden
		}
		if status == "stored" && item.PersistenceStatus == "stored" {
			if item.HindsightMemoryID != nil && *item.HindsightMemoryID == input.HindsightMemoryID && item.LifecycleState == input.LifecycleState {
				result = persistenceRecord(item, nil)
				return nil
			}
			return ErrPersistenceConflict
		}
		if item.Version != input.ExpectedVersion {
			return ErrVersionConflict
		}
		if status == "stored" && item.LifecycleState != input.LifecycleState {
			return ErrPersistenceConflict
		}
		toState := item.State
		memoryID := item.HindsightMemoryID
		if status == "stored" {
			toState = input.LifecycleState
			memoryID = &input.HindsightMemoryID
		}
		resultSQL, updateErr := tx.Update("knowledge_item").Set("state", toState).Set("persistence_status", status).
			Set("hindsight_memory_id", memoryID).Set("failure_reason_code", input.ReasonCode).Set("version", dbr.Expr("version + 1")).
			Where("id=? AND version=?", item.ID, input.ExpectedVersion).Exec()
		if updateErr != nil {
			return updateErr
		}
		rows, _ := resultSQL.RowsAffected()
		if rows != 1 {
			return ErrVersionConflict
		}
		transition := KnowledgeTransition{ID: service.newID(), KnowledgeItemID: item.ID, FromState: item.State, ToState: toState, ActorType: "agent", ActorID: context.AgentUID, ReasonCode: mutationReason(status, input.ReasonCode), PolicyVersionID: context.PolicyVersionID, IdempotencyKey: fmt.Sprintf("%s:%s:%d", item.IdempotencyKey, status, input.ExpectedVersion)}
		if err := service.store.CreateTransition(tx, &transition); err != nil {
			return err
		}
		if err := appendPersistenceAudit(tx, service.newID(), "persistence."+status, context, item.ID, "success", mutationReason(status, input.ReasonCode), item.IdempotencyKey, item.ContentHash); err != nil {
			return err
		}
		item.State, item.PersistenceStatus, item.HindsightMemoryID, item.FailureReasonCode, item.Version = toState, status, memoryID, input.ReasonCode, item.Version+1
		result = persistenceRecord(item, nil)
		return nil
	})
	return result, err
}

func loadPersistence(exec dbr.SessionRunner, controlID string) (*KnowledgeItem, error) {
	var item KnowledgeItem
	err := exec.Select("*").From("knowledge_item").Where("id=?", controlID).LoadOne(&item)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrPersistenceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func loadPersistenceByKeys(exec dbr.SessionRunner, bankID, idempotencyKey, canonicalKey string) (*KnowledgeItem, error) {
	var item KnowledgeItem
	err := exec.Select("*").From("knowledge_item").Where("bank_id=? AND (idempotency_key=? OR canonical_key=?)", bankID, idempotencyKey, canonicalKey).LoadOne(&item)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, dbr.ErrNotFound) {
		return nil, ErrPersistenceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func loadPersistenceEvidence(exec dbr.SessionRunner, controlID string) ([]string, error) {
	var rows []struct {
		EvidenceItemID *string `db:"evidence_item_id"`
	}
	_, err := exec.Select("evidence_item_id").From("knowledge_evidence").Where("knowledge_item_id=?", controlID).OrderAsc("evidence_item_id").Load(&rows)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.EvidenceItemID != nil {
			values = append(values, *row.EvidenceItemID)
		}
	}
	return values, nil
}

func appendPersistenceAudit(exec auditInserter, id, eventType string, context *effectiveAgentContext, controlID, decision, reasonCode, requestID, contentHash string) error {
	_, err := exec.InsertInto("memory_audit_event").Columns(
		"id", "event_type", "principal_agent_uid", "principal_user_uid", "space_id", "resource_type", "resource_id",
		"capability", "decision", "reason_code", "request_id", "policy_version_id", "content_hash", "created_at",
	).Values(id, eventType, context.AgentUID, context.OwnerUID, context.SpaceID, "knowledge_item", controlID,
		CapabilityPropose, decision, reasonCode, requestID, context.PolicyVersionID, contentHash, time.Now()).Exec()
	return err
}

func validPrepare(input PersistencePrepare) bool {
	return persistenceKeyPattern.MatchString(input.BankID) && persistenceKeyPattern.MatchString(input.IdempotencyKey) && hashPattern.MatchString(input.CanonicalKey) && hashPattern.MatchString(input.ContentHash) && hashPattern.MatchString(input.OriginFingerprint) && validLifecycle(input.LifecycleState) && validFactType(input.FactType) && (input.AssertionKind == "firsthand" || input.AssertionKind == "relay" || input.AssertionKind == "derived") && input.Confidence >= 0 && input.Confidence <= 1 && len(input.EvidenceIDs) <= 32
}
func validLifecycle(value string) bool {
	return value == "tentative" || value == "corroborated" || value == "verified"
}
func validFactType(value string) bool {
	return value == "world" || value == "experience" || value == "opinion" || value == "preference" || value == "procedure"
}
func contextHasBank(context *effectiveAgentContext, bankID string) bool {
	for _, bank := range context.AllowedBanks {
		if bank.BankID == bankID {
			return true
		}
	}
	return false
}
func normalizedEvidence(values []string) []string {
	unique := map[string]struct{}{}
	for _, value := range values {
		if persistenceKeyPattern.MatchString(value) {
			unique[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(unique))
	for value := range unique {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
func sameImmutable(left, right *KnowledgeItem) bool {
	return left.BankID == right.BankID && left.CanonicalKey == right.CanonicalKey && left.LifecycleState == right.LifecycleState && left.FactType == right.FactType && left.AuthenticatedSourceAgentUID == right.AuthenticatedSourceAgentUID && left.OwnerUID == right.OwnerUID && sameStringPointer(left.SpaceID, right.SpaceID) && left.CredentialEpoch == right.CredentialEpoch && left.AssertionKind == right.AssertionKind && left.OriginFingerprint == right.OriginFingerprint && left.PolicyVersionID == right.PolicyVersionID && left.ContentHash == right.ContentHash && math.Abs(left.Confidence-right.Confidence) < 0.0005
}
func persistenceRecord(item *KnowledgeItem, evidence []string) *PersistenceRecord {
	return &PersistenceRecord{ControlID: item.ID, BankID: item.BankID, State: item.State, PersistenceStatus: item.PersistenceStatus, LifecycleState: item.LifecycleState, ReasonCode: item.FailureReasonCode, Version: item.Version, EvidenceIDs: normalizedEvidence(evidence)}
}
func mutationReason(status, reason string) string {
	if reason != "" {
		return reason
	}
	return strings.ToUpper(status)
}
