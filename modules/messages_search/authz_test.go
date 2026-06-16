package messages_search

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-lib/pkg/log"
	"github.com/Mininglamp-OSS/octo-lib/pkg/wkhttp"
	"github.com/Mininglamp-OSS/octo-server/modules/group"
	"github.com/Mininglamp-OSS/octo-server/modules/thread"
	"github.com/Mininglamp-OSS/octo-server/modules/user"
	"github.com/gin-gonic/gin"
)

// stubAuthzGroupSvc fakes group.IService for the membership gate. The embedded
// interface is nil so any method other than ExistMemberActive /
// GetGroupWithGroupNo panics on call — proving checkChannelAccess only
// touches the methods the new contract requires.
type stubAuthzGroupSvc struct {
	group.IService
	activeMembers map[string]bool            // groupNo → caller is active member
	groupModels   map[string]*group.InfoResp // groupNo → snapshot (nil = not found)
	groupErr      error                      // GetGroupWithGroupNo error
	memberErr     error                      // ExistMemberActive error
	getGroupCalls int
	memberCalls   int
	gotGroupNo    string
	gotUID        string
}

func (s *stubAuthzGroupSvc) GetGroupWithGroupNo(groupNo string) (*group.InfoResp, error) {
	s.getGroupCalls++
	if s.groupErr != nil {
		return nil, s.groupErr
	}
	return s.groupModels[groupNo], nil
}

func (s *stubAuthzGroupSvc) ExistMemberActive(groupNo string, uid string) (bool, error) {
	s.memberCalls++
	s.gotGroupNo = groupNo
	s.gotUID = uid
	if s.memberErr != nil {
		return false, s.memberErr
	}
	return s.activeMembers[groupNo], nil
}

// stubAuthzUserSvc fakes user.IService for the p2p friend / blacklist gate.
type stubAuthzUserSvc struct {
	user.IService
	friends      map[string]bool // "uid|peer" → friend?
	friendErr    error
	blacklists   map[string]bool // "from|to" → blocked?
	blacklistErr error
	friendCalls  int
	blCalls      int
}

func friendKey(a, b string) string    { return a + "|" + b }
func blacklistKey(a, b string) string { return a + "|" + b }

func (s *stubAuthzUserSvc) IsFriend(uid, toUID string) (bool, error) {
	s.friendCalls++
	if s.friendErr != nil {
		return false, s.friendErr
	}
	return s.friends[friendKey(uid, toUID)], nil
}

func (s *stubAuthzUserSvc) ExistBlacklist(uid, toUID string) (bool, error) {
	s.blCalls++
	if s.blacklistErr != nil {
		return false, s.blacklistErr
	}
	return s.blacklists[blacklistKey(uid, toUID)], nil
}

// stubAuthzThreadSvc fakes thread.IService for the thread gate. Only
// GetThread is implemented; everything else panics so the test asserts the
// gate doesn't drift onto adjacent thread methods.
type stubAuthzThreadSvc struct {
	thread.IService
	threadOK    map[string]bool // "groupNo|shortID" → exists?
	threadErr   error
	threadCalls int
}

func (s *stubAuthzThreadSvc) GetThread(groupNo, shortID, loginUID string) (*thread.ThreadResp, error) {
	s.threadCalls++
	if s.threadErr != nil {
		return nil, s.threadErr
	}
	if s.threadOK[groupNo+"|"+shortID] {
		return &thread.ThreadResp{}, nil
	}
	return nil, errors.New("thread not found")
}

func newAuthzCtx(t *testing.T) (*wkhttp.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	gc, _ := gin.CreateTestContext(rec)
	gc.Request = httptest.NewRequest("POST", "/v1/messages/_search", nil)
	return &wkhttp.Context{Context: gc}, rec
}

func newAuthzHandlerFull(gSvc group.IService, uSvc user.IService, tSvc thread.IService) *Handler {
	return &Handler{
		Log:           log.NewTLog("messages_search-authz-test"),
		cfg:           SearchConfig{},
		groupService:  gSvc,
		userService:   uSvc,
		threadService: tSvc,
	}
}

func newAuthzHandler(gSvc group.IService) *Handler {
	return newAuthzHandlerFull(gSvc, &stubAuthzUserSvc{}, &stubAuthzThreadSvc{})
}

// ---------- p2p ----------

