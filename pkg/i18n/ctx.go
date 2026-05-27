package i18n

import "context"

type languageContextKey struct{}

// LanguageDecision 记录一次语言协商结果及其来源。
type LanguageDecision struct {
	Language string
	Source   LanguageSource
}

// WithLanguage 将语言协商结果写入 context.Context。
func WithLanguage(ctx context.Context, decision LanguageDecision) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, languageContextKey{}, decision)
}

// WithLanguageIfHigherPriority 仅当新来源优先级高于现有来源时覆盖 context。
// 这用于 D9 两段式中间件：Auth 后的 user.language 可以覆盖 Accept-Language/default，
// 但不能覆盖 trusted header、URL 或 cookie 这类显式选择。
func WithLanguageIfHigherPriority(ctx context.Context, decision LanguageDecision) (context.Context, bool) {
	current, ok := LanguageFromContext(ctx)
	if !ok || languageSourcePriority(decision.Source) > languageSourcePriority(current.Source) {
		return WithLanguage(ctx, decision), true
	}
	return ctx, false
}

// LanguageFromContext 读取 context 中的语言协商结果。
func LanguageFromContext(ctx context.Context) (LanguageDecision, bool) {
	if ctx == nil {
		return LanguageDecision{}, false
	}
	decision, ok := ctx.Value(languageContextKey{}).(LanguageDecision)
	if !ok || decision.Language == "" {
		return LanguageDecision{}, false
	}
	return decision, true
}

// LanguageOrDefault 返回 context 中的语言；不存在时返回 fallback。
func LanguageOrDefault(ctx context.Context, fallback string) string {
	if decision, ok := LanguageFromContext(ctx); ok {
		return decision.Language
	}
	return fallback
}

func languageSourcePriority(source LanguageSource) int {
	switch source {
	case LanguageSourceTrustedHeader:
		return 60
	case LanguageSourceQuery:
		return 50
	case LanguageSourceCookie:
		return 40
	case LanguageSourceUser:
		return 30
	case LanguageSourceAccept:
		return 20
	case LanguageSourceDefault:
		return 10
	default:
		return 0
	}
}
