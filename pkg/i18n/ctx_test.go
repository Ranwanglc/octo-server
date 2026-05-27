package i18n

import (
	"context"
	"testing"
)

func TestContextLanguageRoundTrip(t *testing.T) {
	ctx := WithLanguage(context.Background(), LanguageDecision{
		Language: "zh-CN",
		Source:   LanguageSourceCookie,
	})

	got, ok := LanguageFromContext(ctx)
	if !ok {
		t.Fatal("LanguageFromContext ok=false")
	}
	if got.Language != "zh-CN" || got.Source != LanguageSourceCookie {
		t.Fatalf("LanguageFromContext = %#v", got)
	}
}

func TestWithLanguageIfHigherPriority(t *testing.T) {
	ctx := WithLanguage(context.Background(), LanguageDecision{
		Language: "en-US",
		Source:   LanguageSourceAccept,
	})

	ctx, changed := WithLanguageIfHigherPriority(ctx, LanguageDecision{
		Language: "zh-CN",
		Source:   LanguageSourceUser,
	})
	if !changed {
		t.Fatal("user language should override Accept-Language")
	}
	got, _ := LanguageFromContext(ctx)
	if got.Language != "zh-CN" || got.Source != LanguageSourceUser {
		t.Fatalf("after user override = %#v", got)
	}

	ctx, changed = WithLanguageIfHigherPriority(ctx, LanguageDecision{
		Language: "en-US",
		Source:   LanguageSourceCookie,
	})
	if !changed {
		t.Fatal("cookie language should override user language")
	}
	got, _ = LanguageFromContext(ctx)
	if got.Language != "en-US" || got.Source != LanguageSourceCookie {
		t.Fatalf("after cookie override = %#v", got)
	}

	ctx, changed = WithLanguageIfHigherPriority(ctx, LanguageDecision{
		Language: "zh-CN",
		Source:   LanguageSourceUser,
	})
	if changed {
		t.Fatal("user language must not override explicit cookie language")
	}
	got, _ = LanguageFromContext(ctx)
	if got.Language != "en-US" || got.Source != LanguageSourceCookie {
		t.Fatalf("after lower-priority override = %#v", got)
	}
}

func TestLanguageOrDefault(t *testing.T) {
	if got := LanguageOrDefault(context.Background(), "zh-CN"); got != "zh-CN" {
		t.Fatalf("empty context fallback = %q, want zh-CN", got)
	}

	ctx := WithLanguage(context.Background(), LanguageDecision{
		Language: "en-US",
		Source:   LanguageSourceDefault,
	})
	if got := LanguageOrDefault(ctx, "zh-CN"); got != "en-US" {
		t.Fatalf("context language = %q, want en-US", got)
	}
}
