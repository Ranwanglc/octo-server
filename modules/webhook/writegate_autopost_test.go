package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteGateAutopost_DisabledDoesNotSend(t *testing.T) {
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	t.Setenv(envWriteGateAutopostEnabled, "false")
	t.Setenv(envWriteGateAutopostURL, server.URL)

	scheduled := (&Webhook{}).autopostWriteGateMessages([]MsgResp{writeGateTextMsg()})
	assert.False(t, scheduled)

	select {
	case <-received:
		t.Fatal("write-gate autopost sent while disabled")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWriteGateAutopost_EmptyURLDoesNotSend(t *testing.T) {
	t.Setenv(envWriteGateAutopostEnabled, "true")
	t.Setenv(envWriteGateAutopostURL, "")

	scheduled := (&Webhook{}).autopostWriteGateMessages([]MsgResp{writeGateTextMsg()})
	assert.False(t, scheduled)
}

func TestWriteGateAutopost_TextMessagePostsBody(t *testing.T) {
	received := make(chan writeGateAutopostBody, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body writeGateAutopostBody
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		received <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	t.Setenv(envWriteGateAutopostEnabled, "true")
	t.Setenv(envWriteGateAutopostURL, server.URL)
	t.Setenv(envWriteGateAutopostTimeoutMS, "1000")

	scheduled := (&Webhook{}).autopostWriteGateMessages([]MsgResp{
		writeGateTextMsg(),
		{
			MessageID:   889,
			FromUID:     "sender",
			ChannelID:   "group-1",
			ChannelType: common.ChannelTypeGroup.Uint8(),
			Timestamp:   1720000001,
			Payload:     []byte(`{"type":2,"content":"image should skip"}`),
		},
		{
			Header:      messageHeader{NoPersist: 1},
			MessageID:   890,
			FromUID:     "sender",
			ChannelID:   "group-1",
			ChannelType: common.ChannelTypeGroup.Uint8(),
			Timestamp:   1720000002,
			Payload:     []byte(`{"type":1,"content":"no persist should skip"}`),
		},
	})
	assert.True(t, scheduled)

	select {
	case body := <-received:
		assert.Equal(t, "hello write-gate", body.Content)
		assert.Equal(t, "sender", body.SourceAgent)
		assert.Equal(t, "group-1", body.ChannelID)
		assert.Equal(t, common.ChannelTypeGroup.Uint8(), body.ChannelType)
		assert.Equal(t, int64(888), body.MessageID)
		assert.Equal(t, int32(1720000000), body.Timestamp)
		assert.Equal(t, common.Text.Int(), body.ContentType)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write-gate autopost")
	}

	select {
	case extra := <-received:
		t.Fatalf("unexpected extra write-gate autopost: %+v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

func writeGateTextMsg() MsgResp {
	return MsgResp{
		MessageID:   888,
		FromUID:     "sender",
		ToUID:       "receiver",
		ChannelID:   "group-1",
		ChannelType: common.ChannelTypeGroup.Uint8(),
		Timestamp:   1720000000,
		Payload:     []byte(`{"type":1,"content":"hello write-gate"}`),
	}
}
