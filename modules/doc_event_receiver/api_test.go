package doc_event_receiver

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/common"
	"github.com/Mininglamp-OSS/octo-lib/config"
	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/doc_binding"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"github.com/gin-gonic/gin"
)

// memLookup: slug 到 binding 的 in-mem 桩；err 非 nil 时优先抛错。
type memLookup struct {
	m   map[string]*doc_binding.Binding
	err error
}

func (l *memLookup) LookupSlug(slug string) (*doc_binding.Binding, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.m[slug], nil
}

// recSender: 录一次投递的 channel_id/type/from/payload；err 非 nil 模拟 IM 挂掉。
type recSender struct {
	err  error
	last *config.MsgSendReq
	n    int
}

func (s *recSender) SendMessage(req *config.MsgSendReq) error {
	s.n++
	s.last = req
	return s.err
}

const testToken = "secret-token-for-tests"

// newHarness 建带 gin engine 的 Receiver；token 传空测「未配 token 503」。
func newHarness(t *testing.T, token string, lookup bindingLookup, sender sender) (*Receiver, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rcv := &Receiver{
		token:   token,
		lookup:  lookup,
		sender:  sender,
		fromUID: "doc_bot",
		Log:     log.NewTLog("doc_event_receiver_test"),
	}
	engine := gin.New()
	engine.POST(routePath, func(c *gin.Context) {
		rcv.handle(&wkhttp.Context{Context: c})
	})
	return rcv, engine
}

