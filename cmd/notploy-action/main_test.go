package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	testAPIKey    = "npk_end_to_end_key"
	testAppID     = "application-1"
	testOldID     = "deployment-1"
	testNewID     = "deployment-2"
	testPublicURL = "https://my-app.example.com"
)

// binary builds the action once per test run and returns its path.
var binary = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "notploy-action-build")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "notploy-action")
	build := exec.Command("go", "build", "-o", path, ".")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %w\n%s", err, output)
	}
	return path, nil
})

// scenario scripts a fake Notploy instance for one action run.
type scenario struct {
	// statuses are the statuses reported for the deployment this run creates,
	// in order. The last one is repeated, so the wait always terminates.
	statuses []string
	// inputKeySuffix lets a test use the other spelling of an input name.
	underscoredAPIKey bool
}

func TestActionSucceedsAndPublishesItsResult(t *testing.T) {
	result := runAction(t, scenario{statuses: []string{"running", "done"}})

	if result.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s", result.exitCode, result.stdout)
	}
	if !strings.Contains(result.stdout, "Deployment ID: "+testNewID) {
		t.Errorf("stdout does not report the deployment id:\n%s", result.stdout)
	}
	if !strings.Contains(result.stdout, "✓ Deployment successful") {
		t.Errorf("stdout does not report success:\n%s", result.stdout)
	}
	if result.outputs["deployment-id"] != testNewID {
		t.Errorf("deployment-id = %q", result.outputs["deployment-id"])
	}
	if result.outputs["deployment-status"] != "done" {
		t.Errorf("deployment-status = %q", result.outputs["deployment-status"])
	}
	if result.outputs["deployment-url"] != testPublicURL {
		t.Errorf("deployment-url = %q", result.outputs["deployment-url"])
	}
	if !strings.Contains(result.summary, "## Notploy deployment") {
		t.Errorf("summary is missing its title:\n%s", result.summary)
	}
	if !strings.Contains(result.summary, testNewID) {
		t.Errorf("summary is missing the deployment id:\n%s", result.summary)
	}
	if result.stdout == "" {
		t.Error("the action printed nothing")
	}
	// The summary is what reviewers read: secrets must never reach it.
	if strings.Contains(result.summary, testAPIKey) {
		t.Fatalf("the summary leaked the API key:\n%s", result.summary)
	}
}

func TestActionFailsWhenTheDeploymentFails(t *testing.T) {
	result := runAction(t, scenario{statuses: []string{"running", "error"}})

	if result.exitCode == 0 {
		t.Fatalf("exit code = 0, want a failure\nstdout:\n%s", result.stdout)
	}
	if result.outputs["deployment-status"] != "error" {
		t.Errorf("deployment-status = %q", result.outputs["deployment-status"])
	}
	if !strings.Contains(result.stdout, "Notploy deployment failed") {
		t.Errorf("stdout does not report the failure:\n%s", result.stdout)
	}
	// Outputs are published even when the deployment fails, so later steps can
	// link to the failed deployment.
	if result.outputs["deployment-id"] != testNewID {
		t.Errorf("deployment-id = %q", result.outputs["deployment-id"])
	}
}

func TestActionReadsUnderscoredInputNames(t *testing.T) {
	result := runAction(t, scenario{
		statuses:          []string{"done"},
		underscoredAPIKey: true,
	})

	if result.exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout:\n%s", result.exitCode, result.stdout)
	}
	if result.outputs["deployment-status"] != "done" {
		t.Errorf("deployment-status = %q", result.outputs["deployment-status"])
	}
}

type runResult struct {
	exitCode int
	stdout   string
	outputs  map[string]string
	summary  string
}

