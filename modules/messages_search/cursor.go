package messages_search

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// cursorPayload is the search-after key serialised inside an opaque cursor.
// Score is set only for `relevance` sort, where OS sorts by 3 keys
// (timestamp, _score, messageId) and search_after must echo all three.
// `omitempty` keeps time_desc / time_asc cursors byte-identical to the
// pre-relevance-fix encoding, so already-issued cursors decode unchanged.
type cursorPayload struct {
	TS    int64    `json:"ts"`          // OS `timestamp` field, epoch seconds
	MsgID int64    `json:"id"`          // OS `messageId` tiebreaker
	Score *float64 `json:"s,omitempty"` // _score, relevance sort only
}

// cursorSigLen is the HMAC tail length appended after the JSON body. 8 bytes
// of SHA-256 is plenty for a non-monetary tamper check while keeping the
// cursor short on the wire.
const cursorSigLen = 8

// hmacKeyFn returns the keyed HMAC secret. Indirected so tests can swap a
// deterministic value via SetHMACKeyForTest.
var hmacKeyFn = func(cfg SearchConfig) []byte {
	if cfg.CursorHMAC == "" {
		return []byte("octo-messages-search-default-cursor-key")
	}
	return []byte(cfg.CursorHMAC)
}

// encodeCursor packs (timestamp, messageId, score?) into a base64url-encoded
// opaque cursor with an 8-byte HMAC tail. Pass score=nil for time_desc /
// time_asc; pass a non-nil pointer for relevance sort.
func encodeCursor(cfg SearchConfig, ts, msgID int64, score *float64) string {
	p := cursorPayload{TS: ts, MsgID: msgID, Score: score}
	body, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, hmacKeyFn(cfg))
	mac.Write(body)
	sig := mac.Sum(nil)[:cursorSigLen]
	return base64.RawURLEncoding.EncodeToString(append(body, sig...))
}

// decodeCursor reverses encodeCursor, validating the HMAC. Any structural or
// signature failure surfaces as a single "malformed cursor" error so the
// handler can map to VALIDATION_ERROR(field=cursor). The returned score is
// nil for legacy 2-tuple cursors (time_*) and non-nil for relevance cursors.
func decodeCursor(cfg SearchConfig, s string) (int64, int64, *float64, error) {
	if s == "" {
		return 0, 0, nil, errors.New("cursor: empty")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < cursorSigLen+1 {
		return 0, 0, nil, errors.New("cursor: malformed")
	}
	body, sig := raw[:len(raw)-cursorSigLen], raw[len(raw)-cursorSigLen:]
	mac := hmac.New(sha256.New, hmacKeyFn(cfg))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil)[:cursorSigLen], sig) {
		return 0, 0, nil, errors.New("cursor: bad signature")
	}
	var p cursorPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return 0, 0, nil, errors.New("cursor: unmarshal")
	}
	return p.TS, p.MsgID, p.Score, nil
}
