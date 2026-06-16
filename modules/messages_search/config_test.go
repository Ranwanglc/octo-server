package messages_search

import (
	"os"
	"testing"
)

// The OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY env follows the same strict literal
// "true" match as OCTO_SEARCH_OS_INSECURE_HTTP — anything else (1, yes, TRUE,
// True, "") leaves verification ON. Lock that contract in so a future
// "helpful" refactor to strconv.ParseBool doesn't widen the door silently.
func TestLoadConfig_OSInsecureSkipVerify(t *testing.T) {
	cases := []struct {
		name string
		val  string
		set  bool
		want bool
	}{
		{name: "unset defaults to false", set: false, want: false},
		{name: "empty string is false", val: "", set: true, want: false},
		{name: "literal true is true", val: "true", set: true, want: true},
		{name: "uppercase TRUE is false", val: "TRUE", set: true, want: false},
		{name: "mixed-case True is false", val: "True", set: true, want: false},
		{name: "numeric 1 is false", val: "1", set: true, want: false},
		{name: "yes is false", val: "yes", set: true, want: false},
		{name: "false is false", val: "false", set: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv("OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY", tc.val)
			} else {
				// Defensively clear any host-leaked value; restore on cleanup.
				prev, had := os.LookupEnv("OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY")
				_ = os.Unsetenv("OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY")
				t.Cleanup(func() {
					if had {
						_ = os.Setenv("OCTO_SEARCH_OS_INSECURE_SKIP_VERIFY", prev)
					}
				})
			}
			cfg := loadConfig()
			if cfg.OSInsecureSkipVerify != tc.want {
				t.Fatalf("OSInsecureSkipVerify = %v, want %v (env=%q set=%v)",
					cfg.OSInsecureSkipVerify, tc.want, tc.val, tc.set)
			}
		})
	}
}
