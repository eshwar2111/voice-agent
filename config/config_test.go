package config

import "testing"

func TestTrustedExecutionDefaultsTrue(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{"llm_provider":"gemini","api_key":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TrustedExecution {
		t.Error("trusted_execution should default true when absent")
	}
}

func TestTrustedExecutionRespectsFalse(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{"trusted_execution":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrustedExecution {
		t.Error("explicit false must be honored")
	}
}

func TestSpeakResponsesDefaultsTrue(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{"llm_provider":"gemini","api_key":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SpeakResponses {
		t.Error("speak_responses should default true when absent")
	}
}

func TestSpeakResponsesRespectsFalse(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{"speak_responses":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SpeakResponses {
		t.Error("explicit false must be honored")
	}
}
