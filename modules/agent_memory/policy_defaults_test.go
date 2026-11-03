package agent_memory

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestBankPolicyDefaultsV1Fixture(t *testing.T) {
	expectedDefaults := []BankClassDefaults{
		{Version: BankPolicyDefaultsVersionV1, Class: BankClassPrivate, RetentionClass: RetentionClassPersonal, ReviewInterval: 180 * 24 * time.Hour},
		{Version: BankPolicyDefaultsVersionV1, Class: BankClassTentative, RetentionClass: RetentionClassTransient, ReviewInterval: 7 * 24 * time.Hour, RetentionPeriod: 30 * 24 * time.Hour, AutoPromotionEligible: true, AutoStaleEligible: true, AutoArchiveEligible: true},
		{Version: BankPolicyDefaultsVersionV1, Class: BankClassShared, RetentionClass: RetentionClassProject, ReviewInterval: 90 * 24 * time.Hour, RetentionPeriod: 365 * 24 * time.Hour, AutoPromotionEligible: true, AutoStaleEligible: true, AutoArchiveEligible: true},
		{Version: BankPolicyDefaultsVersionV1, Class: BankClassVerified, RetentionClass: RetentionClassDurable, ReviewInterval: 180 * 24 * time.Hour},
		{Version: BankPolicyDefaultsVersionV1, Class: BankClassQuarantine, RetentionClass: RetentionClassQuarantine, ReviewInterval: 24 * time.Hour, RetentionPeriod: 30 * 24 * time.Hour, AutoStaleEligible: true, AutoArchiveEligible: true},
	}
	if got := BankClassDefaultsV1(); !reflect.DeepEqual(got, expectedDefaults) {
		t.Fatalf("BankClassDefaultsV1() = %#v, want %#v", got, expectedDefaults)
	}

	expectedTemplates := []BankTemplate{
		{ID: "agent-private", Version: BankPolicyDefaultsVersionV1, Class: BankClassPrivate, BankType: "private", ScopeType: BankScopeAgent, Sensitivity: "confidential", Defaults: expectedDefaults[0]},
		{ID: "agent-tentative", Version: BankPolicyDefaultsVersionV1, Class: BankClassTentative, BankType: "tentative", ScopeType: BankScopeAgent, Sensitivity: "confidential", Defaults: expectedDefaults[1]},
		{ID: "space-shared", Version: BankPolicyDefaultsVersionV1, Class: BankClassShared, BankType: "shared", ScopeType: BankScopeSpace, Sensitivity: "internal", Defaults: expectedDefaults[2]},
		{ID: "space-verified", Version: BankPolicyDefaultsVersionV1, Class: BankClassVerified, BankType: "verified", ScopeType: BankScopeSpace, Sensitivity: "internal", Defaults: expectedDefaults[3]},
		{ID: "organization-verified", Version: BankPolicyDefaultsVersionV1, Class: BankClassVerified, BankType: "verified", ScopeType: BankScopeOrganization, Sensitivity: "internal", Defaults: expectedDefaults[3]},
		{ID: "system-quarantine", Version: BankPolicyDefaultsVersionV1, Class: BankClassQuarantine, BankType: "quarantine", ScopeType: BankScopeOrganization, Sensitivity: "restricted", SystemOnly: true, Defaults: expectedDefaults[4]},
	}
	if got := BankTemplatesV1(); !reflect.DeepEqual(got, expectedTemplates) {
		t.Fatalf("BankTemplatesV1() = %#v, want %#v", got, expectedTemplates)
	}
}

func TestBankClassDefaultsV1AreClassSpecific(t *testing.T) {
	private, _ := BankClassDefaultV1(BankClassPrivate)
	tentative, _ := BankClassDefaultV1(BankClassTentative)
	shared, _ := BankClassDefaultV1(BankClassShared)
	verified, _ := BankClassDefaultV1(BankClassVerified)
	quarantine, _ := BankClassDefaultV1(BankClassQuarantine)

	if private.RetentionPeriod != 0 || verified.RetentionPeriod != 0 {
		t.Fatal("private and verified durable defaults must remain until superseded or revoked")
	}
	if tentative.RetentionPeriod == shared.RetentionPeriod || tentative.ReviewInterval == shared.ReviewInterval {
		t.Fatal("tentative and shared classes must not use one global TTL/review interval")
	}
	if quarantine.AutoPromotionEligible || !tentative.AutoPromotionEligible || !shared.AutoPromotionEligible {
		t.Fatal("promotion eligibility does not match trust classes")
	}
	if !tentative.AutoStaleEligible || !shared.AutoStaleEligible || !quarantine.AutoStaleEligible || private.AutoStaleEligible || verified.AutoStaleEligible {
		t.Fatal("stale eligibility does not match bounded versus durable classes")
	}
	if !tentative.AutoArchiveEligible || !shared.AutoArchiveEligible || !quarantine.AutoArchiveEligible || private.AutoArchiveEligible || verified.AutoArchiveEligible {
		t.Fatal("archive eligibility does not match bounded versus durable classes")
	}
}

