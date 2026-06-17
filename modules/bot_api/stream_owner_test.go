// Package bot_api · OCT-41: tests for the stream_no→robotID ownership binding
// that guards /v1/bot/stream/end against co-member bot griefing.
//
// The handler ownership check runs against a streamOwnerStore. These tests
// inject an in-memory store (memStreamOwnerStore) via streamOwnerStoreOverride
// so the gate is exercised without a live Redis. The same in-memory store is
// reused by the start/end happy-path tests in stream_test.go to keep ba.ctx
// (and thus Redis) out of those pure-Go handler tests.
package bot_api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// memStreamOwnerStore is an in-memory streamOwnerStore for handler tests.
type memStreamOwnerStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemStreamOwnerStore() *memStreamOwnerStore {
	return &memStreamOwnerStore{m: make(map[string]string)}
}

func (s *memStreamOwnerStore) bind(streamNo, robotID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[streamNo] = robotID
	return nil
}

func (s *memStreamOwnerStore) owner(streamNo string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[streamNo], nil
}

func (s *memStreamOwnerStore) release(streamNo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, streamNo)
	return nil
}

func (s *memStreamOwnerStore) get(streamNo string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[streamNo]
	return v, ok
}

// TestStreamEnd_RejectsForeignOwner is the OCT-41 acceptance test: in a shared
// channel, Bot B must NOT be able to end a stream_no opened by Bot A. Bot A
// owns the stream (recorded at start); Bot B passes the channel gate (it is a
// legitimate member, modelled here via the DM creator-bypass branch) yet must
// be denied by the ownership check, and IMStreamEnd must NOT fire.
func TestStreamEnd_RejectsForeignOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		botA     = "bot_A"
		botB     = "bot_B"
		streamNo = "stream_owned_by_A"
		// Both bots are addressed via a DM whose channel_id == their CreatorUID,
		// so checkSendPermission takes the creator-bypass branch (no DB) and the
		// only thing left to stop Bot B is the ownership gate.
		channelB = "user_creator_B"
	)

	owners := newMemStreamOwnerStore()
	_ = owners.bind(streamNo, botA) // Bot A opened this stream.

	ec := &streamEndCapture{}
	ba := &BotAPI{
		Log:                      log.NewTLog("BotAPI-stream-it"),
		streamEndOverride:        ec.hook,
		streamOwnerStoreOverride: owners,
	}

	body, _ := json.Marshal(config.MessageStreamEndReq{
		StreamNo:    streamNo,
		ChannelID:   channelB,
		ChannelType: common.ChannelTypePerson.Uint8(),
	})

	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/end", body, botB, channelB, BotKindUser)
	ba.streamEnd(c)

	// Denials go through the ResponseErrorL facade, which pins the wire status
	// to 400 (D14 compat) and carries the real 403 in error.http_status — the
	// existing permission tests assert NotEqual(200) for the same reason. We
	// also assert the distinctive ownership message so this isn't confused with
	// a channel-gate denial.
	assert.NotEqualf(t, http.StatusOK, rec.Code,
		"Bot B ending Bot A's stream must be denied, got %d body=%s", rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "stream you opened",
		"denial must be the stream-ownership guard, not some other gate")
	ec.mu.Lock()
	assert.Nil(t, ec.captured, "IMStreamEnd must NOT fire when the caller is not the stream owner")
	ec.mu.Unlock()

	// The binding must survive a rejected end so the true owner can still close it.
	got, ok := owners.get(streamNo)
	assert.True(t, ok, "binding must not be released on a denied end")
	assert.Equal(t, botA, got)
}

