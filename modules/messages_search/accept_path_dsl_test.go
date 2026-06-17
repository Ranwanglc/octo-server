package messages_search

import (
	"encoding/json"
	"strings"
	"testing"
)

// The existing empty-keyword DSL tests prove the *rejection* path (multi_match
// omitted, blank sender_ids dropped). These tests prove the *acceptance* path:
// when an empty-keyword request carries a real filter, the DSL actually emits
// the corresponding OpenSearch clause. Without these, a regression that dropped
// the filter clause would still pass the existing suite (no multi_match, guard
// happy) while silently turning every listing into a full-channel scan.

// emptyKeywordDSLBody builds the _search DSL for an empty-keyword request and
// returns its JSON body for clause assertions.
func emptyKeywordDSLBody(t *testing.T, req SearchMessagesReq, normChannelID, spaceID string) string {
	t.Helper()
	if req.Keyword != "" {
		t.Fatalf("test bug: req.Keyword must be empty to exercise the listing path")
	}
	q := buildSearchMessagesDSL(req, normChannelID, spaceID)
	js, err := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(js)
	if strings.Contains(body, "multi_match") {
		t.Fatalf("empty-keyword DSL must not include multi_match:\n%s", body)
	}
	return body
}

// TestEmptyKeyword_RealSenderIDsEmitsTermsClause — empty keyword + a real
// sender id must produce a `terms` clause on `from`, proving the listing path
// reaches OpenSearch with a bounding filter (not a full scan).
func TestEmptyKeyword_RealSenderIDsEmitsTermsClause(t *testing.T) {
	req := SearchMessagesReq{
		ChannelType: channelTypeGroup,
		ChannelID:   "G1",
		Filters:     SearchFilters{SenderIDs: []string{"", "u-real"}}, // blank dropped, u-real kept
	}
	body := emptyKeywordDSLBody(t, req, "G1", "")
	if !strings.Contains(body, `"terms"`) || !strings.Contains(body, `"from"`) {
		t.Fatalf("empty-keyword + sender_ids must emit a terms clause on `from`:\n%s", body)
	}
	if !strings.Contains(body, `"u-real"`) {
		t.Fatalf("terms clause must carry the real sender id:\n%s", body)
	}
	if strings.Contains(body, `""`) && strings.Contains(body, `"from":["",`) {
		t.Fatalf("blank sender id must be dropped, not emitted as a term:\n%s", body)
	}
}

// TestEmptyKeyword_SentAtEmitsRangeClause — empty keyword + a time window must
// produce a `range` clause on `timestamp` with the parsed bounds.
func TestEmptyKeyword_SentAtEmitsRangeClause(t *testing.T) {
	req := SearchMessagesReq{
		ChannelType: channelTypeGroup,
		ChannelID:   "G1",
		Filters:     SearchFilters{SentAtFrom: "2026-01-01", SentAtTo: "2026-12-31"},
	}
	body := emptyKeywordDSLBody(t, req, "G1", "")
	if !strings.Contains(body, `"range"`) || !strings.Contains(body, `"timestamp"`) {
		t.Fatalf("empty-keyword + sent_at must emit a range clause on `timestamp`:\n%s", body)
	}
	// olivere/elastic serialises Gte/Lte as from/to + include_lower/include_upper.
	if !strings.Contains(body, `"from":1767196800`) || !strings.Contains(body, `"to":1798732799`) {
		t.Fatalf("range clause must carry both lower (from) and upper (to) bounds:\n%s", body)
	}
}

// TestEmptyKeyword_P2PSpaceIDEmitsTermFilter — empty keyword + p2p with a
// resolved spaceId must still emit the spaceId term filter (the cross-Space
// isolation clause), proving the listing path stays fail-closed per Space.
func TestEmptyKeyword_P2PSpaceIDEmitsTermFilter(t *testing.T) {
	req := SearchMessagesReq{
		ChannelType: channelTypePerson,
		ChannelID:   "peer",
		// p2p empty-keyword listing is gated by the guard on having a filter;
		// here spaceId itself plus a sender id keep it a legitimate request.
		Filters: SearchFilters{SenderIDs: []string{"u-real"}},
	}
	body := emptyKeywordDSLBody(t, req, "fake-cid", "spaceX")
	if !strings.Contains(body, `"spaceId":"spaceX"`) {
		t.Fatalf("empty-keyword p2p DSL must keep the spaceId term filter:\n%s", body)
	}
}

// TestEmptyKeyword_AllFiltersEmitAllClauses — the combined listing request
// (sender + time window + p2p space) must emit every clause at once, the full
// shape the web channel-search UI sends when filtering without a keyword.
func TestEmptyKeyword_AllFiltersEmitAllClauses(t *testing.T) {
	req := SearchMessagesReq{
		ChannelType: channelTypePerson,
		ChannelID:   "peer",
		Filters: SearchFilters{
			SenderIDs:  []string{"u-real"},
			SentAtFrom: "2026-01-01",
			SentAtTo:   "2026-12-31",
		},
	}
	body := emptyKeywordDSLBody(t, req, "fake-cid", "spaceX")
	for _, want := range []string{
		`"terms"`, `"from"`, `"u-real"`,
		`"range"`, `"timestamp"`, `"from":1767196800`, `"to":1798732799`,
		`"spaceId":"spaceX"`,
		`"channelId":"fake-cid"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("combined empty-keyword listing DSL missing %q in:\n%s", want, body)
		}
	}
}

// TestBuildSearchAllDSL_EmptyKeywordRealSenderEmitsTerms — same acceptance
// proof for _search_all (the second keyword-optional endpoint), so both
// listing endpoints have a live-path assertion, not just _search.
func TestBuildSearchAllDSL_EmptyKeywordRealSenderEmitsTerms(t *testing.T) {
	req := SearchAllReq{
		ChannelType: channelTypeGroup,
		ChannelID:   "G1",
		Filters:     SearchFilters{SenderIDs: []string{"u-real"}},
	}
	q := buildSearchAllDSL(req, "G1", "")
	js, _ := json.Marshal(extractDSL(t, q.(interface {
		Source() (any, error)
	})))
	body := string(js)
	if strings.Contains(body, "multi_match") {
		t.Fatalf("empty-keyword _search_all must not include multi_match:\n%s", body)
	}
	if !strings.Contains(body, `"terms"`) || !strings.Contains(body, `"u-real"`) {
		t.Fatalf("empty-keyword _search_all + sender_ids must emit a terms clause:\n%s", body)
	}
}
