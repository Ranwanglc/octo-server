package messages_search

import (
	"encoding/json"
	"strconv"
)

// Doc mirrors the OpenSearch `_source` shape produced by
// wukongim-message-indexer (see indexer-os-changes.md §3.2). We only
// deserialise structured `payload.*` subobjects — `payloadRaw` is
// `enabled:false` in the mapping and would force per-doc JSON parsing on the
// hot path with no upside.
type Doc struct {
	MessageID   int64    `json:"messageId"`
	MessageSeq  uint64   `json:"messageSeq"`
	From        string   `json:"from,omitempty"`
	To          string   `json:"to,omitempty"`
	ChannelID   string   `json:"channelId"`
	ChannelType uint32   `json:"channelType"`
	Timestamp   int64    `json:"timestamp"`
	Payload     *Payload `json:"payload,omitempty"`
	Revoked     bool     `json:"revoked,omitempty"`
	// SpaceID mirrors the OS doc's `spaceId` keyword introduced in v1.9 to
	// scope DM (p2p) search by Space membership. The indexer derives this
	// from `payload.space_id`; older documents without the field are
	// fail-closed by the term filter in applySpaceIDScope (no match → no
	// hit) rather than implicitly visible.
	SpaceID string `json:"spaceId,omitempty"`
	// ParentMessageID and Virtual mark rich-text-derived sub-documents that
	// the indexer emits per embedded image/file inside a payload.type=14
	// rich-text message (Part B virtual-docs contract, see
	// docs/messages-search/richtext-virtual-docs-octo-server-dev.md §1).
	//
	// Both fields are reader-internal — they never reach the JSON response.
	// `Virtual=true` drives the `must_not(virtual=true)` filter on the four
	// text-search builders so derivative children don't masquerade as
	// independent messages on _search / _search_all / _search_around.
	// `ParentMessageID` is the visibility key used by filterVisible: revoke /
	// delete / channel-offset / visibles state is owned by the parent rich-text
	// row in MySQL and the child has no row of its own.
	//
	// *int64 distinguishes "field absent" (legacy / non-virtual docs) from a
	// zero parent id. Per indexer contract `Virtual=true` ⇒ `ParentMessageID`
	// non-nil and equal to the parent's messageId. Plain docs (Virtual=false)
	// leave ParentMessageID nil and keep the existing behaviour.
	ParentMessageID *int64 `json:"parentMessageId,omitempty"`
	Virtual         bool   `json:"virtual,omitempty"`
	// SubSeq is the sort-tiebreaker for virtual sub-documents derived from
	// rich-text parents (Part B). Per indexer contract:
	//   - plain message docs and rich-text parent docs (Virtual=false): SubSeq=0
	//   - virtual sub-documents (Virtual=true):                          SubSeq>=1
	// Together with (timestamp, messageId) this guarantees a globally unique
	// sort tuple so OpenSearch search_after never silently skips siblings
	// that share (timestamp, messageId) with their parent. Storage docs that
	// pre-date the field deserialize to 0, which matches the plain/parent
	// convention — safe for the read/deserialize path before the indexer field
	// exists. NOTE: sorting on subSeq is NOT safe by itself — applySort must
	// pass UnmappedType+Missing(0) so a reader-first deploy doesn't 400 on the
	// missing mapping (see dsl.go::applySort).
	SubSeq int `json:"subSeq,omitempty"`
	// Visibles is the per-message allowlist a sender may attach to a group
	// message so only the listed UIDs see it (mirrors the read-path gate
	// in modules/message/api.go::MsgSyncResp.from at the visibles-array
	// branch). When non-empty and the caller's UID is absent the search
	// post-filter must drop the hit. Schema is reserved here ahead of the
	// indexer write — see CONSTRAINTS-2026-06-12 for the transient
	// fail-open while the field is unwritten.
	Visibles []string `json:"visibles,omitempty"`
	// PayloadRaw carries the indexer-preserved original message payload blob
	// (wukongim-message-indexer writes the full source payload under
	// `_source.payloadRaw` with mapping `enabled:false`, see indexer
	// transform/doc.go). It is only consumed by the typed rich-text (type=14)
	// projection in buildRichTextDetail — the legacy structured `payload.*`
	// fields above stay authoritative for everything else. Absent on docs
	// indexed before the indexer started writing the field; downstream
	// projectors must fail-soft (nil RichText, snippet fallback) in that case.
	PayloadRaw json.RawMessage `json:"payloadRaw,omitempty"`
}

// Payload is the structured projection of the message payload. Each typed
// subobject is allocated only when the indexer recognised its content type, so
// a non-nil pointer is the strongest "this message is of type X" signal.
type Payload struct {
	Type         *int                 `json:"type,omitempty"`
	Text         *TextPayload         `json:"text,omitempty"`
	Image        *ImagePayload        `json:"image,omitempty"`
	Gif          *GifPayload          `json:"gif,omitempty"`
	Voice        *VoicePayload        `json:"voice,omitempty"`
	Video        *VideoPayload        `json:"video,omitempty"`
	File         *FilePayload         `json:"file,omitempty"`
	MergeForward *MergeForwardPayload `json:"mergeForward,omitempty"`
	RichText     *RichTextPayload     `json:"richText,omitempty"`
}

