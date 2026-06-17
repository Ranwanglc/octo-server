package bot_api

import "time"

// OCT-41 — stream_no → robotID ownership binding.
//
// /v1/bot/stream/end is gated by checkSendPermission on the supplied
// channel_id, but that gate only proves the caller MAY post to that channel —
// it does NOT prove the caller opened the stream it is trying to end. WuKongIM
// resolves a stream by its stream_no alone; the channel_id/channel_type in the
// end request are addressing/routing fields, not an ownership or
// stream↔channel authorization check (the deployed WuKongIM exposes no way to
// assert "stream_no belongs to channel_id"). Without an explicit binding a
// co-member bot could observe a peer's live stream_no (it is rendered into the
// channel and visible via /messages/sync) and prematurely terminate the peer's
// bubble. We therefore record which bot opened each stream at start and reject
// an end whose recorded owner is a different bot.
//
// The binding is keyed solely on stream_no and is independent of channel_id,
// so it also closes the channel-gate-bypass concern in OCT-41 item #2: an
// attacker cannot end a stream they do not own no matter which channel they
// claim in the request.

const (
	// streamOwnerKeyPrefix namespaces the Redis keys that hold a stream's owner.
	streamOwnerKeyPrefix = "bot:stream:owner:"

	// streamOwnerTTL bounds how long a stream_no → robotID binding lives. It
	// must comfortably exceed the longest plausible live bubble (a slow LLM
	// generation streamed token-by-token) so a still-streaming bubble is never
	// orphaned — an absent binding fails open (see streamEnd) to preserve the
	// terminal-END guarantee, so the only cost of an over-long TTL is a slightly
	// larger Redis key set. Streams normally close within seconds; one hour is
	// deliberately generous.
	streamOwnerTTL = time.Hour
)

// streamOwnerStore records and verifies which bot owns a given WuKongIM stream.
// Production uses a Redis-backed implementation; unit tests inject an in-memory
// store via BotAPI.streamOwnerStoreOverride so the ownership gate can be
// table-tested without a live Redis.
type streamOwnerStore interface {
	// bind records streamNo → robotID at stream open. It overwrites any prior
	// binding for the same stream_no.
	bind(streamNo, robotID string) error
	// owner returns the bound robotID for streamNo, or "" when no binding
	// exists (TTL elapsed, opened outside this path, or never recorded).
	owner(streamNo string) (string, error)
	// release removes the binding after a successful end so the stream_no does
	// not linger in the store. Best-effort; callers ignore the error.
	release(streamNo string) error
}

// redisStreamOwnerStore is the production streamOwnerStore backed by the shared
// Redis connection on the BotAPI context.
type redisStreamOwnerStore struct {
	ba  *BotAPI
	ttl time.Duration
}

func (s redisStreamOwnerStore) key(streamNo string) string {
	return streamOwnerKeyPrefix + streamNo
}

func (s redisStreamOwnerStore) bind(streamNo, robotID string) error {
	return s.ba.ctx.GetRedisConn().SetAndExpire(s.key(streamNo), robotID, s.ttl)
}

func (s redisStreamOwnerStore) owner(streamNo string) (string, error) {
	// GetString returns ("", nil) for a missing key, which the caller treats as
	// "no binding" → fail open.
	return s.ba.ctx.GetRedisConn().GetString(s.key(streamNo))
}

func (s redisStreamOwnerStore) release(streamNo string) error {
	return s.ba.ctx.GetRedisConn().Del(s.key(streamNo))
}

// streamOwners returns the active streamOwnerStore: the test override when set,
// otherwise the Redis-backed production store.
func (ba *BotAPI) streamOwners() streamOwnerStore {
	if ba.streamOwnerStoreOverride != nil {
		return ba.streamOwnerStoreOverride
	}
	return redisStreamOwnerStore{ba: ba, ttl: streamOwnerTTL}
}
