package auth

import "sync/atomic"

// Lookup-registry singletons that decouple modules/auth's HTTP handlers
// from the implementation packages they consume (modules/bot_api,
// modules/usersecret). Mirrors the existing AppBotRegistry pattern in
// modules/bot_api/registry.go: each provider module sets its
// implementation at init() time; modules/auth's verify handler reads
// from the atomic at request time.
//
// Why not constructor injection from main.go?
// The codebase's module.Setup walks an init-time registry to wire each
// module's Start hook with a *config.Context. There is no central place
// in main.go to thread *BotAPI / *usersecret.API into modules/auth's
// constructor. The atomic.Value pattern is the established way other
// modules already cross-wire (AppBotRegistry) and keeps the boot order
// independent: whichever module loads its init first writes; the reader
// at request time always sees a populated value (init() runs before
// HTTP routing).
//
// Tests can override either registry by calling the Set* function with
// a stub implementation; the atomic Store is goroutine-safe.

var (
	botLookupValue    atomic.Value // holds BotLookup
	apiKeyLookupValue atomic.Value // holds APIKeyLookup
)

// SetBotLookup is called by the BotLookup-implementing module at init().
// Calling with a nil v is a no-op; a future replacement (e.g. test stub)
// can overwrite the previous value.
func SetBotLookup(v BotLookup) {
	if v == nil {
		return
	}
	botLookupValue.Store(&botLookupHolder{v: v})
}

// GetBotLookup returns the registered BotLookup or nil if no provider has
// registered yet. Handlers MUST treat nil as an infrastructure failure
// (return ErrAuthUpstreamFailed); never panic.
func GetBotLookup() BotLookup {
	if h, _ := botLookupValue.Load().(*botLookupHolder); h != nil {
		return h.v
	}
	return nil
}

// SetAPIKeyLookup mirrors SetBotLookup for the API Key path.
func SetAPIKeyLookup(v APIKeyLookup) {
	if v == nil {
		return
	}
	apiKeyLookupValue.Store(&apiKeyLookupHolder{v: v})
}

// GetAPIKeyLookup mirrors GetBotLookup.
func GetAPIKeyLookup() APIKeyLookup {
	if h, _ := apiKeyLookupValue.Load().(*apiKeyLookupHolder); h != nil {
		return h.v
	}
	return nil
}

// Holder types let atomic.Value store interface values without panicking
// on the "stored type is not consistent" check (atomic.Value requires
// every Store to use the same concrete type). The holder pointer satisfies
// that constraint.
type (
	botLookupHolder    struct{ v BotLookup }
	apiKeyLookupHolder struct{ v APIKeyLookup }
)
