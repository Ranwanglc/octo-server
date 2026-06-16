package messages_search

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/Mininglamp-OSS/octo-server/modules/message"
)

// fakeMessageIService is a hand-rolled message.IService stub used to drive
// the production messageVisibilityProbe in isolation from MySQL. The
// embedded nil interface means any IService method without a *Fn override
// panics on call — proving the probe touches only the four DB methods the
// contract requires.
type fakeMessageIService struct {
	message.IService

	revokedFn        func(ids []string) ([]*message.MessageExtraResp, error)
	deletedFn        func(ids []string) ([]*message.MessageExtraResp, error)
	deletedWithUIDFn func(uid string, ids []string) ([]*message.MessageUserExtraResp, error)
	channelOffsetFn  func(uid string, channelIDs []string) ([]*message.ChannelOffsetResp, error)

	revokedCalls        int
	deletedCalls        int
	deletedWithUIDCalls int
	channelOffsetCalls  int

	lastIDsForRevoked        []string
	lastIDsForDeleted        []string
	lastUIDForDeletedWithUID string
	lastIDsForDeletedWithUID []string
	lastUIDForOffset         string
	lastChannelIDsForOffset  []string
}

func (f *fakeMessageIService) GetRevokedMessages(ids []string) ([]*message.MessageExtraResp, error) {
	f.revokedCalls++
	f.lastIDsForRevoked = append([]string(nil), ids...)
	if f.revokedFn == nil {
		panic("fakeMessageIService.GetRevokedMessages: revokedFn not set (probe should not have called this)")
	}
	return f.revokedFn(ids)
}

func (f *fakeMessageIService) GetDeletedMessages(ids []string) ([]*message.MessageExtraResp, error) {
	f.deletedCalls++
	f.lastIDsForDeleted = append([]string(nil), ids...)
	if f.deletedFn == nil {
		panic("fakeMessageIService.GetDeletedMessages: deletedFn not set (probe should not have called this)")
	}
	return f.deletedFn(ids)
}

func (f *fakeMessageIService) GetDeletedMessagesWithUID(uid string, ids []string) ([]*message.MessageUserExtraResp, error) {
	f.deletedWithUIDCalls++
	f.lastUIDForDeletedWithUID = uid
	f.lastIDsForDeletedWithUID = append([]string(nil), ids...)
	if f.deletedWithUIDFn == nil {
		panic("fakeMessageIService.GetDeletedMessagesWithUID: deletedWithUIDFn not set (probe should not have called this)")
	}
	return f.deletedWithUIDFn(uid, ids)
}

func (f *fakeMessageIService) GetChannelOffsetWithUID(uid string, channelIDs []string) ([]*message.ChannelOffsetResp, error) {
	f.channelOffsetCalls++
	f.lastUIDForOffset = uid
	f.lastChannelIDsForOffset = append([]string(nil), channelIDs...)
	if f.channelOffsetFn == nil {
		panic("fakeMessageIService.GetChannelOffsetWithUID: channelOffsetFn not set (probe should not have called this)")
	}
	return f.channelOffsetFn(uid, channelIDs)
}

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- RevokedSet ---------------------------------------------------------

func TestProbe_RevokedSet_EmptyIDsNoCall(t *testing.T) {
	t.Parallel()
	for _, ids := range [][]string{nil, {}} {
		fake := &fakeMessageIService{}
		probe := newMessageVisibilityProbe(fake)

		got, err := probe.RevokedSet(ids)
		if err != nil {
			t.Fatalf("RevokedSet(%v) err: %v", ids, err)
		}
		if len(got) != 0 {
			t.Fatalf("RevokedSet(%v) = %v, want empty", ids, got)
		}
		if fake.revokedCalls != 0 {
			t.Fatalf("RevokedSet(%v) made %d DB calls, want 0", ids, fake.revokedCalls)
		}
	}
}