func runAction(t *testing.T, scenario scenario) runResult {
	t.Helper()

	path, err := binary()
	if err != nil {
		t.Fatalf("build the action: %v", err)
	}

	instance := newFakeInstance(t, scenario)

	workDir := t.TempDir()
	outputPath := filepath.Join(workDir, "outputs")
	summaryPath := filepath.Join(workDir, "summary")

	apiKeyInput := "INPUT_API-KEY"
	if scenario.underscoredAPIKey {
		apiKeyInput = "INPUT_API_KEY"
	}

	command := exec.Command(path)
	command.Env = []string{
		"INPUT_ENDPOINT=" + instance.URL,
		apiKeyInput + "=" + testAPIKey,
		"INPUT_APPLICATION-ID=" + testAppID,
		"INPUT_WAIT=true",
		"INPUT_TIMEOUT=30s",
		"INPUT_POLL-INTERVAL=1s",
		"GITHUB_OUTPUT=" + outputPath,
		"GITHUB_STEP_SUMMARY=" + summaryPath,
		"GITHUB_REPOSITORY=skygenesisenterprise/notploy",
		"GITHUB_REF=refs/heads/main",
		"GITHUB_REF_NAME=main",
		"GITHUB_SHA=0123456789abcdef0123456789abcdef01234567",
		"GITHUB_ACTOR=octocat",
		"GITHUB_EVENT_NAME=push",
		"GITHUB_RUN_ID=123456",
		"GITHUB_RUN_NUMBER=42",
		"GITHUB_SERVER_URL=https://github.com",
		"GITHUB_WORKFLOW=Deploy",
	}
	output, err := command.CombinedOutput()

	result := runResult{
		stdout:  string(output),
		outputs: parseOutputs(t, outputPath),
		summary: readIfPresent(t, summaryPath),
	}
	if err == nil {
		return result
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("run the action: %v\noutput:\n%s", err, output)
	}
	result.exitCode = exitErr.ExitCode()
	return result
}

// newFakeInstance serves the three Notploy endpoints the action uses.
func newFakeInstance(t *testing.T, scenario scenario) *httptest.Server {
	t.Helper()

	var (
		mutex       sync.Mutex
		listCalls   int
		deployments []map[string]any
	)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/application.deploy", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("x-api-key") != testAPIKey {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"message": "Authorization not provided",
				"code":    "UNAUTHORIZED",
			})
			return
		}

		mutex.Lock()
		defer mutex.Unlock()
		// The deployment row appears only after the trigger.
		deployments = append([]map[string]any{{
			"deploymentId":  testNewID,
			"title":         "GitHub Actions: Deploy #42",
			"status":        "running",
			"applicationId": testAppID,
			"createdAt":     "2026-09-28T12:00:00.000Z",
		}}, deployments...)
		writeJSON(w, http.StatusOK, map[string]any{})
	})

	mux.HandleFunc("/api/deployment.all", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != testAPIKey {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "nope"})
			return
		}

		mutex.Lock()
		defer mutex.Unlock()

		call := listCalls
		listCalls++

		for _, deployment := range deployments {
			if deployment["deploymentId"] == testNewID {
				deployment["status"] = statusAt(scenario.statuses, call-1)
			}
		}
		writeJSON(w, http.StatusOK, deployments)
	})

	mux.HandleFunc("/api/application.one", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"applicationId": testAppID,
			"name":          "my-app",
			"domains": []map[string]any{
				{"host": "my-app.example.com", "https": true, "enabled": true},
			},
		})
	})

	// The historical deployment the snapshot has to ignore.
	deployments = []map[string]any{{
		"deploymentId":  testOldID,
		"title":         "previous",
		"status":        "done",
		"applicationId": testAppID,
		"createdAt":     "2026-09-27T12:00:00.000Z",
	}}

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// statusAt indexes the scripted statuses, ignoring the pre-trigger snapshot
// call and repeating the last entry.
func statusAt(statuses []string, pollIndex int) string {
	if len(statuses) == 0 {
		return "done"
	}
	if pollIndex < 0 {
		return statuses[0]
	}
	if pollIndex >= len(statuses) {
		return statuses[len(statuses)-1]
	}
	return statuses[pollIndex]
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func parseOutputs(t *testing.T, path string) map[string]string {
	t.Helper()

	content := readIfPresent(t, path)
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if line == "" || strings.Contains(line, "<<") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = value
	}
	return values
}

func readIfPresent(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