func postJSON(t *testing.T, engine *gin.Engine, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	switch v := body.(type) {
	case string:
		buf.WriteString(v)
	default:
		if err := json.NewEncoder(&buf).Encode(v); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, routePath, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(tokenHeader, token)
	}
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestHandle_MissingEnvToken_Returns503(t *testing.T) {
	_, engine := newHarness(t, "", &memLookup{}, &recSender{})
	rec := postJSON(t, engine, testToken, map[string]any{"event_type": "comment.created", "slug": "any"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandle_WrongToken_Returns401(t *testing.T) {
	sender := &recSender{}
	_, engine := newHarness(t, testToken, &memLookup{}, sender)
	rec := postJSON(t, engine, "wrong", map[string]any{"event_type": "comment.created", "slug": "any"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if sender.n != 0 {
		t.Fatalf("bad token should never invoke sender, got n=%d", sender.n)
	}
}

func TestHandle_MissingTokenHeader_Returns401(t *testing.T) {
	_, engine := newHarness(t, testToken, &memLookup{}, &recSender{})
	rec := postJSON(t, engine, "", map[string]any{"event_type": "comment.created", "slug": "any"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestHandle_BadJSON_Returns400(t *testing.T) {
	_, engine := newHarness(t, testToken, &memLookup{}, &recSender{})
	rec := postJSON(t, engine, testToken, "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestHandle_MissingSlug_Returns400(t *testing.T) {
	_, engine := newHarness(t, testToken, &memLookup{}, &recSender{})
	rec := postJSON(t, engine, testToken, map[string]any{"event_type": "comment.created"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestHandle_UnknownEventType_Returns202AndIgnores(t *testing.T) {
	sender := &recSender{}
	_, engine := newHarness(t, testToken, &memLookup{m: map[string]*doc_binding.Binding{
		"s1": {Slug: "s1", MountType: doc_binding.MountTypeGroup, GroupNo: "g1"},
	}}, sender)
	rec := postJSON(t, engine, testToken, map[string]any{"event_type": "comment.deleted", "slug": "s1"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d", rec.Code)
	}
	if sender.n != 0 {
		t.Fatalf("unknown event_type should not invoke sender, got n=%d", sender.n)
	}
}

func TestHandle_UnknownSlug_Returns202AndIgnores(t *testing.T) {
	sender := &recSender{}
	_, engine := newHarness(t, testToken, &memLookup{m: map[string]*doc_binding.Binding{}}, sender)
	rec := postJSON(t, engine, testToken, map[string]any{"event_type": "comment.created", "slug": "gone"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d", rec.Code)
	}
	if sender.n != 0 {
		t.Fatalf("unknown slug should not invoke sender, got n=%d", sender.n)
	}
}

func TestHandle_LookupError_Returns500(t *testing.T) {
	_, engine := newHarness(t, testToken, &memLookup{err: errors.New("db down")}, &recSender{})
	rec := postJSON(t, engine, testToken, map[string]any{"event_type": "comment.created", "slug": "s1"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
}

func TestHandle_GroupBinding_SendsToGroupChannel(t *testing.T) {
	sender := &recSender{}
	_, engine := newHarness(t, testToken, &memLookup{m: map[string]*doc_binding.Binding{
		"s1": {Slug: "s1", MountType: doc_binding.MountTypeGroup, GroupNo: "GROUP-NO-1"},
	}}, sender)
	body := map[string]any{
		"event_type": "comment.created",
		"slug":       "s1",
		"actor":      map[string]any{"uid": "u1", "name": "张三"},
		"doc":        map[string]any{"title": "季度复盘", "url": "https://docs.example.com/d/s1#c-42"},
		"comment":    map[string]any{"id": "c-42", "text": "第 3 节数据要重跑"},
	}
	rec := postJSON(t, engine, testToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if sender.n != 1 {
		t.Fatalf("want 1 send, got %d", sender.n)
	}
	if sender.last.ChannelID != "GROUP-NO-1" {
		t.Fatalf("channel_id: want GROUP-NO-1, got %q", sender.last.ChannelID)
	}
	if sender.last.ChannelType != common.ChannelTypeGroup.Uint8() {
		t.Fatalf("channel_type: want %d, got %d", common.ChannelTypeGroup.Uint8(), sender.last.ChannelType)
	}
	if sender.last.FromUID == "" {
		t.Fatalf("from_uid should not be empty in default harness")
	}
	if !bytes.Contains(sender.last.Payload, []byte(`"content"`)) {
		t.Fatalf("payload missing content: %s", string(sender.last.Payload))
	}
	if !bytes.Contains(sender.last.Payload, []byte("张三")) {
		t.Fatalf("payload missing actor name: %s", string(sender.last.Payload))
	}
}

func TestHandle_ThreadBinding_SendsToThreadChannel(t *testing.T) {
	sender := &recSender{}
	groupNo := strings.Repeat("a", 32)
	shortID := "1234567890123456789"
	_, engine := newHarness(t, testToken, &memLookup{m: map[string]*doc_binding.Binding{
		"s2": {Slug: "s2", MountType: doc_binding.MountTypeThread, GroupNo: groupNo, ThreadId: shortID},
	}}, sender)
	body := map[string]any{
		"event_type": "comment.created",
		"slug":       "s2",
		"actor":      map[string]any{"uid": "u1", "name": "李四"},
		"doc":        map[string]any{"title": "子区文档", "url": "https://docs.example.com/d/s2"},
		"comment":    map[string]any{"id": "c-1", "text": "hi"},
	}
	rec := postJSON(t, engine, testToken, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	wantChannelID := thread.BuildChannelID(groupNo, shortID)
	if sender.last.ChannelID != wantChannelID {
		t.Fatalf("channel_id: want %q, got %q", wantChannelID, sender.last.ChannelID)
	}
	if sender.last.ChannelType != common.ChannelTypeCommunityTopic.Uint8() {
		t.Fatalf("channel_type: want %d (CommunityTopic), got %d",
			common.ChannelTypeCommunityTopic.Uint8(), sender.last.ChannelType)
	}
}

func TestHandle_SpaceBinding_Returns202AndSkipsSend(t *testing.T) {
	sender := &recSender{}
	_, engine := newHarness(t, testToken, &memLookup{m: map[string]*doc_binding.Binding{
		"s3": {Slug: "s3", MountType: doc_binding.MountTypeSpace, SpaceId: "sp-1"},
	}}, sender)
	rec := postJSON(t, engine, testToken, map[string]any{
		"event_type": "comment.created", "slug": "s3",
		"actor":   map[string]any{"name": "王五"},
		"doc":     map[string]any{"title": "space doc"},
		"comment": map[string]any{"text": "x"},
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202 for space mount, got %d", rec.Code)
	}
	if sender.n != 0 {
		t.Fatalf("space mount should not send, got n=%d", sender.n)
	}
}

func TestHandle_SendError_Returns502(t *testing.T) {
	sender := &recSender{err: errors.New("IM 502")}
	_, engine := newHarness(t, testToken, &memLookup{m: map[string]*doc_binding.Binding{
		"s1": {Slug: "s1", MountType: doc_binding.MountTypeGroup, GroupNo: "g"},
	}}, sender)
	rec := postJSON(t, engine, testToken, map[string]any{
		"event_type": "comment.created", "slug": "s1",
		"actor":   map[string]any{"name": "n"},
		"doc":     map[string]any{"title": "t"},
		"comment": map[string]any{"text": "c"},
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502 on downstream send error, got %d", rec.Code)
	}
}

func TestHandle_LargeBody_Returns413(t *testing.T) {
	_, engine := newHarness(t, testToken, &memLookup{}, &recSender{})
	huge := strings.Repeat("x", maxBodyBytes+100)
	body := `{"event_type":"comment.created","slug":"s1","comment":{"text":"` + huge + `"}}`
	rec := postJSON(t, engine, testToken, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

func TestBuildCommentPayload_EscapesXSS(t *testing.T) {
	req := &docEventReq{}
	req.EventType = "comment.created"
	req.Slug = "s1"
	req.Actor.Name = `<script>alert(1)</script>`
	req.Doc.Title = `T & "quote"`
	req.Doc.URL = `https://ex.com/"drop`
	req.Comment.Text = `<b>bold</b>`
	p := buildCommentPayload(req)
	content, _ := p["content"].(string)
	for _, banned := range []string{"<script>", "</script>", "<b>"} {
		if strings.Contains(content, banned) {
			t.Fatalf("content should not contain raw %q: %s", banned, content)
		}
	}
	if !strings.Contains(content, "&lt;script&gt;") {
		t.Fatalf("content should have escaped actor name: %s", content)
	}
	if !strings.Contains(content, "&amp;") {
		t.Fatalf("content should have escaped ampersand in title: %s", content)
	}
}

func TestBuildCommentPayload_TruncatesLongText(t *testing.T) {
	req := &docEventReq{}
	req.Actor.Name = "n"
	req.Doc.Title = "t"
	req.Comment.Text = strings.Repeat("你", commentPreviewRunes+50)
	p := buildCommentPayload(req)
	content, _ := p["content"].(string)
	if !strings.HasSuffix(content, "…") {
		t.Fatalf("long text should be truncated with …, got tail: %q", tail(content, 4))
	}
	if strings.Count(content, "你") > commentPreviewRunes {
		t.Fatalf("truncated content has %d 你 chars, want <= %d",
			strings.Count(content, "你"), commentPreviewRunes)
	}
}

func tail(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[len(rs)-n:])
}

func TestResolveChannel(t *testing.T) {
	cases := []struct {
		name    string
		b       *doc_binding.Binding
		wantID  string
		wantTyp uint8
		wantOK  bool
	}{
		{"group", &doc_binding.Binding{MountType: doc_binding.MountTypeGroup, GroupNo: "g1"},
			"g1", common.ChannelTypeGroup.Uint8(), true},
		{"thread", &doc_binding.Binding{MountType: doc_binding.MountTypeThread, GroupNo: "g1", ThreadId: "t1"},
			thread.BuildChannelID("g1", "t1"), common.ChannelTypeCommunityTopic.Uint8(), true},
		{"space", &doc_binding.Binding{MountType: doc_binding.MountTypeSpace, SpaceId: "sp"},
			"", 0, false},
		{"unknown", &doc_binding.Binding{MountType: "wat"}, "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, typ, ok := resolveChannel(tc.b)
			if id != tc.wantID || typ != tc.wantTyp || ok != tc.wantOK {
				t.Fatalf("got (%q,%d,%v) want (%q,%d,%v)", id, typ, ok, tc.wantID, tc.wantTyp, tc.wantOK)
			}
		})
	}
}
