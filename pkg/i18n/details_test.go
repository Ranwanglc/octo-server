package i18n

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDetails_FilterByCodeSafeDetailKeys(t *testing.T) {
	code, ok := codes.Lookup("err.shared.rate.limited")
	if !ok {
		t.Fatal("err.shared.rate.limited not registered")
	}

	details := Details{
		"retry_after": "30s",
		"token":       "secret-token",
		"raw_err":     "redis down",
	}
	tokenBefore := testutil.ToFloat64(unsafeDetailsDroppedTotal.WithLabelValues(code.ID, "token"))
	rawErrBefore := testutil.ToFloat64(unsafeDetailsDroppedTotal.WithLabelValues(code.ID, "raw_err"))

	filtered := details.FilterBy(code)
	if len(filtered) != 1 {
		t.Fatalf("filtered len = %d, want 1: %#v", len(filtered), filtered)
	}
	if got := filtered["retry_after"]; got != "30s" {
		t.Fatalf("retry_after = %v, want 30s", got)
	}
	if _, ok := filtered["token"]; ok {
		t.Fatal("unsafe key token was not dropped")
	}

	filtered["retry_after"] = "mutated"
	if got := details["retry_after"]; got != "30s" {
		t.Fatalf("FilterBy returned alias; original retry_after = %v", got)
	}

	if got := testutil.ToFloat64(unsafeDetailsDroppedTotal.WithLabelValues(code.ID, "token")) - tokenBefore; got != 1 {
		t.Fatalf("token dropped metric delta = %v, want 1", got)
	}
	if got := testutil.ToFloat64(unsafeDetailsDroppedTotal.WithLabelValues(code.ID, "raw_err")) - rawErrBefore; got != 1 {
		t.Fatalf("raw_err dropped metric delta = %v, want 1", got)
	}
}

func TestDetails_FilterByDropsAllWhenNoSafeKeys(t *testing.T) {
	code, ok := codes.Lookup("err.shared.auth.required")
	if !ok {
		t.Fatal("err.shared.auth.required not registered")
	}

	before := testutil.ToFloat64(unsafeDetailsDroppedTotal.WithLabelValues(code.ID, "field"))
	filtered := Details{"field": "name"}.FilterBy(code)
	if len(filtered) != 0 {
		t.Fatalf("filtered = %#v, want empty", filtered)
	}
	if got := testutil.ToFloat64(unsafeDetailsDroppedTotal.WithLabelValues(code.ID, "field")) - before; got != 1 {
		t.Fatalf("dropped metric delta = %v, want 1", got)
	}
}

func TestDetails_NilFilter(t *testing.T) {
	code, ok := codes.Lookup("err.shared.param.invalid")
	if !ok {
		t.Fatal("err.shared.param.invalid not registered")
	}

	var details Details
	if got := details.FilterBy(code); got != nil {
		t.Fatalf("nil FilterBy = %#v, want nil", got)
	}
}