// TestCheckChannelAccess_P2PFriendAllowed — friends with no blacklist either
// way pass the gate. Replaces the legacy "p2p always allowed" test (PR #361
// reviewer flag: search bypassed friend + blacklist).
func TestCheckChannelAccess_P2PFriendAllowed(t *testing.T) {
	uSvc := &stubAuthzUserSvc{
		friends:    map[string]bool{friendKey("me", "peer"): true},
		blacklists: map[string]bool{},
	}
	h := newAuthzHandlerFull(&stubAuthzGroupSvc{}, uSvc, &stubAuthzThreadSvc{})
	c, rec := newAuthzCtx(t)

	if !h.checkChannelAccess(c, channelTypePerson, "peer", "me") {
		t.Fatalf("friend with no blacklist must pass the p2p gate")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("no response should be written on allow, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_P2PSelfAllowed — searching one's own DM (notes-to-self)
// must not consult friend/blacklist.
func TestCheckChannelAccess_P2PSelfAllowed(t *testing.T) {
	uSvc := &stubAuthzUserSvc{}
	h := newAuthzHandlerFull(&stubAuthzGroupSvc{}, uSvc, &stubAuthzThreadSvc{})
	c, _ := newAuthzCtx(t)

	if !h.checkChannelAccess(c, channelTypePerson, "me", "me") {
		t.Fatalf("self DM must always pass the p2p gate")
	}
	if uSvc.friendCalls != 0 || uSvc.blCalls != 0 {
		t.Fatalf("self DM must not call IsFriend/ExistBlacklist; got friend=%d bl=%d", uSvc.friendCalls, uSvc.blCalls)
	}
}

// TestCheckChannelAccess_P2PNotFriendDenied — non-friends are denied as
// NOT_FOUND (anti-enumeration).
func TestCheckChannelAccess_P2PNotFriendDenied(t *testing.T) {
	uSvc := &stubAuthzUserSvc{friends: map[string]bool{}}
	h := newAuthzHandlerFull(&stubAuthzGroupSvc{}, uSvc, &stubAuthzThreadSvc{})
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypePerson, "peer", "me") {
		t.Fatalf("non-friend must be denied")
	}
	if uSvc.blCalls != 0 {
		t.Fatalf("blacklist must not be queried after non-friend rejection; got %d", uSvc.blCalls)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("denial should render the not_found envelope, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_P2PBlockedByMeDenied — caller blocking peer must
// hide history.
func TestCheckChannelAccess_P2PBlockedByMeDenied(t *testing.T) {
	uSvc := &stubAuthzUserSvc{
		friends:    map[string]bool{friendKey("me", "peer"): true},
		blacklists: map[string]bool{blacklistKey("me", "peer"): true},
	}
	h := newAuthzHandlerFull(&stubAuthzGroupSvc{}, uSvc, &stubAuthzThreadSvc{})
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypePerson, "peer", "me") {
		t.Fatalf("caller-side blacklist must deny")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write a response")
	}
}

// TestCheckChannelAccess_P2PBlockedByPeerDenied — peer blocking caller must
// hide history (anti-harassment).
func TestCheckChannelAccess_P2PBlockedByPeerDenied(t *testing.T) {
	uSvc := &stubAuthzUserSvc{
		friends:    map[string]bool{friendKey("me", "peer"): true},
		blacklists: map[string]bool{blacklistKey("peer", "me"): true},
	}
	h := newAuthzHandlerFull(&stubAuthzGroupSvc{}, uSvc, &stubAuthzThreadSvc{})
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypePerson, "peer", "me") {
		t.Fatalf("peer-side blacklist must deny")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write a response")
	}
}

// TestCheckChannelAccess_P2PIsFriendErrorFailsClosed — DB lookup errors must
// fail closed with INTERNAL_ERROR (not silently allow).
func TestCheckChannelAccess_P2PIsFriendErrorFailsClosed(t *testing.T) {
	uSvc := &stubAuthzUserSvc{friendErr: errors.New("db down")}
	h := newAuthzHandlerFull(&stubAuthzGroupSvc{}, uSvc, &stubAuthzThreadSvc{})
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypePerson, "peer", "me") {
		t.Fatalf("IsFriend error must fail closed")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("fail-closed denial must write a response")
	}
}

// TestCheckChannelAccess_P2PBlacklistErrorFailsClosed — blacklist lookup
// error must fail closed.
func TestCheckChannelAccess_P2PBlacklistErrorFailsClosed(t *testing.T) {
	uSvc := &stubAuthzUserSvc{
		friends:      map[string]bool{friendKey("me", "peer"): true},
		blacklistErr: errors.New("db down"),
	}
	h := newAuthzHandlerFull(&stubAuthzGroupSvc{}, uSvc, &stubAuthzThreadSvc{})
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypePerson, "peer", "me") {
		t.Fatalf("ExistBlacklist error must fail closed")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("fail-closed denial must write a response")
	}
}

// ---------- group ----------

// TestCheckChannelAccess_GroupMemberAllowed — active members of a normal-status
// group pass.
func TestCheckChannelAccess_GroupMemberAllowed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{
		activeMembers: map[string]bool{"G1": true},
		groupModels: map[string]*group.InfoResp{
			"G1": {GroupNo: "G1", Status: 1},
		},
	}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if !h.checkChannelAccess(c, channelTypeGroup, "G1", "me") {
		t.Fatalf("active member must pass the gate")
	}
	if gSvc.gotGroupNo != "G1" || gSvc.gotUID != "me" {
		t.Fatalf("membership checked with wrong identity: group=%q uid=%q", gSvc.gotGroupNo, gSvc.gotUID)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("no response should be written on allow, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_GroupNonMemberDenied is the regression guard for the
// PR #361 blocking finding: any logged-in user could search ANY group's full
// message history by sending an arbitrary group_no.
func TestCheckChannelAccess_GroupNonMemberDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{
		activeMembers: map[string]bool{},
		groupModels: map[string]*group.InfoResp{
			"victim-group": {GroupNo: "victim-group", Status: 1},
		},
	}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeGroup, "victim-group", "attacker") {
		t.Fatalf("non-member must be denied")
	}
	if gSvc.memberCalls != 1 {
		t.Fatalf("ExistMemberActive should be consulted exactly once, got %d", gSvc.memberCalls)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("denial should render the not_found envelope, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_GroupDisbandedDeniedEvenIfMember — disband must
// short-circuit BEFORE membership, so leftover member rows can't leak
// access on a disbanded group.
func TestCheckChannelAccess_GroupDisbandedDeniedEvenIfMember(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{
		activeMembers: map[string]bool{"G1": true}, // bookkeeping says member
		groupModels: map[string]*group.InfoResp{
			"G1": {GroupNo: "G1", Status: group.GroupStatusDisband},
		},
	}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeGroup, "G1", "me") {
		t.Fatalf("disbanded group must be denied even with stale active membership")
	}
	if gSvc.memberCalls != 0 {
		t.Fatalf("disband must short-circuit before ExistMemberActive; got %d member calls", gSvc.memberCalls)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write a response")
	}
}

// TestCheckChannelAccess_GroupNilModelDenied — a nil group model is treated
// as "does not exist" (not an internal error), to match anti-enumeration.
func TestCheckChannelAccess_GroupNilModelDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{
		activeMembers: map[string]bool{},
		groupModels:   map[string]*group.InfoResp{}, // nil for any groupNo
	}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeGroup, "G1", "me") {
		t.Fatalf("missing group model must be denied")
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("denial should render the not_found envelope, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_GroupGetGroupErrorFailsClosed — GetGroupWithGroupNo
// error must fail closed.
func TestCheckChannelAccess_GroupGetGroupErrorFailsClosed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{groupErr: errors.New("db down")}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeGroup, "G1", "me") {
		t.Fatalf("GetGroupWithGroupNo error must fail closed")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("fail-closed denial must write a response")
	}
}

// TestCheckChannelAccess_GroupExistMemberErrorFailsClosed — membership lookup
// error must deny (legacy regression: covers the original
// LookupErrorFailsClosed case from the pre-disband code path).
func TestCheckChannelAccess_GroupExistMemberErrorFailsClosed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{
		groupModels: map[string]*group.InfoResp{
			"G1": {GroupNo: "G1", Status: 1},
		},
		memberErr: errors.New("db down"),
	}
	h := newAuthzHandler(gSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeGroup, "G1", "me") {
		t.Fatalf("ExistMemberActive error must fail closed")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("fail-closed denial must write a response")
	}
}

// ---------- thread ----------

// TestCheckChannelAccess_ThreadMemberAllowed — parent-group active member
// with an existing (non-deleted) thread passes.
func TestCheckChannelAccess_ThreadMemberAllowed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{"G9": true}}
	tSvc := &stubAuthzThreadSvc{threadOK: map[string]bool{"G9|123456789012345": true}}
	h := newAuthzHandlerFull(gSvc, &stubAuthzUserSvc{}, tSvc)
	c, _ := newAuthzCtx(t)

	if !h.checkChannelAccess(c, channelTypeThread, "G9____123456789012345", "me") {
		t.Fatalf("parent-group member of an existing thread must pass the gate")
	}
	if gSvc.gotGroupNo != "G9" {
		t.Fatalf("thread gate must check the parent group, got %q", gSvc.gotGroupNo)
	}
	if tSvc.threadCalls != 1 {
		t.Fatalf("thread gate must consult GetThread exactly once, got %d", tSvc.threadCalls)
	}
}

