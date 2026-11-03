package agent_memory

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

const BankPolicyDefaultsVersionV1 = "bank-policy-defaults-v1"

type BankClass string

const (
	BankClassPrivate    BankClass = "private"
	BankClassTentative  BankClass = "tentative"
	BankClassShared     BankClass = "shared"
	BankClassVerified   BankClass = "verified"
	BankClassQuarantine BankClass = "quarantine"
)

const (
	RetentionClassPersonal   = "personal"
	RetentionClassTransient  = "transient"
	RetentionClassProject    = "project"
	RetentionClassDurable    = "durable"
	RetentionClassQuarantine = "quarantine"
)

var ErrUnknownBankClass = errors.New("unknown bank class")

type BankClassDefaults struct {
	Version               string
	Class                 BankClass
	RetentionClass        string
	ReviewInterval        time.Duration
	RetentionPeriod       time.Duration
	AutoPromotionEligible bool
	AutoStaleEligible     bool
	AutoArchiveEligible   bool
}

type BankTemplate struct {
	ID          string
	Version     string
	Class       BankClass
	BankType    string
	ScopeType   BankScopeType
	Sensitivity string
	SystemOnly  bool
	Defaults    BankClassDefaults
}

func init() {
	defaults := bankClassDefaultsV1Fixture()
	templates := bankTemplatesV1Fixture()
	if err := validateBankPolicyDefaults(BankPolicyDefaultsVersionV1, defaults[:], templates[:]); err != nil {
		panic(err)
	}
}

func BankClassDefaultsV1() []BankClassDefaults {
	fixture := bankClassDefaultsV1Fixture()
	return append([]BankClassDefaults(nil), fixture[:]...)
}

func BankClassDefaultV1(class BankClass) (BankClassDefaults, error) {
	for _, defaults := range bankClassDefaultsV1Fixture() {
		if defaults.Class == class {
			return defaults, nil
		}
	}
	return BankClassDefaults{}, fmt.Errorf("%w: %q", ErrUnknownBankClass, class)
}

func BankTemplatesV1() []BankTemplate {
	fixture := bankTemplatesV1Fixture()
	return append([]BankTemplate(nil), fixture[:]...)
}

func UserCreatableBankTemplatesV1() []BankTemplate {
	fixture := bankTemplatesV1Fixture()
	templates := make([]BankTemplate, 0, len(fixture)-1)
	for _, template := range fixture {
		if !template.SystemOnly {
			templates = append(templates, template)
		}
	}
	return templates
}

func BankTemplatesByClassV1(class BankClass) ([]BankTemplate, error) {
	if _, err := BankClassDefaultV1(class); err != nil {
		return nil, err
	}
	templates := make([]BankTemplate, 0, 2)
	for _, template := range bankTemplatesV1Fixture() {
		if template.Class == class {
			templates = append(templates, template)
		}
	}
	return templates, nil
}

func BankTemplateV1(scopeType BankScopeType, class BankClass) (BankTemplate, bool) {
	for _, template := range bankTemplatesV1Fixture() {
		if template.ScopeType == scopeType && template.Class == class {
			return template, true
		}
	}
	return BankTemplate{}, false
}

func bankClassDefaultsV1Fixture() [5]BankClassDefaults {
	return [5]BankClassDefaults{
		{
			Version: BankPolicyDefaultsVersionV1, Class: BankClassPrivate, RetentionClass: RetentionClassPersonal,
			ReviewInterval: 180 * 24 * time.Hour,
		},
		{
			Version: BankPolicyDefaultsVersionV1, Class: BankClassTentative, RetentionClass: RetentionClassTransient,
			ReviewInterval: 7 * 24 * time.Hour, RetentionPeriod: 30 * 24 * time.Hour,
			AutoPromotionEligible: true, AutoStaleEligible: true, AutoArchiveEligible: true,
		},
		{
			Version: BankPolicyDefaultsVersionV1, Class: BankClassShared, RetentionClass: RetentionClassProject,
			ReviewInterval: 90 * 24 * time.Hour, RetentionPeriod: 365 * 24 * time.Hour,
			AutoPromotionEligible: true, AutoStaleEligible: true, AutoArchiveEligible: true,
		},
		{
			Version: BankPolicyDefaultsVersionV1, Class: BankClassVerified, RetentionClass: RetentionClassDurable,
			ReviewInterval: 180 * 24 * time.Hour,
		},
		{
			Version: BankPolicyDefaultsVersionV1, Class: BankClassQuarantine, RetentionClass: RetentionClassQuarantine,
			ReviewInterval: 24 * time.Hour, RetentionPeriod: 30 * 24 * time.Hour,
			AutoStaleEligible: true, AutoArchiveEligible: true,
		},
	}
}

func bankTemplatesV1Fixture() [6]BankTemplate {
	return [6]BankTemplate{
		newBankTemplateV1("agent-private", BankClassPrivate, BankScopeAgent, "confidential", false),
		newBankTemplateV1("agent-tentative", BankClassTentative, BankScopeAgent, "confidential", false),
		newBankTemplateV1("space-shared", BankClassShared, BankScopeSpace, "internal", false),
		newBankTemplateV1("space-verified", BankClassVerified, BankScopeSpace, "internal", false),
		newBankTemplateV1("organization-verified", BankClassVerified, BankScopeOrganization, "internal", false),
		newBankTemplateV1("system-quarantine", BankClassQuarantine, BankScopeOrganization, "restricted", true),
	}
}

