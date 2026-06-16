package i18n

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	// MetadataOctoLang is the gRPC metadata key used for language propagation.
	MetadataOctoLang = "x-octo-lang"
)

// UnaryClientLanguageInterceptor propagates the negotiated language through gRPC metadata.
func UnaryClientLanguageInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = InjectGRPCMetadata(ctx)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerLanguageInterceptor reads propagated language metadata into context.
func UnaryServerLanguageInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if lang, ok := languageFromIncomingMetadata(ctx); ok {
			ctx = WithLanguage(ctx, LanguageDecision{
				Language: lang,
				Source:   LanguageSourceGRPCMetadata,
			})
		}
		return handler(ctx, req)
	}
}

// InjectGRPCMetadata propagates the negotiated language into outgoing gRPC metadata.
func InjectGRPCMetadata(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	decision, ok := LanguageFromContext(ctx)
	if !ok || decision.Language == "" {
		return ctx
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		md = md.Copy()
	} else {
		md = metadata.MD{}
	}
	md.Set(MetadataOctoLang, decision.Language)
	return metadata.NewOutgoingContext(ctx, md)
}

func languageFromIncomingMetadata(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	for _, raw := range md.Get(MetadataOctoLang) {
		if lang, ok := MatchSupportedLanguage(raw); ok {
			return lang, true
		}
	}
	return "", false
}
