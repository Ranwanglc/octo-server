package messages_search

import (
	"testing"
)

func TestSingleSearchAllHit_File(t *testing.T) {
	tp := payloadTypeFile
	doc := Doc{
		MessageID:  100,
		MessageSeq: 9,
		From:       "u1",
		Timestamp:  1717000000,
		Payload: &Payload{
			Type: &tp,
			File: &FilePayload{Name: "a.pdf", Ext: "pdf", URL: "http://x"},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, nil)
	if got.ResultType != "file" {
		t.Errorf("result_type: got %q", got.ResultType)
	}
	if got.File == nil || got.File.FileName != "a.pdf" {
		t.Fatalf("file should be populated: %+v", got.File)
	}
	if got.Message != nil {
		t.Errorf("message should be nil for file result: %+v", got.Message)
	}
	if got.SortedAt != got.File.SentAt {
		t.Errorf("sorted_at must mirror inner sent_at: got %q vs %q", got.SortedAt, got.File.SentAt)
	}
}

func TestSingleSearchAllHit_TextMessage(t *testing.T) {
	tp := payloadTypeText
	doc := Doc{
		MessageID:  101,
		MessageSeq: 10,
		From:       "u2",
		Timestamp:  1717000001,
		Payload: &Payload{
			Type: &tp,
			Text: &TextPayload{Content: "hello"},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	hl := map[string][]string{"payload.text.content": {"<mark>hello</mark>"}}
	got := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, hl)
	if got.ResultType != "message" {
		t.Errorf("result_type: got %q", got.ResultType)
	}
	if got.Message == nil || got.Message.Snippet == "" {
		t.Fatalf("message + snippet expected: %+v", got.Message)
	}
	if got.Message.MessageKind != "text" {
		t.Errorf("text kind: got %q", got.Message.MessageKind)
	}
	if got.File != nil {
		t.Errorf("file should be nil for message result")
	}
}

func TestSingleSearchAllHit_ForwardKeepsMessageType(t *testing.T) {
	tp := payloadTypeMergeForward
	doc := Doc{
		MessageID: 102,
		Timestamp: 100,
		Payload: &Payload{
			Type:         &tp,
			MergeForward: &MergeForwardPayload{ChildCount: 4},
		},
	}
	h := &Handler{cfg: SearchConfig{}, cache: newSenderCache(8, 0)}
	got := h.singleSearchAllHit(doc, SearchAllReq{ChannelType: channelTypeGroup, ChannelID: "g"}, nil)
	if got.ResultType != "message" {
		t.Errorf("forward must be 'message' (file is type=8 only): got %q", got.ResultType)
	}
	if got.Message == nil || got.Message.MessageKind != "forward" {
		t.Errorf("forward kind: %+v", got.Message)
	}
	if got.Message.OuterPreview == nil || got.Message.OuterPreview.ChildCount != 4 {
		t.Errorf("outer_preview: %+v", got.Message.OuterPreview)
	}
}