type TextPayload struct {
	Content string `json:"content,omitempty"`
}

type ImagePayload struct {
	URL     string `json:"url,omitempty"`
	Caption string `json:"caption,omitempty"`
	Name    string `json:"name,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
}

type GifPayload struct {
	URL string `json:"url,omitempty"`
}

type VoicePayload struct {
	URL string `json:"url,omitempty"`
}

type VideoPayload struct {
	URL    string `json:"url,omitempty"`
	Cover  string `json:"cover,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Second int    `json:"second,omitempty"`
}

type FilePayload struct {
	URL       string `json:"url,omitempty"`
	Name      string `json:"name,omitempty"`
	Caption   string `json:"caption,omitempty"`
	SizeBytes int64  `json:"size,omitempty"`
	Ext       string `json:"extension,omitempty"`
}

type MergeForwardPayload struct {
	ChildCount int               `json:"childCount,omitempty"`
	Msgs       []MergeForwardMsg `json:"msgs,omitempty"`
}

// RichTextPayload mirrors the indexer's richText projection. Only `searchText`
// is materialised here — the full block tree (text/image/file blocks) lives in
// payloadRaw on the OS doc and is not read by the search path. searchText is
// the indexer's plain-text join of all rich-text blocks plus embedded
// image/file name+caption, written under analyzer ik_max_word. See Part A doc.
type RichTextPayload struct {
	SearchText string `json:"searchText,omitempty"`
}

// MergeForwardMsg is the per-child projection from `payload.mergeForward.msgs[]`.
// `from` and `timestamp` are forward-compat fields the indexer will start
// writing in a follow-up release; both are omitempty so older OS docs (which
// only carry messageId/type/searchText) deserialise to a zero value and the
// API can degrade `sender_id` / `sent_at` to omitted on the wire.
type MergeForwardMsg struct {
	MessageID  int64  `json:"messageId"`
	Type       int    `json:"type"`
	SearchText string `json:"searchText,omitempty"`
	From       string `json:"from,omitempty"`
	Timestamp  int64  `json:"timestamp,omitempty"`
}

// Payload type IDs (mirroring dmwork-lib `common/msg.go::ContentType`). Kept
// as untyped int constants because OS stores them as `int` and our DSL/filter
// layer needs the raw value.
const (
	payloadTypeText         = 1
	payloadTypeImage        = 2
	payloadTypeGIF          = 3
	payloadTypeVoice        = 4
	payloadTypeVideo        = 5
	payloadTypeFile         = 8
	payloadTypeMergeForward = 11
	payloadTypeRichText     = 14
	payloadTypeCmd          = 99

	// System message range per indexer spec
	// (~/Projects/_refs/wukongim-message-indexer/docs/specs/2026-06-04-v1.6-decisions.md §2.2):
	// payload.type 1000-2000 covers FriendApply / Group* / Hotline* / Tip etc.
	// These are control-plane events emitted by WuKongIM and MUST be hard-filtered
	// from /_search_messages (the legacy `_search` surface) per the indexer's
	// "搜索硬过滤" contract.
	payloadTypeSystemMin = 1000
	payloadTypeSystemMax = 2000
)

// classifyKind decides the response `message_kind` for /v1/messages/_search
// and /v1/messages/_search_all (browse mode surfaces image/video here too).
// Priority: mergeForward beats raw payload.type so a forward card whose
// payload also happens to mention an image still renders as a forward.
// Image (2) / video (5) surface as their own kinds so the client can switch
// to the media renderer instead of trying to render an empty text snippet —
// this also reaches /_search_around, which is the intended behaviour (the
// around window already returns image/file docs and previously stamped them
// as "text"). Everything else folds into "text".
func classifyKind(p *Payload) string {
	if p == nil {
		return "text"
	}
	if p.MergeForward != nil {
		return "forward"
	}
	switch payloadType(p) {
	case payloadTypeImage:
		return "image"
	case payloadTypeVideo:
		return "video"
	}
	return "text"
}

// OuterPreview is the optional summary card returned for forward messages.
type OuterPreview struct {
	ChildCount int `json:"child_count"`
}

// buildOuterPreview emits a non-nil preview only when the doc is a forward
// card with a positive child_count. Plain text and quote messages return nil;
// forwards with a missing or non-positive childCount also return nil so we
// don't surface the misleading `{child_count: 0}` to the client.
func buildOuterPreview(p *Payload) *OuterPreview {
	if p == nil || p.MergeForward == nil {
		return nil
	}
	if p.MergeForward.ChildCount <= 0 {
		return nil
	}
	return &OuterPreview{ChildCount: p.MergeForward.ChildCount}
}

// payloadType returns the typed payload.type or 0 when missing. Used by the
// _search_all dispatcher to pick a result_type without dereferencing a *int
// at every call site.
func payloadType(p *Payload) int {
	if p == nil || p.Type == nil {
		return 0
	}
	return *p.Type
}

