package config

import (
	"strings"
	"testing"
	"time"

	"github.com/skygenesisenterprise/notploy-actions/internal/github"
)

func mapGetenv(values map[string]string) github.Getenv {
	return func(name string) string { return values[name] }
}

// required is the smallest environment that passes validation.
func required() map[string]string {
	return map[string]string{
		"INPUT_ENDPOINT":       "https://notploy.example.com",
		"INPUT_API-KEY":        "npk_secret_value",
		"INPUT_APPLICATION-ID": "application-1",
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(mapGetenv(required()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Endpoint != "https://notploy.example.com" {
		t.Errorf("Endpoint = %q", cfg.Endpoint)
	}
	if cfg.APIKey != "npk_secret_value" {
		t.Errorf("APIKey was not read")
	}
	if cfg.ApplicationID != "application-1" {
		t.Errorf("ApplicationID = %q", cfg.ApplicationID)
	}
	if !cfg.Wait {
		t.Error("Wait should default to true")
	}
	if cfg.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %s, want %s", cfg.Timeout, DefaultTimeout)
	}
	if cfg.PollInterval != DefaultPollInterval {
		t.Errorf("PollInterval = %s, want %s", cfg.PollInterval, DefaultPollInterval)
	}
}

func TestLoadReadsEveryInput(t *testing.T) {
	env := required()
	env["INPUT_WAIT"] = "false"
	env["INPUT_TIMEOUT"] = "2m"
	env["INPUT_POLL-INTERVAL"] = "30s"

	cfg, err := Load(mapGetenv(env))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Wait {
		t.Error("Wait = true, want false")
	}
	if cfg.Timeout != 2*time.Minute {
		t.Errorf("Timeout = %s", cfg.Timeout)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %s", cfg.PollInterval)
	}
}

func TestLoadTrimsWhitespace(t *testing.T) {
	env := required()
	env["INPUT_ENDPOINT"] = "  https://notploy.example.com  "
	env["INPUT_WAIT"] = " true "

	cfg, err := Load(mapGetenv(env))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Endpoint != "https://notploy.example.com" {
		t.Errorf("Endpoint = %q", cfg.Endpoint)
	}
	if !cfg.Wait {
		t.Error("Wait = false, want true")
	}
}

func TestLoadReportsMissingRequiredInputs(t *testing.T) {
	tests := map[string]struct {
		remove  string
		missing string
	}{
		"endpoint":       {remove: "INPUT_ENDPOINT", missing: "endpoint"},
		"api-key":        {remove: "INPUT_API-KEY", missing: "api-key"},
		"application-id": {remove: "INPUT_APPLICATION-ID", missing: "application-id"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			env := required()
			delete(env, test.remove)

			_, err := Load(mapGetenv(env))
			if err == nil {
				t.Fatalf("Load() succeeded without %s", test.missing)
			}
			if !strings.Contains(err.Error(), test.missing) {
				t.Fatalf("error %q does not name %q", err, test.missing)
			}
		})
	}
}

func TestLoadRejectsInvalidTimeout(t *testing.T) {
	for _, value := range []string{"nope", "30", "-1s", "0s"} {
		env := required()
		env["INPUT_TIMEOUT"] = value

		if _, err := Load(mapGetenv(env)); err == nil {
			t.Fatalf("Load() accepted timeout %q", value)
		}
	}
}

func TestLoadRejectsInvalidPollInterval(t *testing.T) {
	// A missing unit, a negative value, zero, and a value below the floor.
	for _, value := range []string{"nope", "-5s", "0s", "500ms"} {
		env := required()
		env["INPUT_POLL-INTERVAL"] = value

		if _, err := Load(mapGetenv(env)); err == nil {
			t.Fatalf("Load() accepted poll-interval %q", value)
		}
	}
}

func TestLoadRejectsInvalidWait(t *testing.T) {
	env := required()
	env["INPUT_WAIT"] = "maybe"

	if _, err := Load(mapGetenv(env)); err == nil {
		t.Fatal("Load() accepted a non-boolean wait input")
	}
}

func TestLoadNeverLeaksAnInputValue(t *testing.T) {
	env := required()
	env["INPUT_TIMEOUT"] = "not-a-duration"

	_, err := Load(mapGetenv(env))
	if err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), env["INPUT_API-KEY"]) {
		t.Fatalf("error message leaked the API key: %q", err)
	}
	if strings.Contains(err.Error(), env["INPUT_ENDPOINT"]) {
		t.Fatalf("error message leaked the endpoint: %q", err)
	}
}
