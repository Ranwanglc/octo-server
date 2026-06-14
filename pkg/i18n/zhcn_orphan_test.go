package i18n

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-server/pkg/i18n/codes"
	// Side-effect import: registers every err.server.* code in init() so
	// codes.All() is the complete set. Without it this package's tests only see
	// the err.shared.* codes and every err.server.* zh key would look orphaned.
	// (errcode imports pkg/i18n/codes, not pkg/i18n, so there is no cycle.)
	_ "github.com/Mininglamp-OSS/octo-server/pkg/errcode"
)

// zhCNKeyRE matches a go-i18n message-id header line: `["err.server.foo.bar"]`.
var zhCNKeyRE = regexp.MustCompile(`(?m)^\["([^"]+)"\]`)

// TestActiveZhCN_NoOrphanKeys pins that every err.* message key in
// locales/active.zh-CN.toml corresponds to a registered code. An orphan key —
// almost always a typo'd ID or a stale entry left after a code was renamed —
// binds to no code, so the intended translation never reaches the wire (the
// renderer silently falls back to the en-US source) and nothing else catches
// it: i18n-extract-check only validates the en-US markers, and
// TestBundle_DefaultMessagesMatchTOML only covers codes that set
// DefaultMessages["zh-CN"]. This guard closes that gap for the whole zh-CN
// bundle.
func TestActiveZhCN_NoOrphanKeys(t *testing.T) {
	data, err := localesFS.ReadFile("locales/active.zh-CN.toml")
	if err != nil {
		t.Fatalf("read active.zh-CN.toml: %v", err)
	}

	registered := make(map[string]bool, len(codes.All()))
	for _, c := range codes.All() {
		registered[c.ID] = true
	}

	for _, m := range zhCNKeyRE.FindAllStringSubmatch(string(data), -1) {
		id := m[1]
		if !strings.HasPrefix(id, "err.") {
			continue
		}
		if !registered[id] {
			t.Errorf("active.zh-CN.toml key %q has no registered code (orphan — likely a typo'd or renamed ID; its translation will never render)", id)
		}
	}
}