func newBankTemplateV1(id string, class BankClass, scopeType BankScopeType, sensitivity string, systemOnly bool) BankTemplate {
	defaults, err := BankClassDefaultV1(class)
	if err != nil {
		panic(err)
	}
	return BankTemplate{
		ID: id, Version: BankPolicyDefaultsVersionV1, Class: class, BankType: string(class),
		ScopeType: scopeType, Sensitivity: sensitivity, SystemOnly: systemOnly, Defaults: defaults,
	}
}

func validateBankPolicyDefaults(version string, defaults []BankClassDefaults, templates []BankTemplate) error {
	if version == "" {
		return errors.New("bank policy defaults version is required")
	}
	byClass := make(map[BankClass]BankClassDefaults, len(defaults))
	for _, classDefaults := range defaults {
		if classDefaults.Version != version || !isSupportedBankClass(classDefaults.Class) || classDefaults.RetentionClass == "" {
			return fmt.Errorf("invalid bank class defaults: %+v", classDefaults)
		}
		if classDefaults.ReviewInterval <= 0 || classDefaults.ReviewInterval > 10*365*24*time.Hour {
			return fmt.Errorf("bank class %q has invalid review interval", classDefaults.Class)
		}
		if classDefaults.RetentionPeriod < 0 || classDefaults.RetentionPeriod > 10*365*24*time.Hour {
			return fmt.Errorf("bank class %q has invalid retention period", classDefaults.Class)
		}
		if classDefaults.RetentionPeriod == 0 && (classDefaults.AutoStaleEligible || classDefaults.AutoArchiveEligible) {
			return fmt.Errorf("bank class %q requires bounded retention for automatic stale/archive", classDefaults.Class)
		}
		if classDefaults.AutoArchiveEligible && !classDefaults.AutoStaleEligible {
			return fmt.Errorf("bank class %q cannot auto-archive without auto-stale", classDefaults.Class)
		}
		if classDefaults.Class == BankClassQuarantine && classDefaults.AutoPromotionEligible {
			return errors.New("quarantine cannot be auto-promoted")
		}
		if _, exists := byClass[classDefaults.Class]; exists {
			return fmt.Errorf("duplicate bank class defaults %q", classDefaults.Class)
		}
		byClass[classDefaults.Class] = classDefaults
	}
	for _, class := range supportedBankClasses() {
		if _, ok := byClass[class]; !ok {
			return fmt.Errorf("missing bank class defaults %q", class)
		}
	}
	if len(byClass) != len(supportedBankClasses()) {
		return fmt.Errorf("unexpected bank class defaults count %d", len(byClass))
	}

	expected := map[string]struct{}{
		"agent/private": {}, "agent/tentative": {}, "space/shared": {},
		"space/verified": {}, "organization/verified": {}, "organization/quarantine": {},
	}
	seenIDs := make(map[string]struct{}, len(templates))
	seenPairs := make(map[string]struct{}, len(templates))
	for _, template := range templates {
		classDefaults, ok := byClass[template.Class]
		if template.Version != version || template.ID == "" || !ok || template.Defaults != classDefaults {
			return fmt.Errorf("invalid bank template identity/defaults: %+v", template)
		}
		if template.BankType != string(template.Class) {
			return fmt.Errorf("bank template %q class/type mismatch", template.ID)
		}
		if template.Sensitivity != "internal" && template.Sensitivity != "confidential" && template.Sensitivity != "restricted" {
			return fmt.Errorf("bank template %q has invalid sensitivity", template.ID)
		}
		if template.Class == BankClassQuarantine {
			if !template.SystemOnly || template.Sensitivity != "restricted" {
				return fmt.Errorf("bank template %q violates quarantine constraints", template.ID)
			}
		} else if template.SystemOnly {
			return fmt.Errorf("bank template %q unexpectedly system-only", template.ID)
		}
		if _, exists := seenIDs[template.ID]; exists {
			return fmt.Errorf("duplicate bank template id %q", template.ID)
		}
		seenIDs[template.ID] = struct{}{}
		pair := string(template.ScopeType) + "/" + string(template.Class)
		if _, exists := seenPairs[pair]; exists {
			return fmt.Errorf("duplicate bank template pair %q", pair)
		}
		seenPairs[pair] = struct{}{}
	}
	missing := make([]string, 0)
	for pair := range expected {
		if _, ok := seenPairs[pair]; !ok {
			missing = append(missing, pair)
		}
	}
	if len(missing) > 0 || len(seenPairs) != len(expected) {
		sort.Strings(missing)
		return fmt.Errorf("bank template matrix incomplete or unexpected: missing=%v count=%d", missing, len(seenPairs))
	}
	return nil
}

func supportedBankClasses() []BankClass {
	return []BankClass{BankClassPrivate, BankClassTentative, BankClassShared, BankClassVerified, BankClassQuarantine}
}

func isSupportedBankClass(class BankClass) bool {
	switch class {
	case BankClassPrivate, BankClassTentative, BankClassShared, BankClassVerified, BankClassQuarantine:
		return true
	default:
		return false
	}
}
