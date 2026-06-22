package bot_api

import (
	"sync"
	"sync/atomic"
)

// AppBotRegistrySpec is the minimal spec needed by bot_api auth.
//
// PR-A2 widened this struct to carry DisplayName and CreatedBy so the
// registry-hit fast path in LookupAppBot can return a complete
// AppBotIdentity (BotName + OwnerUID) without falling back to a DB
// round-trip. Previously the registry only carried UID/Scope/SpaceID;
// any caller wanting the owner UID had to do its own DB lookup or
// accept an empty field. Jerry-Xin flagged this on octo-server #430
// review as blocking — the registry IS the primary lookup path, so
// it must produce complete identities for the seam to be useful.
type AppBotRegistrySpec struct {
	UID         string
	DisplayName string
	Scope       string
	SpaceID     string
	CreatedBy   string
}

// AppBotRegistryInterface is the interface for App Bot in-memory registry lookup.
type AppBotRegistryInterface interface {
	FindByToken(token string) *AppBotRegistrySpec
}

// appBotRegistryValue stores AppBotRegistryInterface, set by the app_bot module on init.
var appBotRegistryValue atomic.Value

// SetAppBotRegistry sets the global App Bot registry (called by app_bot module).
func SetAppBotRegistry(r AppBotRegistryInterface) {
	appBotRegistryValue.Store(r)
}

// GetAppBotRegistry returns the global App Bot registry.
func GetAppBotRegistry() AppBotRegistryInterface {
	v := appBotRegistryValue.Load()
	if v == nil {
		return nil
	}
	return v.(AppBotRegistryInterface)
}

// AppBotRegistryAdapter adapts an external registry to AppBotRegistryInterface.
// The app_bot module sets this on startup.
type AppBotRegistryAdapter struct {
	mu      sync.RWMutex
	byToken map[string]*AppBotRegistrySpec
}

// NewAppBotRegistryAdapter creates a new adapter.
func NewAppBotRegistryAdapter() *AppBotRegistryAdapter {
	return &AppBotRegistryAdapter{
		byToken: make(map[string]*AppBotRegistrySpec),
	}
}

// FindByToken looks up spec by token.
func (a *AppBotRegistryAdapter) FindByToken(token string) *AppBotRegistrySpec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.byToken[token]
}

// Add adds a spec by token.
func (a *AppBotRegistryAdapter) Add(token string, spec *AppBotRegistrySpec) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.byToken[token] = spec
}

// Remove removes a spec by token.
func (a *AppBotRegistryAdapter) Remove(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.byToken, token)
}

// Update atomically replaces a spec by old and new token.
func (a *AppBotRegistryAdapter) Update(oldToken, newToken string, spec *AppBotRegistrySpec) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.byToken, oldToken)
	a.byToken[newToken] = spec
}
