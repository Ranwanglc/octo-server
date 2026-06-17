package messages_search

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/olivere/elastic"
)

// effectiveFilterSamples maps each exported SearchFilters field to a request
// in which ONLY that field is set to a representative, effective value.
//
// This registry is the anti-drift hinge between the validator guard
// (validate.go::hasEffectiveFilters) and the DSL builder
// (dsl.go::addCommonFilters). Both hardcode the same field set independently,
// so adding a new shared filter (e.g. message_types) to one without the other
// silently breaks the "empty keyword + new filter" listing path:
//   - wired into addCommonFilters but NOT hasEffectiveFilters → a legitimate
//     empty-keyword search carrying only the new filter is wrongly rejected 400.
//   - wired into hasEffectiveFilters but NOT addCommonFilters → the guard lets
//     the request through but the DSL emits no clause, degenerating into the
//     full-channel scan the guard exists to prevent.
//
// TestSearchFilters_NoUnregisteredFields below uses reflection to force a new
// struct field to appear here; the per-sample assertions then fail until BOTH
// the guard and the builder agree on it. Keeping this registry complete is the
// invariant that lets the two functions stay hardcoded yet provably in sync.
var effectiveFilterSamples = map[string]SearchFilters{
	"SenderIDs":  {SenderIDs: []string{"u1"}},
	"SentAtFrom": {SentAtFrom: "2026-01-01"},
	"SentAtTo":   {SentAtTo: "2026-12-31"},
}

// TestSearchFilters_NoUnregisteredFields fails when a new exported field is
// added to SearchFilters without registering an effective sample above. The
// failure message points the author at the three places a shared filter must
// be wired so the guard and the builder cannot drift apart.
func TestSearchFilters_NoUnregisteredFields(t *testing.T) {
	ft := reflect.TypeOf(SearchFilters{})
	for i := 0; i < ft.NumField(); i++ {
		name := ft.Field(i).Name
		if _, ok := effectiveFilterSamples[name]; !ok {
			t.Fatalf("SearchFilters.%s has no effectiveFilterSamples entry.\n"+
				"A new shared filter must be wired in THREE places that must stay in lockstep:\n"+
				"  1) validate.go::hasEffectiveFilters — so empty-keyword + this filter passes the guard\n"+
				"  2) dsl.go::addCommonFilters — so it emits an OpenSearch clause (no full-channel scan)\n"+
				"  3) effectiveFilterSamples (this test) — a sample proving 1 and 2 agree", name)
		}
	}
}

// TestEffectiveFilterSamples_GuardAndBuilderAgree is the actual drift assertion.
// For every registered field, the validator guard must count it as effective
// AND the DSL builder must emit a filter clause for it. If either side forgets
// the field, exactly one of these fails — making the inconsistency a red test
// rather than a production 400 or a silent full-channel scan.
func TestEffectiveFilterSamples_GuardAndBuilderAgree(t *testing.T) {
	for field, sample := range effectiveFilterSamples {
		// Guard side: hasEffectiveFilters must recognise the lone filter.
		if !hasEffectiveFilters(sample) {
			t.Errorf("hasEffectiveFilters(%s sample)=false; validator would reject a legitimate "+
				"empty-keyword + %s search (guard out of sync with addCommonFilters)", field, field)
		}

		// Builder side: addCommonFilters must emit at least one OS clause.
		b := elastic.NewBoolQuery()
		addCommonFilters(b, sample)
		src, err := b.Source()
		if err != nil {
			t.Fatalf("%s: bool Source(): %v", field, err)
		}
		js, err := json.Marshal(src)
		if err != nil {
			t.Fatalf("%s: marshal: %v", field, err)
		}
		if !strings.Contains(string(js), `"filter"`) {
			t.Errorf("addCommonFilters(%s sample) emitted no filter clause; an empty-keyword search "+
				"carrying only this filter would degenerate into a full-channel scan even though the "+
				"guard let it through:\n%s", field, js)
		}
	}
}