// TestStreamEnd_OwnerCanEnd: the bot that opened the stream ends it
// successfully, IMStreamEnd fires, and the binding is released afterward.
func TestStreamEnd_OwnerCanEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		botA       = "bot_A"
		creatorUID = "user_creator"
		streamNo   = "stream_owned_by_A"
	)

	owners := newMemStreamOwnerStore()
	_ = owners.bind(streamNo, botA)

	ec := &streamEndCapture{}
	ba := &BotAPI{
		Log:                      log.NewTLog("BotAPI-stream-it"),
		streamEndOverride:        ec.hook,
		streamOwnerStoreOverride: owners,
	}

	body, _ := json.Marshal(config.MessageStreamEndReq{
		StreamNo:    streamNo,
		ChannelID:   creatorUID,
		ChannelType: common.ChannelTypePerson.Uint8(),
	})

	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/end", body, botA, creatorUID, BotKindUser)
	ba.streamEnd(c)

	assert.Equalf(t, http.StatusOK, rec.Code,
		"owner ending its own stream should be 200, got %d body=%s", rec.Code, rec.Body.String())
	ec.mu.Lock()
	if assert.NotNil(t, ec.captured, "IMStreamEnd MUST fire for the stream owner") {
		assert.Equal(t, streamNo, ec.captured.StreamNo)
	}
	ec.mu.Unlock()

	_, ok := owners.get(streamNo)
	assert.False(t, ok, "binding should be released after a successful end")
}

// TestStreamEnd_NoBindingFallsThrough: an absent binding (stale TTL, or a
// stream opened outside this path) must NOT block the end — the terminal-END
// guarantee outweighs the bounded griefing risk, which only exists while a
// binding is present. IMStreamEnd must still fire.
func TestStreamEnd_NoBindingFallsThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		botX       = "bot_X"
		creatorUID = "user_creator"
		streamNo   = "stream_with_no_binding"
	)

	owners := newMemStreamOwnerStore() // empty: no binding for streamNo

	ec := &streamEndCapture{}
	ba := &BotAPI{
		Log:                      log.NewTLog("BotAPI-stream-it"),
		streamEndOverride:        ec.hook,
		streamOwnerStoreOverride: owners,
	}

	body, _ := json.Marshal(config.MessageStreamEndReq{
		StreamNo:    streamNo,
		ChannelID:   creatorUID,
		ChannelType: common.ChannelTypePerson.Uint8(),
	})

	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/end", body, botX, creatorUID, BotKindUser)
	ba.streamEnd(c)

	assert.Equalf(t, http.StatusOK, rec.Code,
		"absent binding must fall through to allow, got %d body=%s", rec.Code, rec.Body.String())
	ec.mu.Lock()
	assert.NotNil(t, ec.captured, "IMStreamEnd MUST fire when no binding exists (terminal-END guarantee)")
	ec.mu.Unlock()
}

// TestStreamStart_BindsOwner: a successful stream/start records stream_no →
// robotID so a later stream/end can verify ownership.
func TestStreamStart_BindsOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		botRobotID = "bot_X"
		creatorUID = "user_creator"
		wantStream = "stream_99"
	)

	owners := newMemStreamOwnerStore()
	sc := &streamStartCapture{streamNo: wantStream}
	ba := &BotAPI{
		Log:                      log.NewTLog("BotAPI-stream-it"),
		streamStartOverride:      sc.hook,
		streamOwnerStoreOverride: owners,
	}

	body, _ := json.Marshal(config.MessageStreamStartReq{
		ChannelID:   creatorUID,
		ChannelType: common.ChannelTypePerson.Uint8(),
	})

	rec := httptest.NewRecorder()
	c := newStreamTestContext(rec, "/v1/bot/stream/start", body, botRobotID, creatorUID, BotKindUser)
	ba.streamStart(c)

	assert.Equalf(t, http.StatusOK, rec.Code,
		"streamStart should respond 200, got %d body=%s", rec.Code, rec.Body.String())
	got, ok := owners.get(wantStream)
	assert.True(t, ok, "stream/start must record the owner binding")
	assert.Equal(t, botRobotID, got, "binding must map stream_no → authenticated robotID")
}
