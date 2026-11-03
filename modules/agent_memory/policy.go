package agent_memory

import (
	"encoding/json"
	"errors"
	"time"
)

type PrincipalType string

const (
	PrincipalTypeAgent     PrincipalType = "agent"
	PrincipalTypeSpaceRole PrincipalType = "space_role"
	PrincipalTypeUser      PrincipalType = "user"
)

type Capability string

const (
	CapabilityRecall  Capability = "recall"
	CapabilityPropose Capability = "propose"
	CapabilityCurate  Capability = "curate"
	CapabilityAdmin   Capability = "admin"
)

type GrantEffect string

const (
	GrantEffectAllow GrantEffect = "allow"
	GrantEffectDeny  GrantEffect = "deny"
)

type PolicyReasonCode string

const (
	PolicyReasonAllowed   PolicyReasonCode = "MEMORY_ALLOWED"
	PolicyReasonForbidden PolicyReasonCode = "MEMORY_FORBIDDEN"
)

type PolicyPrincipal struct {
	Type           PrincipalType
	ID             string
	OrganizationID string
}

type CapabilityRequest struct {
	Principal   PolicyPrincipal
	Profile     AgentMemoryProfile
	Bank        MemoryBank
	Capability  Capability
	Grants      []MemoryGrant
	Policy      *MemoryPolicyVersion
	Now         time.Time
	RobotActive *bool // nil when the caller already authenticated the Robot
}

var sensitivityRank = map[string]int{"internal": 0, "confidential": 1, "restricted": 2}

type PolicyDecision struct {
	Allowed    bool
	ReasonCode PolicyReasonCode
	Cause      string // internal explanation; wire authorization stays anti-enumerating
}

func ResolveCapability(request CapabilityRequest) PolicyDecision {
	if cause := request.invalidCause(); cause != "" {
		return forbiddenDecisionCause(cause)
	}

	allowed := false
	for _, grant := range request.Grants {
		if !grantMatches(request, grant) {
			continue
		}

		switch GrantEffect(grant.Effect) {
		case GrantEffectDeny:
			return forbiddenDecisionCause("EXPLICIT_DENY")
		case GrantEffectAllow:
			allowed = true
		}
	}

	if !allowed {
		return forbiddenDecisionCause("NO_ALLOW")
	}
	return PolicyDecision{Allowed: true, ReasonCode: PolicyReasonAllowed}
}

func (request CapabilityRequest) valid() bool { return request.invalidCause() == "" }

func (request CapabilityRequest) invalidCause() string {
	if request.Principal.Type != PrincipalTypeAgent || request.Principal.ID == "" || request.Principal.ID != request.Profile.AgentUID {
		return "PRINCIPAL_MISMATCH"
	}
	if request.RobotActive != nil && !*request.RobotActive {
		return "ROBOT_INACTIVE"
	}
	if request.Profile.ValidateScope() != nil || request.Bank.ValidateScope() != nil || !scopeMatches(request) {
		return "SCOPE_INELIGIBLE"
	}
	if !knownCapability(request.Capability) {
		return "UNKNOWN_CAPABILITY"
	}
	if request.Policy == nil || request.Policy.ID != request.Profile.PolicyVersionID || request.Policy.Status != "active" || !policyAllows(request.Policy.DocumentJSON, request.Capability) {
		return "POLICY_CEILING"
	}
	if !policyAllowsSensitivity(request.Policy.DocumentJSON, request.Bank.Sensitivity) {
		return "SENSITIVITY_CEILING"
	}
	if !request.Profile.Enabled || request.Profile.Status != "active" {
		return "PROFILE_DISABLED"
	}
	if request.Bank.ID == "" || request.Bank.Status != "active" || (request.Bank.PolicyVersionID != "" && request.Bank.PolicyVersionID != request.Profile.PolicyVersionID) {
		return "BANK_UNAVAILABLE"
	}
	return ""
}

