package config

import "testing"

func TestLoadFromEnvAddsLatestProtection(t *testing.T) {
	env := map[string]string{
		"REGISTRY_URL":       "https://registry.example.com/",
		"RUN_ONCE":           "true",
		"THRESHOLD_DAYS":     "30",
		"MIN_IMAGES_KEEP":    "3",
		"PROTECTED_TAGS":     "prod,stable",
		"DRY_RUN":            "true",
		"REGISTRY_TOKEN":     "token",
		"HTTP_TIMEOUT":       "5s",
		"RUN_ON_START":       "false",
		"PAGE_SIZE":          "50",
		"REGISTRY_BINARY":    "/bin/registry",
		"REGISTRY_READ_ONLY": "false",
	}

	cfg, err := LoadFromEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadFromEnv returned error: %v", err)
	}
	if cfg.RegistryURL != "https://registry.example.com" {
		t.Fatalf("unexpected registry URL: %q", cfg.RegistryURL)
	}
	for _, tag := range []string{"latest", "prod", "stable"} {
		if _, ok := cfg.ProtectedTags[tag]; !ok {
			t.Fatalf("expected protected tag %q", tag)
		}
	}
	if !cfg.GarbageCollectDryRun {
		t.Fatal("garbage collection dry-run should default to DRY_RUN")
	}
}

func TestLoadFromEnvRequiresReadOnlyForRealGC(t *testing.T) {
	env := map[string]string{
		"REGISTRY_URL":            "https://registry.example.com",
		"RUN_ONCE":                "true",
		"THRESHOLD_DAYS":          "30",
		"MIN_IMAGES_KEEP":         "3",
		"DRY_RUN":                 "false",
		"RUN_GARBAGE_COLLECT":     "true",
		"GARBAGE_COLLECT_DRY_RUN": "false",
		"REGISTRY_CONFIG_PATH":    "/etc/docker/registry/config.yml",
	}

	_, err := LoadFromEnv(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err == nil {
		t.Fatal("expected real GC without read-only confirmation to fail")
	}
}
