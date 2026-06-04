package i18n

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestUnaryClientLanguageInterceptorInjectsMetadata(t *testing.T) {
	ctx := WithLanguage(context.Background(), LanguageDecision{
		Language: "zh-CN",
		Source:   LanguageSourceCookie,
	})

	err := UnaryClientLanguageInterceptor()(ctx, "/test.Service/Method", nil, nil, nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				t.Fatal("outgoing metadata missing")
			}
			if got := md.Get(MetadataOctoLang); len(got) != 1 || got[0] != "zh-CN" {
				t.Fatalf("%s metadata = %v, want [zh-CN]", MetadataOctoLang, got)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
}

func TestUnaryClientLanguageInterceptorOverwritesExistingMetadata(t *testing.T) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(MetadataOctoLang, "en-US"))
	ctx = WithLanguage(ctx, LanguageDecision{
		Language: "zh-CN",
		Source:   LanguageSourceCookie,
	})

	err := UnaryClientLanguageInterceptor()(ctx, "/test.Service/Method", nil, nil, nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				t.Fatal("outgoing metadata missing")
			}
			if got := md.Get(MetadataOctoLang); len(got) != 1 || got[0] != "zh-CN" {
				t.Fatalf("%s metadata = %v, want [zh-CN]", MetadataOctoLang, got)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
}

func TestUnaryServerLanguageInterceptorWritesLanguageContext(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(MetadataOctoLang, "zh"))

	resp, err := UnaryServerLanguageInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
		func(ctx context.Context, req interface{}) (interface{}, error) {
			decision, ok := LanguageFromContext(ctx)
			if !ok {
				t.Fatal("language decision missing from context")
			}
			if decision.Language != "zh-CN" {
				t.Fatalf("Language = %q, want zh-CN", decision.Language)
			}
			if decision.Source != LanguageSourceGRPCMetadata {
				t.Fatalf("Source = %q, want %q", decision.Source, LanguageSourceGRPCMetadata)
			}
			return "ok", nil
		})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("resp = %v, want ok", resp)
	}
}

func TestUnaryServerLanguageInterceptorIgnoresUnsupportedMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(MetadataOctoLang, "not-a-language"))

	_, err := UnaryServerLanguageInterceptor()(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
		func(ctx context.Context, req interface{}) (interface{}, error) {
			if decision, ok := LanguageFromContext(ctx); ok {
				t.Fatalf("LanguageFromContext = %#v, want none", decision)
			}
			return nil, nil
		})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
}
