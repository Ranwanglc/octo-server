package i18n

import (
	"errors"
	"testing"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
)

func TestParams_TemplateDataCopiesAndRenders(t *testing.T) {
	params := Params{"Name": "octo", "Count": 3, "PluralCount": 3}

	data, err := params.TemplateData()
	if err != nil {
		t.Fatalf("TemplateData err = %v", err)
	}
	data["Name"] = "mutated"

	if got := params["Name"]; got != "octo" {
		t.Fatalf("TemplateData returned alias; Params[Name] = %v", got)
	}

	got, err := params.Render("{{.Name}} has {{.Count}} items")
	if err != nil {
		t.Fatalf("Render err = %v", err)
	}
	if want := "octo has 3 items"; got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestParams_NilTemplateData(t *testing.T) {
	var params Params
	data, err := params.TemplateData()
	if err != nil {
		t.Fatalf("nil TemplateData err = %v", err)
	}
	if data != nil {
		t.Fatalf("nil TemplateData = %#v, want nil", data)
	}
}

func TestParams_BlocksSensitiveKeys(t *testing.T) {
	tests := []string{
		"uid",
		"userUID",
		"access_token",
		"SQLQuery",
		"secret_key",
		"internal_id",
		"password",
		"raw_err",
	}

	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			_, err := Params{key: "value"}.TemplateData()
			if !errors.Is(err, ErrSensitiveParamKey) {
				t.Fatalf("TemplateData err = %v, want ErrSensitiveParamKey", err)
			}
		})
	}
}

func TestLocalizer_InterpolatesParams(t *testing.T) {
	const id = "err.shared.template.params"
	if _, ok := codes.Lookup(id); !ok {
		codes.Register(codes.Code{
			ID:             id,
			HTTPStatus:     400,
			DefaultMessage: "Field {{.Field}} must be at least {{.Min}} characters.",
		})
	}

	resetBundle()
	t.Cleanup(resetBundle)

	got := NewLocalizer("en-US").Translate(id, "en-US", Params{
		"Field": "name",
		"Min":   3,
	})
	want := "Field name must be at least 3 characters."
	if got != want {
		t.Fatalf("Translate with params = %q, want %q", got, want)
	}
}
