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

func TestFileIndexDefaults(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SemanticSearch {
		t.Error("semantic_search should default true")
	}
	if len(cfg.IndexRoots) == 0 {
		t.Error("index_roots should have defaults")
	}
}

func TestSemanticSearchRespectsFalse(t *testing.T) {
	cfg, err := loadFromBytes([]byte(`{"semantic_search":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SemanticSearch {
		t.Error("explicit false must be honored")
	}
}

func TestWakeWordDefaults(t *testing.T) {
	// Absent keys: enabled true, default phrase + model dir.
	cfg, err := loadFromBytes([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.WakeWordEnabled {
		t.Error("WakeWordEnabled should default true when absent")
	}
	if cfg.WakeWord != DefaultWakeWord {
		t.Errorf("WakeWord = %q, want %q", cfg.WakeWord, DefaultWakeWord)
	}
	if cfg.KWSModelPath != DefaultKWSModelPath {
		t.Errorf("KWSModelPath = %q, want %q", cfg.KWSModelPath, DefaultKWSModelPath)
	}
	// Explicit false is honored.
	cfg2, _ := loadFromBytes([]byte(`{"wake_word_enabled": false}`))
	if cfg2.WakeWordEnabled {
		t.Error("explicit wake_word_enabled=false must be honored")
	}
}
