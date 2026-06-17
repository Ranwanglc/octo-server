package botfather

import (
	"strings"
	"testing"
)

// Regression guard for #362: the BotFather /daemon onboarding instructions
// used to tell users to `export OCTO_MATTER_URL`, but the octo-daemon binary
// never reads that env (it reaches matter through the fleet/server endpoints).
// The start block and env_vars must therefore expose exactly the two URLs the
// daemon actually consumes — OCTO_SERVER_URL and OCTO_FLEET_URL — and never
// OCTO_MATTER_URL.

const (
	envServerURL = "OCTO_SERVER_URL"
	envFleetURL  = "OCTO_FLEET_URL"
	envMatterURL = "OCTO_MATTER_URL"
)

func TestBuildDaemonStartBlock_OmitsMatterURL(t *testing.T) {
	const (
		serverURL = "http://octo.example.com:8090"
		fleetURL  = "http://octo.example.com:8092"
		apiKey    = "uk_test_key"
	)

	block := buildDaemonStartBlock(serverURL, fleetURL, apiKey)

	if strings.Contains(block, envMatterURL) {
		t.Fatalf("start block must not instruct OCTO_MATTER_URL (#362); got:\n%s", block)
	}
	for _, want := range []string{envServerURL, envFleetURL} {
		if !strings.Contains(block, want) {
			t.Errorf("start block must export %s; got:\n%s", want, block)
		}
	}
	if !strings.Contains(block, serverURL) || !strings.Contains(block, fleetURL) {
		t.Errorf("start block must contain both derived URLs; got:\n%s", block)
	}
	if !strings.Contains(block, "--api-key "+apiKey) {
		t.Errorf("start block must pass the api key to octo-daemon start; got:\n%s", block)
	}
}

func TestDaemonEnvVars_OmitsMatterURL(t *testing.T) {
	const (
		serverURL = "http://octo.example.com:8090"
		fleetURL  = "http://octo.example.com:8092"
	)

	env := daemonEnvVars(serverURL, fleetURL)

	if _, ok := env[envMatterURL]; ok {
		t.Errorf("env_vars must not include %s (#362); got: %#v", envMatterURL, env)
	}
	if got := env[envServerURL]; got != serverURL {
		t.Errorf("env_vars[%s] = %q, want %q", envServerURL, got, serverURL)
	}
	if got := env[envFleetURL]; got != fleetURL {
		t.Errorf("env_vars[%s] = %q, want %q", envFleetURL, got, fleetURL)
	}
	if len(env) != 2 {
		t.Errorf("env_vars must expose exactly the two daemon-consumed URLs; got %d keys: %#v", len(env), env)
	}
}