func TestProbe_RevokedSet_AllItemsCollected(t *testing.T) {
	t.Parallel()
	fake := &fakeMessageIService{
		revokedFn: func(ids []string) ([]*message.MessageExtraResp, error) {
			return []*message.MessageExtraResp{
				{MessageIDStr: "m1"},
				{MessageIDStr: "m2"},
				{MessageIDStr: "m3"},
			}, nil
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.RevokedSet([]string{"m1", "m2", "m3"})
	if err != nil {
		t.Fatalf("RevokedSet err: %v", err)
	}
	if want := []string{"m1", "m2", "m3"}; !reflect.DeepEqual(sortedSetKeys(got), want) {
		t.Fatalf("RevokedSet keys = %v, want %v", sortedSetKeys(got), want)
	}
	if fake.revokedCalls != 1 {
		t.Fatalf("RevokedSet calls = %d, want 1", fake.revokedCalls)
	}
}

func TestProbe_RevokedSet_DBErrorPropagated(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	fake := &fakeMessageIService{
		revokedFn: func(ids []string) ([]*message.MessageExtraResp, error) {
			return nil, dbErr
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.RevokedSet([]string{"m1"})
	if !errors.Is(err, dbErr) {
		t.Fatalf("RevokedSet err = %v, want %v", err, dbErr)
	}
	if got != nil {
		t.Fatalf("RevokedSet on err = %v, want nil", got)
	}
}

// --- GloballyDeletedSet -------------------------------------------------

func TestProbe_GloballyDeletedSet_EmptyIDsNoCall(t *testing.T) {
	t.Parallel()
	for _, ids := range [][]string{nil, {}} {
		fake := &fakeMessageIService{}
		probe := newMessageVisibilityProbe(fake)

		got, err := probe.GloballyDeletedSet(ids)
		if err != nil {
			t.Fatalf("GloballyDeletedSet(%v) err: %v", ids, err)
		}
		if len(got) != 0 {
			t.Fatalf("GloballyDeletedSet(%v) = %v, want empty", ids, got)
		}
		if fake.deletedCalls != 0 {
			t.Fatalf("GloballyDeletedSet(%v) made %d DB calls, want 0", ids, fake.deletedCalls)
		}
	}
}

func TestProbe_GloballyDeletedSet_FiltersOnIsMutualDeleted(t *testing.T) {
	t.Parallel()
	fake := &fakeMessageIService{
		deletedFn: func(ids []string) ([]*message.MessageExtraResp, error) {
			return []*message.MessageExtraResp{
				{MessageIDStr: "m1", IsMutualDeleted: 1},
				{MessageIDStr: "m2", IsMutualDeleted: 0},
				{MessageIDStr: "m3", IsMutualDeleted: 1},
			}, nil
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.GloballyDeletedSet([]string{"m1", "m2", "m3"})
	if err != nil {
		t.Fatalf("GloballyDeletedSet err: %v", err)
	}
	if want := []string{"m1", "m3"}; !reflect.DeepEqual(sortedSetKeys(got), want) {
		t.Fatalf("GloballyDeletedSet keys = %v, want %v (m2 must be filtered out)", sortedSetKeys(got), want)
	}
}

func TestProbe_GloballyDeletedSet_DBErrorPropagated(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	fake := &fakeMessageIService{
		deletedFn: func(ids []string) ([]*message.MessageExtraResp, error) {
			return nil, dbErr
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.GloballyDeletedSet([]string{"m1"})
	if !errors.Is(err, dbErr) {
		t.Fatalf("GloballyDeletedSet err = %v, want %v", err, dbErr)
	}
	if got != nil {
		t.Fatalf("GloballyDeletedSet on err = %v, want nil", got)
	}
}

// --- UserDeletedSet -----------------------------------------------------

func TestProbe_UserDeletedSet_EmptyIDsNoCall(t *testing.T) {
	t.Parallel()
	for _, ids := range [][]string{nil, {}} {
		fake := &fakeMessageIService{}
		probe := newMessageVisibilityProbe(fake)

		got, err := probe.UserDeletedSet("u1", ids)
		if err != nil {
			t.Fatalf("UserDeletedSet(%v) err: %v", ids, err)
		}
		if len(got) != 0 {
			t.Fatalf("UserDeletedSet(%v) = %v, want empty", ids, got)
		}
		if fake.deletedWithUIDCalls != 0 {
			t.Fatalf("UserDeletedSet(%v) made %d DB calls, want 0", ids, fake.deletedWithUIDCalls)
		}
	}
}

func TestProbe_UserDeletedSet_FiltersOnMessageIsDeleted_PassesUID(t *testing.T) {
	t.Parallel()
	fake := &fakeMessageIService{
		deletedWithUIDFn: func(uid string, ids []string) ([]*message.MessageUserExtraResp, error) {
			return []*message.MessageUserExtraResp{
				{MessageIDStr: "m1", MessageIsDeleted: 1},
				{MessageIDStr: "m2", MessageIsDeleted: 0},
				{MessageIDStr: "m3", MessageIsDeleted: 1},
			}, nil
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.UserDeletedSet("u1", []string{"m1", "m2", "m3"})
	if err != nil {
		t.Fatalf("UserDeletedSet err: %v", err)
	}
	if want := []string{"m1", "m3"}; !reflect.DeepEqual(sortedSetKeys(got), want) {
		t.Fatalf("UserDeletedSet keys = %v, want %v (m2 must be filtered out)", sortedSetKeys(got), want)
	}
	if fake.lastUIDForDeletedWithUID != "u1" {
		t.Fatalf("UserDeletedSet uid pass-through = %q, want %q", fake.lastUIDForDeletedWithUID, "u1")
	}
}

func TestProbe_UserDeletedSet_DBErrorPropagated(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	fake := &fakeMessageIService{
		deletedWithUIDFn: func(uid string, ids []string) ([]*message.MessageUserExtraResp, error) {
			return nil, dbErr
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.UserDeletedSet("u1", []string{"m1"})
	if !errors.Is(err, dbErr) {
		t.Fatalf("UserDeletedSet err = %v, want %v", err, dbErr)
	}
	if got != nil {
		t.Fatalf("UserDeletedSet on err = %v, want nil", got)
	}
}

// --- ChannelOffset ------------------------------------------------------

func TestProbe_ChannelOffset_MatchesChannelID(t *testing.T) {
	t.Parallel()
	fake := &fakeMessageIService{
		channelOffsetFn: func(uid string, channelIDs []string) ([]*message.ChannelOffsetResp, error) {
			// Return both — the probe must pick the row whose ChannelID
			// matches the queried id and ignore the rest.
			return []*message.ChannelOffsetResp{
				{ChannelID: "cidA", MessageSeq: 100},
				{ChannelID: "cidB", MessageSeq: 200},
			}, nil
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.ChannelOffset("u1", "cidB")
	if err != nil {
		t.Fatalf("ChannelOffset(cidB) err: %v", err)
	}
	if got != 200 {
		t.Fatalf("ChannelOffset(cidB) = %d, want 200", got)
	}

	got, err = probe.ChannelOffset("u1", "cidC")
	if err != nil {
		t.Fatalf("ChannelOffset(cidC) err: %v", err)
	}
	if got != 0 {
		t.Fatalf("ChannelOffset(cidC) = %d, want 0 (no match)", got)
	}

	if fake.channelOffsetCalls != 2 {
		t.Fatalf("ChannelOffset calls = %d, want 2", fake.channelOffsetCalls)
	}
	if want := []string{"cidC"}; !reflect.DeepEqual(fake.lastChannelIDsForOffset, want) {
		t.Fatalf("last channelIDs = %v, want %v (probe must call svc with single-channel slice)", fake.lastChannelIDsForOffset, want)
	}
	if fake.lastUIDForOffset != "u1" {
		t.Fatalf("ChannelOffset uid pass-through = %q, want %q", fake.lastUIDForOffset, "u1")
	}
}

func TestProbe_ChannelOffset_EmptyResponse(t *testing.T) {
	t.Parallel()
	fake := &fakeMessageIService{
		channelOffsetFn: func(uid string, channelIDs []string) ([]*message.ChannelOffsetResp, error) {
			return nil, nil
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.ChannelOffset("u1", "cidA")
	if err != nil {
		t.Fatalf("ChannelOffset err: %v", err)
	}
	if got != 0 {
		t.Fatalf("ChannelOffset on empty = %d, want 0", got)
	}
}

func TestProbe_ChannelOffset_DBErrorPropagated(t *testing.T) {
	t.Parallel()
	dbErr := errors.New("db down")
	fake := &fakeMessageIService{
		channelOffsetFn: func(uid string, channelIDs []string) ([]*message.ChannelOffsetResp, error) {
			return nil, dbErr
		},
	}
	probe := newMessageVisibilityProbe(fake)

	got, err := probe.ChannelOffset("u1", "cidA")
	if !errors.Is(err, dbErr) {
		t.Fatalf("ChannelOffset err = %v, want %v", err, dbErr)
	}
	if got != 0 {
		t.Fatalf("ChannelOffset on err = %d, want 0", got)
	}
}