// TestCheckChannelAccess_ThreadNonMemberDenied — non-members of the parent
// group are denied.
func TestCheckChannelAccess_ThreadNonMemberDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{}}
	tSvc := &stubAuthzThreadSvc{threadOK: map[string]bool{"G9|123456789012345": true}}
	h := newAuthzHandlerFull(gSvc, &stubAuthzUserSvc{}, tSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeThread, "G9____123456789012345", "attacker") {
		t.Fatalf("non-member of parent group must be denied thread search")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write a response")
	}
}

// TestCheckChannelAccess_ThreadMalformedIDDenied — malformed channel_id is
// rejected before any service lookup.
func TestCheckChannelAccess_ThreadMalformedIDDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{"": true}}
	tSvc := &stubAuthzThreadSvc{}
	h := newAuthzHandlerFull(gSvc, &stubAuthzUserSvc{}, tSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeThread, "____orphan", "me") {
		t.Fatalf("malformed thread channel_id must be denied")
	}
	if gSvc.memberCalls != 0 || tSvc.threadCalls != 0 {
		t.Fatalf("malformed id must short-circuit; group=%d thread=%d", gSvc.memberCalls, tSvc.threadCalls)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("denial must write a response")
	}
}

// TestCheckChannelAccess_ThreadDeletedOrMissingDenied — when the thread is
// missing or deleted, GetThread returns err and the gate denies before
// consulting the group membership (anti-enumeration).
func TestCheckChannelAccess_ThreadDeletedOrMissingDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{"G9": true}}
	tSvc := &stubAuthzThreadSvc{threadOK: map[string]bool{}} // GetThread always errors → not found
	h := newAuthzHandlerFull(gSvc, &stubAuthzUserSvc{}, tSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeThread, "G9____123456789012345", "me") {
		t.Fatalf("missing/deleted thread must be denied")
	}
	if gSvc.memberCalls != 0 {
		t.Fatalf("ExistMemberActive must not be reached after GetThread err; got %d", gSvc.memberCalls)
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("denial should render the not_found envelope, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_ThreadGetThreadDBErrorDenied — DB error inside
// GetThread collapses with not-found into NOT_FOUND (anti-enumeration over
// operational signal).
func TestCheckChannelAccess_ThreadGetThreadDBErrorDenied(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{activeMembers: map[string]bool{"G9": true}}
	tSvc := &stubAuthzThreadSvc{threadErr: errors.New("db down")}
	h := newAuthzHandlerFull(gSvc, &stubAuthzUserSvc{}, tSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeThread, "G9____123456789012345", "me") {
		t.Fatalf("GetThread DB error must deny")
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("denial should render the not_found envelope, got %q", rec.Body.String())
	}
}

// TestCheckChannelAccess_ThreadParentMemberErrorFailsClosed — a DB error on
// ExistMemberActive (after GetThread succeeded) maps to INTERNAL_ERROR,
// not NOT_FOUND, because the existence question has already been answered.
func TestCheckChannelAccess_ThreadParentMemberErrorFailsClosed(t *testing.T) {
	gSvc := &stubAuthzGroupSvc{
		memberErr: errors.New("db down"),
	}
	tSvc := &stubAuthzThreadSvc{threadOK: map[string]bool{"G9|123456789012345": true}}
	h := newAuthzHandlerFull(gSvc, &stubAuthzUserSvc{}, tSvc)
	c, rec := newAuthzCtx(t)

	if h.checkChannelAccess(c, channelTypeThread, "G9____123456789012345", "me") {
		t.Fatalf("parent-group ExistMemberActive error must fail closed")
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("fail-closed denial must write a response")
	}
}
