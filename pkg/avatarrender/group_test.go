package avatarrender

import (
	"bytes"
	"image/png"
	"reflect"
	"testing"
)

func TestGroupAvatarLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"four cjk 2x2", "架构讨论", []string{"架构", "讨论"}},
		{"four cjk 2x2 b", "后端架构", []string{"后端", "架构"}},
		{"three cjk 1+2", "三个字", []string{"三", "个字"}},
		{"three cjk 1+2 b", "云服务", []string{"云", "服务"}},
		{"two cjk single", "开发", []string{"开发"}},
		{"one cjk single", "发", []string{"发"}},
		{"four latin single", "abcd", []string{"abcd"}},
		{"two latin single", "ab", []string{"ab"}},
		{"mixed wide wraps", "A产品B", []string{"A产", "品B"}},
		{"two mixed single", "a产", []string{"a产"}},
		{"empty", "", []string{""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GroupAvatarLines(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("GroupAvatarLines(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderGroupProducesValidPNG(t *testing.T) {
	for _, text := range []string{"后端架构", "三个字", "开发", "abcd", "发"} {
		data, err := RenderGroup(Options{Text: text, Bg: ColorForSeed("g_" + text), Size: 200})
		if err != nil {
			t.Fatalf("RenderGroup(%q): %v", text, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("decode png for %q: %v", text, err)
		}
		if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 200 {
			t.Fatalf("size for %q = %dx%d, want 200x200", text, b.Dx(), b.Dy())
		}
	}
}

func TestRenderGroupEmptyTextErrors(t *testing.T) {
	if _, err := RenderGroup(Options{Text: "", Bg: ColorForSeed("g1")}); err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestRenderGroupDeterministic(t *testing.T) {
	a, err := RenderGroup(Options{Text: "架构讨论", Bg: ColorForSeed("g9"), Size: 200})
	if err != nil {
		t.Fatal(err)
	}
	b, err := RenderGroup(Options{Text: "架构讨论", Bg: ColorForSeed("g9"), Size: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("RenderGroup not deterministic for identical input")
	}
}