// RichTextBlock mirrors a single block in a payload.type=14 rich-text
// message's `content[]`. The JSON tags are aligned with octo-web's
// RichTextContent.ts / octo-lib common/richtext.go so the search projection
// returns the same shape the existing channel/sync renderer already consumes.
// Image/file-specific fields (extension/mime/caption) follow the octo-web
// schema which extends octo-lib's MVP shape; unknown / unused fields stay
// omitempty so unrelated block types serialise to the minimal `{type,text}`
// or `{type,url,...}` form.
type RichTextBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	URL       string `json:"url,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Name      string `json:"name,omitempty"`
	Extension string `json:"extension,omitempty"`
	Mime      string `json:"mime,omitempty"`
	Caption   string `json:"caption,omitempty"`
}

// RichTextMentionEntity is one @-mention anchor inside a text block, used by
// the renderer to highlight the matching `[offset, offset+length)` rune range
// of the surrounding text.
type RichTextMentionEntity struct {
	UID    string `json:"uid"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// RichTextMention is the payload.mention object: per-user entities plus the
// three @-all tri-state flags (all / humans / ais). Whole object is optional
// and missing when the message has no @ mentions.
type RichTextMention struct {
	Entities []RichTextMentionEntity `json:"entities,omitempty"`
	All      int                     `json:"all,omitempty"`
	Humans   int                     `json:"humans,omitempty"`
	Ais      int                     `json:"ais,omitempty"`
}

// RichTextDetail is the typed projection of a rich-text payload exposed under
// MessageHit.rich_text. Shape matches the channel/sync payload contract so the
// client can render search hits with the same components as the timeline.
type RichTextDetail struct {
	Content []RichTextBlock  `json:"content"`
	Plain   string           `json:"plain,omitempty"`
	Mention *RichTextMention `json:"mention,omitempty"`
}

// buildRichTextDetail extracts the rich-text projection from the indexer's
// preserved `_source.payloadRaw` blob. Caller must guard with
// payloadType(...) == payloadTypeRichText — this function does not re-check.
//
// Fail-soft contract: returns nil for the empty / unparseable / pre-payloadRaw
// cases so the caller silently falls back to the existing snippet path:
//
//   - raw == nil / len(raw)==0  → nil  (legacy doc indexed before the field)
//   - json.Unmarshal fails      → nil  (corrupt or unexpected envelope)
//
// Backwards compatibility: the rich-text payload's `content` was historically a
// plain JSON string. New payloads emit an array of RichTextBlock. We branch on
// the first non-space byte ('[' = array, anything else = string) and normalise
// the legacy string form into a single `{type:"text", text:<s>}` block. Empty
// strings collapse to a nil Content slice so the renderer doesn't get a
// `{type:"text", text:""}` ghost block.
func buildRichTextDetail(raw json.RawMessage) *RichTextDetail {
	if len(raw) == 0 {
		return nil
	}
	var p struct {
		Content json.RawMessage  `json:"content"`
		Plain   string           `json:"plain"`
		Mention *RichTextMention `json:"mention"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	d := &RichTextDetail{Plain: p.Plain, Mention: p.Mention}
	if len(p.Content) > 0 {
		if p.Content[0] == '[' {
			_ = json.Unmarshal(p.Content, &d.Content)
		} else {
			var s string
			if json.Unmarshal(p.Content, &s) == nil && s != "" {
				d.Content = []RichTextBlock{{Type: "text", Text: s}}
			}
		}
	}
	return d
}

// InnerMessage is the per-child shape surfaced under MessageHit.inner_messages
// for forward (type=11) hits. SenderName is filled in after senderJoin runs;
// SenderID / SentAt are omitted when the indexer hasn't yet populated the
// underlying msgs[].from / msgs[].timestamp fields.
type InnerMessage struct {
	MessageID  string `json:"message_id"`
	Type       int    `json:"type"`
	SearchText string `json:"search_text,omitempty"`
	SenderID   string `json:"sender_id,omitempty"`
	SenderName string `json:"sender_name,omitempty"`
	SentAt     string `json:"sent_at,omitempty"`
}

// buildInnerMessages projects the forward card's child messages onto the API
// shape. Returns nil for non-forward payloads or empty msgs[] so the response
// field is omitted entirely (omitempty) rather than emitting `[]`.
//
// `sender_name` is left empty here — the caller batches all child uids into
// the page's senderJoin and fills the names afterwards.
func buildInnerMessages(p *Payload) []InnerMessage {
	if p == nil || p.MergeForward == nil {
		return nil
	}
	if len(p.MergeForward.Msgs) == 0 {
		return nil
	}
	out := make([]InnerMessage, 0, len(p.MergeForward.Msgs))
	for _, m := range p.MergeForward.Msgs {
		im := InnerMessage{
			MessageID:  strconv.FormatInt(m.MessageID, 10),
			Type:       m.Type,
			SearchText: m.SearchText,
			SenderID:   m.From,
		}
		if m.Timestamp > 0 {
			im.SentAt = msToRFC3339(m.Timestamp)
		}
		out = append(out, im)
	}
	return out
}
