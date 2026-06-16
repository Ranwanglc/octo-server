package messages_search

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
)

// TestSource_FullDoc covers the v4.2 mapping shape. Keep this fixture in sync
// with indexer-os-changes.md §3.2 — when the indexer adds a new structured
// field, extend this fixture before touching the handlers so we catch
// unmarshal regressions early.
const fullDocFixture = `{
  "messageId": 1234567890123,
  "messageSeq": 42,
  "from": "userA",
  "to": "userB",
  "channelId": "chan@channelB",
  "channelType": 1,
  "timestamp": 1717000000,
  "revoked": false,
  "payload": {
    "type": 11,
    "mergeForward": {
      "childCount": 5,
      "msgs": [
        {"messageId": 1, "type": 1, "searchText": "hello"},
        {"messageId": 2, "type": 8, "searchText": "doc.pdf"}
      ]
    }
  }
}`

func TestUnmarshalDoc_MergeForward(t *testing.T) {
	var doc Doc
	if err := json.Unmarshal([]byte(fullDocFixture), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.MessageID != 1234567890123 {
		t.Fatalf("messageId: got %d", doc.MessageID)
	}
	if doc.MessageSeq != 42 {
		t.Fatalf("messageSeq: got %d", doc.MessageSeq)
	}
	if doc.Payload == nil || doc.Payload.MergeForward == nil {
		t.Fatalf("expected mergeForward")
	}
	if doc.Payload.MergeForward.ChildCount != 5 {
		t.Fatalf("childCount: got %d", doc.Payload.MergeForward.ChildCount)
	}
	if len(doc.Payload.MergeForward.Msgs) != 2 {
		t.Fatalf("msgs len: got %d", len(doc.Payload.MergeForward.Msgs))
	}
}

func TestUnmarshalDoc_Image(t *testing.T) {
	src := `{"messageId":1,"messageSeq":1,"channelId":"g","timestamp":100,"payload":{"type":2,"image":{"url":"http://x","width":640,"height":480,"caption":"hi"}}}`
	var doc Doc
	if err := json.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Payload.Image == nil || doc.Payload.Image.URL != "http://x" {
		t.Fatalf("image: %+v", doc.Payload.Image)
	}
	if doc.Payload.Image.Width != 640 || doc.Payload.Image.Height != 480 {
		t.Fatalf("dims: %+v", doc.Payload.Image)
	}
}

func TestUnmarshalDoc_Video(t *testing.T) {
	src := `{"messageId":1,"messageSeq":1,"channelId":"g","timestamp":100,"payload":{"type":5,"video":{"url":"http://x","cover":"c","second":12,"width":640,"height":480}}}`
	var doc Doc
	if err := json.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Payload.Video.Second != 12 {
		t.Fatalf("second: got %d", doc.Payload.Video.Second)
	}
}

func TestUnmarshalDoc_File(t *testing.T) {
	src := `{"messageId":1,"messageSeq":1,"channelId":"g","timestamp":100,"payload":{"type":8,"file":{"url":"http://x","name":"a.pdf","size":12345,"extension":"pdf"}}}`
	var doc Doc
	if err := json.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Payload.File.SizeBytes != 12345 || doc.Payload.File.Ext != "pdf" {
		t.Fatalf("file: %+v", doc.Payload.File)
	}
}

func TestClassifyKind_Forward(t *testing.T) {
	p := &Payload{MergeForward: &MergeForwardPayload{ChildCount: 3}}
	if got := classifyKind(p); got != "forward" {
		t.Fatalf("forward: got %q", got)
	}
}

func TestClassifyKind_Text(t *testing.T) {
	if got := classifyKind(nil); got != "text" {
		t.Fatalf("nil: got %q", got)
	}
	tp := 1
	p := &Payload{Type: &tp, Text: &TextPayload{Content: "hi"}}
	if got := classifyKind(p); got != "text" {
		t.Fatalf("text: got %q", got)
	}
	// Quote / reply (any non-mergeForward case) folds into "text".
	tp = 2
	p = &Payload{Type: &tp, Image: &ImagePayload{}}
	if got := classifyKind(p); got != "text" {
		t.Fatalf("image folds to text: got %q", got)
	}
}

func TestBuildOuterPreview_ForwardOnly(t *testing.T) {
	p := &Payload{MergeForward: &MergeForwardPayload{ChildCount: 7}}
	prev := buildOuterPreview(p)
	if prev == nil || prev.ChildCount != 7 {
		t.Fatalf("forward preview: %+v", prev)
	}
	if prev := buildOuterPreview(nil); prev != nil {
		t.Fatalf("nil payload: want nil preview")
	}
	if prev := buildOuterPreview(&Payload{}); prev != nil {
		t.Fatalf("text payload: want nil preview")
	}
	// P2-6 guard: a forward whose ChildCount is missing or non-positive
	// must NOT yield {child_count: 0} on the wire — the frontend reads
	// that as "0 messages" which is misleading.
	if prev := buildOuterPreview(&Payload{MergeForward: &MergeForwardPayload{}}); prev != nil {
		t.Fatalf("zero childCount: want nil preview, got %+v", prev)
	}
	if prev := buildOuterPreview(&Payload{MergeForward: &MergeForwardPayload{ChildCount: -1}}); prev != nil {
		t.Fatalf("negative childCount: want nil preview, got %+v", prev)
	}
}

func TestPickSnippet(t *testing.T) {
	hl := map[string][]string{
		"payload.text.content": {"hello <mark>world</mark>"},
		"payload.file.name":    {"<mark>doc</mark>.pdf"},
	}
	if got := pickSnippet(hl); !strings.Contains(got, "world") {
		t.Fatalf("priority: text content should win, got %q", got)
	}
	if got := pickSnippet(map[string][]string{"payload.file.name": {"x"}}); got != "x" {
		t.Fatalf("file fallback: got %q", got)
	}
	if got := pickSnippet(nil); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

// TestPayloadTypeConstants_AlignWithOctoLib pins the local payloadType*
// constants to octo-lib/common.ContentType so a renumber upstream surfaces
// here as a test failure rather than a silent mis-routing of message types.
func TestPayloadTypeConstants_AlignWithOctoLib(t *testing.T) {
	cases := []struct {
		name  string
		local int
		lib   common.ContentType
	}{
		{"text", payloadTypeText, common.Text},
		{"image", payloadTypeImage, common.Image},
		{"gif", payloadTypeGIF, common.GIF},
		{"voice", payloadTypeVoice, common.Voice},
		{"video", payloadTypeVideo, common.Video},
		{"file", payloadTypeFile, common.File},
		{"merge_forward", payloadTypeMergeForward, common.MultipleForward},
		{"cmd", payloadTypeCmd, common.CMD},
	}
	for _, tc := range cases {
		if int(tc.lib) != tc.local {
			t.Fatalf("%s: local=%d != octo-lib %d", tc.name, tc.local, tc.lib)
		}
	}
}
