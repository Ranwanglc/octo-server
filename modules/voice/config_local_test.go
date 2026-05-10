package voice

import (
	"os"
	"testing"
)

func TestLocalEnabled_Default(t *testing.T) {
	os.Unsetenv("VOICE_LOCAL_ENABLED")
	cfg := NewVoiceConfigFromEnv()
	if !cfg.LocalEnabled {
		t.Errorf("expected LocalEnabled=true by default, got false")
	}
}

func TestLocalEnabled_False(t *testing.T) {
	os.Setenv("VOICE_LOCAL_ENABLED", "false")
	defer os.Unsetenv("VOICE_LOCAL_ENABLED")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalEnabled {
		t.Errorf("expected LocalEnabled=false when env=false, got true")
	}
}

func TestLocalEnabled_Zero(t *testing.T) {
	os.Setenv("VOICE_LOCAL_ENABLED", "0")
	defer os.Unsetenv("VOICE_LOCAL_ENABLED")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalEnabled {
		t.Errorf("expected LocalEnabled=false when env=0, got true")
	}
}

func TestLocalEnabled_TRUE(t *testing.T) {
	os.Setenv("VOICE_LOCAL_ENABLED", "TRUE")
	defer os.Unsetenv("VOICE_LOCAL_ENABLED")
	cfg := NewVoiceConfigFromEnv()
	if !cfg.LocalEnabled {
		t.Errorf("expected LocalEnabled=true when env=TRUE, got false")
	}
}

func TestLocalEnabled_InvalidValue(t *testing.T) {
	os.Setenv("VOICE_LOCAL_ENABLED", "invalidvalue")
	defer os.Unsetenv("VOICE_LOCAL_ENABLED")
	cfg := NewVoiceConfigFromEnv()
	if !cfg.LocalEnabled {
		t.Errorf("expected LocalEnabled=true when env=invalidvalue (ParseBool fails, default preserved), got false")
	}
}

func TestLocalTimeoutMs_Default(t *testing.T) {
	os.Unsetenv("VOICE_LOCAL_TIMEOUT_MS")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTimeoutMs != 10000 {
		t.Errorf("expected LocalTimeoutMs=10000 by default, got %d", cfg.LocalTimeoutMs)
	}
}

func TestLocalTimeoutMs_CustomValue(t *testing.T) {
	os.Setenv("VOICE_LOCAL_TIMEOUT_MS", "15000")
	defer os.Unsetenv("VOICE_LOCAL_TIMEOUT_MS")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTimeoutMs != 15000 {
		t.Errorf("expected LocalTimeoutMs=15000, got %d", cfg.LocalTimeoutMs)
	}
}

func TestLocalTimeoutMs_Zero(t *testing.T) {
	os.Setenv("VOICE_LOCAL_TIMEOUT_MS", "0")
	defer os.Unsetenv("VOICE_LOCAL_TIMEOUT_MS")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTimeoutMs != 10000 {
		t.Errorf("expected LocalTimeoutMs=10000 when env=0 (n>0 check), got %d", cfg.LocalTimeoutMs)
	}
}

func TestLocalTimeoutMs_Negative(t *testing.T) {
	os.Setenv("VOICE_LOCAL_TIMEOUT_MS", "-1")
	defer os.Unsetenv("VOICE_LOCAL_TIMEOUT_MS")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTimeoutMs != 10000 {
		t.Errorf("expected LocalTimeoutMs=10000 when env=-1, got %d", cfg.LocalTimeoutMs)
	}
}

func TestLocalTimeoutMs_ExceedsCap(t *testing.T) {
	os.Setenv("VOICE_LOCAL_TIMEOUT_MS", "999999")
	defer os.Unsetenv("VOICE_LOCAL_TIMEOUT_MS")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTimeoutMs != 60000 {
		t.Errorf("expected LocalTimeoutMs=60000 when env=999999 (capped at 60000), got %d", cfg.LocalTimeoutMs)
	}
}

func TestLocalTimeoutMs_InvalidString(t *testing.T) {
	os.Setenv("VOICE_LOCAL_TIMEOUT_MS", "abc")
	defer os.Unsetenv("VOICE_LOCAL_TIMEOUT_MS")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTimeoutMs != 10000 {
		t.Errorf("expected LocalTimeoutMs=10000 when env=abc (Atoi fails), got %d", cfg.LocalTimeoutMs)
	}
}

func TestLocalProbeURL_Default(t *testing.T) {
	os.Unsetenv("VOICE_LOCAL_PROBE_URL")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalProbeURL != "http://localhost:8787/" {
		t.Errorf("expected LocalProbeURL=http://localhost:8787/ by default, got %s", cfg.LocalProbeURL)
	}
}

func TestLocalTranscribeURL_Default(t *testing.T) {
	os.Unsetenv("VOICE_LOCAL_TRANSCRIBE_URL")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTranscribeURL != "http://localhost:8787/v1/voice/transcribe" {
		t.Errorf("expected LocalTranscribeURL=http://localhost:8787/v1/voice/transcribe by default, got %s", cfg.LocalTranscribeURL)
	}
}

func TestLocalProbeURL_Custom(t *testing.T) {
	os.Setenv("VOICE_LOCAL_PROBE_URL", "http://localhost:9999/health")
	defer os.Unsetenv("VOICE_LOCAL_PROBE_URL")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalProbeURL != "http://localhost:9999/health" {
		t.Errorf("expected LocalProbeURL=http://localhost:9999/health, got %s", cfg.LocalProbeURL)
	}
}

func TestLocalTranscribeURL_Custom(t *testing.T) {
	os.Setenv("VOICE_LOCAL_TRANSCRIBE_URL", "http://localhost:9999/api/transcribe")
	defer os.Unsetenv("VOICE_LOCAL_TRANSCRIBE_URL")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalTranscribeURL != "http://localhost:9999/api/transcribe" {
		t.Errorf("expected LocalTranscribeURL=http://localhost:9999/api/transcribe, got %s", cfg.LocalTranscribeURL)
	}
}

func TestLocalURLs_EmptyEnvPreservesDefaults(t *testing.T) {
	os.Setenv("VOICE_LOCAL_PROBE_URL", "")
	os.Setenv("VOICE_LOCAL_TRANSCRIBE_URL", "")
	defer os.Unsetenv("VOICE_LOCAL_PROBE_URL")
	defer os.Unsetenv("VOICE_LOCAL_TRANSCRIBE_URL")
	cfg := NewVoiceConfigFromEnv()
	if cfg.LocalProbeURL != "http://localhost:8787/" {
		t.Errorf("expected LocalProbeURL default preserved with empty env, got %s", cfg.LocalProbeURL)
	}
	if cfg.LocalTranscribeURL != "http://localhost:8787/v1/voice/transcribe" {
		t.Errorf("expected LocalTranscribeURL default preserved with empty env, got %s", cfg.LocalTranscribeURL)
	}
}
