package messages_search

import (
	"testing"
)

func TestNormalizedChannelID_DM(t *testing.T) {
	got := normalizedChannelID(channelTypePerson, "peerUID", "loginUID")
	// fakeChannelID is one of "{login}@{peer}" / "{peer}@{login}" depending
	// on hash. Just assert non-empty + contains both uids.
	if got == "" {
		t.Fatalf("expected non-empty normalized channelID")
	}
	if got != "loginUID@peerUID" && got != "peerUID@loginUID" {
		t.Fatalf("unexpected fakeChannelID format: %q", got)
	}
}

func TestNormalizedChannelID_Group(t *testing.T) {
	if got := normalizedChannelID(channelTypeGroup, "groupNo123", "loginUID"); got != "groupNo123" {
		t.Fatalf("group channel_id should pass through, got %q", got)
	}
}

func TestNormalizedChannelID_Thread(t *testing.T) {
	id := "groupNo____shortID12345"
	if got := normalizedChannelID(channelTypeThread, id, "loginUID"); got != id {
		t.Fatalf("thread channel_id should pass through, got %q", got)
	}
}

func TestGroupNoFromChannel(t *testing.T) {
	if got := groupNoFromChannel(channelTypeGroup, "abc"); got != "abc" {
		t.Fatalf("group: want abc, got %q", got)
	}
	if got := groupNoFromChannel(channelTypeThread, "parent____121212121212121"); got != "parent" {
		t.Fatalf("thread: want parent, got %q", got)
	}
	if got := groupNoFromChannel(channelTypeThread, "no-separator"); got != "" {
		t.Fatalf("malformed thread: want empty, got %q", got)
	}
	if got := groupNoFromChannel(channelTypePerson, "peer"); got != "" {
		t.Fatalf("p2p: want empty, got %q", got)
	}
}