// policyAllows parses the versioned policy ceiling. Missing, malformed, or
// unknown capability ceilings deny by default.
func policyAllows(document []byte, capability Capability) bool {
	var raw struct {
		CapabilityCeiling []Capability `json:"capability_ceiling"`
		Capabilities      []Capability `json:"capabilities"`
		Memory            *struct {
			CapabilityCeiling []Capability `json:"capability_ceiling"`
			Capabilities      []Capability `json:"capabilities"`
		} `json:"memory"`
	}
	if len(document) == 0 || json.Unmarshal(document, &raw) != nil {
		return false
	}
	ceiling := raw.CapabilityCeiling
	if len(ceiling) == 0 {
		ceiling = raw.Capabilities
	}
	if len(ceiling) == 0 && raw.Memory != nil {
		ceiling = raw.Memory.CapabilityCeiling
		if len(ceiling) == 0 {
			ceiling = raw.Memory.Capabilities
		}
	}
	for _, allowed := range ceiling {
		if !knownCapability(allowed) {
			return false
		}
		if allowed == capability {
			return true
		}
	}
	return false
}

func policyCeiling(policy *MemoryPolicyVersion) ([]Capability, error) {
	if policy == nil || policy.Status != "active" {
		return nil, ErrPolicyNotFound
	}
	result := make([]Capability, 0, 4)
	for _, capability := range []Capability{CapabilityRecall, CapabilityPropose, CapabilityCurate, CapabilityAdmin} {
		if policyAllows(policy.DocumentJSON, capability) {
			result = append(result, capability)
		}
	}
	if len(result) == 0 {
		return nil, errors.New("policy capability ceiling is empty or invalid")
	}
	return result, nil
}

// policyAllowsSensitivity is deliberately strict: unknown policy or Bank labels deny.
func policyAllowsSensitivity(document []byte, sensitivity string) bool {
	var raw struct {
		SensitivityCeiling string `json:"sensitivity_ceiling"`
		Memory             *struct {
			SensitivityCeiling string `json:"sensitivity_ceiling"`
		} `json:"memory"`
	}
	if json.Unmarshal(document, &raw) != nil {
		return false
	}
	ceiling := raw.SensitivityCeiling
	if ceiling == "" && raw.Memory != nil {
		ceiling = raw.Memory.SensitivityCeiling
	}
	bankRank, bankOK := sensitivityRank[sensitivity]
	ceilingRank, ceilingOK := sensitivityRank[ceiling]
	return bankOK && ceilingOK && bankRank <= ceilingRank
}

func scopeMatches(request CapabilityRequest) bool {
	switch request.Bank.ScopeType {
	case BankScopeAgent:
		return request.Bank.ScopeID == request.Profile.AgentUID
	case BankScopeSpace:
		return request.Profile.ScopeType == ProfileScopeSpace &&
			request.Profile.SpaceID != nil &&
			*request.Profile.SpaceID != "" &&
			request.Bank.ScopeID == *request.Profile.SpaceID
	case BankScopeOrganization:
		return request.Principal.OrganizationID != "" &&
			request.Bank.ScopeID == request.Principal.OrganizationID
	default:
		return false
	}
}

func grantMatches(request CapabilityRequest, grant MemoryGrant) bool {
	if PrincipalType(grant.PrincipalType) != request.Principal.Type || grant.PrincipalID != request.Principal.ID {
		return false
	}
	if grant.BankID != request.Bank.ID || Capability(grant.Capability) != request.Capability {
		return false
	}
	if grant.RevokedAt != nil {
		return false
	}
	if grant.ValidFrom != nil && request.Now.Before(*grant.ValidFrom) {
		return false
	}
	if grant.ValidUntil != nil && !request.Now.Before(*grant.ValidUntil) {
		return false
	}
	return true
}

func knownCapability(capability Capability) bool {
	switch capability {
	case CapabilityRecall, CapabilityPropose, CapabilityCurate, CapabilityAdmin:
		return true
	default:
		return false
	}
}

func forbiddenDecision() PolicyDecision { return forbiddenDecisionCause("") }

func forbiddenDecisionCause(cause string) PolicyDecision {
	return PolicyDecision{ReasonCode: PolicyReasonForbidden, Cause: cause}
}