func TestBankPolicyDefaultsV1ReturnsDefensiveCopies(t *testing.T) {
	defaults := BankClassDefaultsV1()
	defaults[0].RetentionClass = "mutated"
	defaults = append(defaults, BankClassDefaults{Class: "invented"})
	if got, _ := BankClassDefaultV1(BankClassPrivate); got.RetentionClass != RetentionClassPersonal {
		t.Fatalf("private retention class = %q after caller mutation", got.RetentionClass)
	}
	if len(BankClassDefaultsV1()) != 5 {
		t.Fatal("caller append mutated V1 class defaults")
	}

	templates := BankTemplatesV1()
	templates[0].ID = "mutated"
	templates[0].Defaults.RetentionClass = "mutated"
	if got, ok := BankTemplateV1(BankScopeAgent, BankClassPrivate); !ok || got.ID != "agent-private" || got.Defaults.RetentionClass != RetentionClassPersonal {
		t.Fatalf("BankTemplateV1() = %+v, %t after caller mutation", got, ok)
	}
}

func TestBankPolicyDefaultsV1ReservedQuarantine(t *testing.T) {
	quarantine, ok := BankTemplateV1(BankScopeOrganization, BankClassQuarantine)
	if !ok || !quarantine.SystemOnly || quarantine.Sensitivity != "restricted" || quarantine.Defaults.AutoPromotionEligible {
		t.Fatalf("quarantine template = %+v, %t", quarantine, ok)
	}
	for _, template := range UserCreatableBankTemplatesV1() {
		if template.Class == BankClassQuarantine || template.SystemOnly {
			t.Fatalf("reserved template returned as user creatable: %+v", template)
		}
	}
	if len(UserCreatableBankTemplatesV1()) != 5 {
		t.Fatalf("user creatable template count = %d, want 5", len(UserCreatableBankTemplatesV1()))
	}
}

func TestBankPolicyDefaultsV1LookupDeterministicAndRejectsUnknownClass(t *testing.T) {
	first, err := BankTemplatesByClassV1(BankClassVerified)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BankTemplatesByClassV1(BankClassVerified)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first) != 2 || first[0].ID != "space-verified" || first[1].ID != "organization-verified" {
		t.Fatalf("verified lookup is not deterministic: %#v / %#v", first, second)
	}
	if _, err := BankClassDefaultV1("unknown"); !errors.Is(err, ErrUnknownBankClass) {
		t.Fatalf("BankClassDefaultV1(unknown) error = %v", err)
	}
	if _, err := BankTemplatesByClassV1("unknown"); !errors.Is(err, ErrUnknownBankClass) {
		t.Fatalf("BankTemplatesByClassV1(unknown) error = %v", err)
	}
}

func TestValidateBankPolicyDefaultsRejectsIncompleteOrContradictoryFixtures(t *testing.T) {
	defaults := BankClassDefaultsV1()
	templates := BankTemplatesV1()
	mutateClass := func(class BankClass, mutate func(*BankClassDefaults)) ([]BankClassDefaults, []BankTemplate) {
		changedDefaults := replaceClassDefaults(defaults, class, mutate)
		changedTemplates := append([]BankTemplate(nil), templates...)
		for index := range changedTemplates {
			if changedTemplates[index].Class == class {
				changedTemplates[index].Defaults, _ = findClassDefaults(changedDefaults, class)
			}
		}
		return changedDefaults, changedTemplates
	}
	nonpositiveReviewDefaults, nonpositiveReviewTemplates := mutateClass(BankClassTentative, func(value *BankClassDefaults) { value.ReviewInterval = 0 })
	unboundedStaleDefaults, unboundedStaleTemplates := mutateClass(BankClassTentative, func(value *BankClassDefaults) { value.RetentionPeriod = 0 })
	archiveWithoutStaleDefaults, archiveWithoutStaleTemplates := mutateClass(BankClassTentative, func(value *BankClassDefaults) { value.AutoStaleEligible = false })
	quarantinePromotionDefaults, quarantinePromotionTemplates := mutateClass(BankClassQuarantine, func(value *BankClassDefaults) { value.AutoPromotionEligible = true })

	tests := []struct {
		name      string
		defaults  []BankClassDefaults
		templates []BankTemplate
	}{
		{name: "missing class", defaults: defaults[:len(defaults)-1], templates: templates},
		{name: "missing template", defaults: defaults, templates: templates[:len(templates)-1]},
		{name: "nonpositive review", defaults: nonpositiveReviewDefaults, templates: nonpositiveReviewTemplates},
		{name: "unbounded automatic stale", defaults: unboundedStaleDefaults, templates: unboundedStaleTemplates},
		{name: "archive without stale", defaults: archiveWithoutStaleDefaults, templates: archiveWithoutStaleTemplates},
		{name: "quarantine promotion", defaults: quarantinePromotionDefaults, templates: quarantinePromotionTemplates},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBankPolicyDefaults(BankPolicyDefaultsVersionV1, test.defaults, test.templates); err == nil {
				t.Fatal("validateBankPolicyDefaults() error = nil")
			}
		})
	}
}

func findClassDefaults(defaults []BankClassDefaults, class BankClass) (BankClassDefaults, bool) {
	for _, value := range defaults {
		if value.Class == class {
			return value, true
		}
	}
	return BankClassDefaults{}, false
}

func replaceClassDefaults(defaults []BankClassDefaults, class BankClass, mutate func(*BankClassDefaults)) []BankClassDefaults {
	result := append([]BankClassDefaults(nil), defaults...)
	for index := range result {
		if result[index].Class == class {
			mutate(&result[index])
			break
		}
	}
	return result
}
