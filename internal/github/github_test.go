package github

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mapGetenv(values map[string]string) Getenv {
	return func(name string) string { return values[name] }
}

func TestReadInputAcceptsBothSpellings(t *testing.T) {
	tests := map[string]struct {
		input string
		env   map[string]string
		want  string
	}{
		"hyphenated key": {
			input: "api-key",
			env:   map[string]string{"INPUT_API-KEY": "s3cret"},
			want:  "s3cret",
		},
		"underscored key": {
			input: "api-key",
			env:   map[string]string{"INPUT_API_KEY": "s3cret"},
			want:  "s3cret",
		},
		"absent": {
			input: "api-key",
			env:   map[string]string{},
			want:  "",
		},
		"explicitly empty": {
			input: "api-key",
			env:   map[string]string{"INPUT_API-KEY": ""},
			want:  "",
		},
		"name without hyphen": {
			input: "endpoint",
			env:   map[string]string{"INPUT_ENDPOINT": "https://notploy.example.com"},
			want:  "https://notploy.example.com",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := ReadInput(mapGetenv(test.env), test.input); got != test.want {
				t.Fatalf("ReadInput(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestLoadContext(t *testing.T) {
	context := LoadContext(mapGetenv(map[string]string{
		"GITHUB_REPOSITORY": "skygenesisenterprise/notploy",
		"GITHUB_REF":        "refs/heads/main",
		"GITHUB_REF_NAME":   "main",
		"GITHUB_SHA":        "0123456789abcdef0123456789abcdef01234567",
		"GITHUB_ACTOR":      "octocat",
		"GITHUB_EVENT_NAME": "workflow_dispatch",
		"GITHUB_RUN_ID":     "123456",
		"GITHUB_RUN_NUMBER": "42",
		"GITHUB_SERVER_URL": "https://github.com",
		"GITHUB_WORKFLOW":   "Deploy",
	}))

	if context.Repository != "skygenesisenterprise/notploy" {
		t.Errorf("Repository = %q", context.Repository)
	}
	if context.Ref != "refs/heads/main" {
		t.Errorf("Ref = %q", context.Ref)
	}
	if context.Sha != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("Sha = %q", context.Sha)
	}
	if context.Actor != "octocat" {
		t.Errorf("Actor = %q", context.Actor)
	}
	if context.Workflow != "Deploy" {
		t.Errorf("Workflow = %q", context.Workflow)
	}
	if context.RunID != "123456" || context.RunNumber != "42" {
		t.Errorf("run identification = %q / %q", context.RunID, context.RunNumber)
	}

	if got := context.ShortSha(); got != "0123456" {
		t.Errorf("ShortSha() = %q", got)
	}
	if got := context.Revision(); got != "skygenesisenterprise/notploy@0123456" {
		t.Errorf("Revision() = %q", got)
	}
	if got := context.CommitURL(); got != "https://github.com/skygenesisenterprise/notploy/commit/0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("CommitURL() = %q", got)
	}
	if got := context.DeploymentTitle(); got != "GitHub Actions: Deploy #42 skygenesisenterprise/notploy@0123456" {
		t.Errorf("DeploymentTitle() = %q", got)
	}
	if got := context.DeploymentDescription(); got != "event workflow_dispatch, ref refs/heads/main, by octocat, run 123456" {
		t.Errorf("DeploymentDescription() = %q", got)
	}
}

func TestContextOutsideOfActionsIsUsable(t *testing.T) {
	context := LoadContext(mapGetenv(nil))

	if got := context.DeploymentTitle(); got != "GitHub Actions deployment" {
		t.Errorf("DeploymentTitle() = %q", got)
	}
	if got := context.DeploymentDescription(); got != "" {
		t.Errorf("DeploymentDescription() = %q", got)
	}
	if got := context.CommitURL(); got != "" {
		t.Errorf("CommitURL() = %q", got)
	}
	if got := context.Revision(); got != "" {
		t.Errorf("Revision() = %q", got)
	}
}

func TestContextHandlesShortSha(t *testing.T) {
	context := Context{Sha: "abc"}
	if got := context.ShortSha(); got != "abc" {
		t.Fatalf("ShortSha() = %q", got)
	}
}

func TestWriteOutputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outputs")

	err := WriteOutputs(mapGetenv(map[string]string{"GITHUB_OUTPUT": path}), map[string]string{
		"deployment-url":    "https://example.com",
		"deployment-id":     "deployment-1",
		"deployment-status": "done",
	})
	if err != nil {
		t.Fatalf("WriteOutputs() error = %v", err)
	}

	content := readFile(t, path)
	want := "deployment-id=deployment-1\ndeployment-status=done\ndeployment-url=https://example.com\n"
	if content != want {
		t.Fatalf("outputs =\n%q\nwant\n%q", content, want)
	}
}

func TestWriteOutputsUsesDelimiterForMultilineValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outputs")

	err := WriteOutputs(mapGetenv(map[string]string{"GITHUB_OUTPUT": path}), map[string]string{
		"deployment-status": "line one\nline two",
	})
	if err != nil {
		t.Fatalf("WriteOutputs() error = %v", err)
	}

	content := readFile(t, path)

	firstLine, _, ok := strings.Cut(content, "\n")
	if !ok {
		t.Fatalf("outputs = %q, want a multi-line delimiter form", content)
	}
	delimiter, ok := strings.CutPrefix(firstLine, "deployment-status<<")
	if !ok {
		t.Fatalf("outputs = %q, want the delimiter form", content)
	}
	// The very same delimiter must open and close the value.
	if strings.Count(content, delimiter) != 2 {
		t.Fatalf("outputs = %q, want the delimiter %q twice", content, delimiter)
	}
	if !strings.Contains(content, "line one\nline two") {
		t.Fatalf("outputs = %q, want the value to be preserved", content)
	}
}

func TestWriteOutputsIsANoOpOutsideActions(t *testing.T) {
	if err := WriteOutputs(mapGetenv(nil), map[string]string{"deployment-id": "x"}); err != nil {
		t.Fatalf("WriteOutputs() error = %v", err)
	}
}

func TestWriteSummaryAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary")
	getenv := mapGetenv(map[string]string{"GITHUB_STEP_SUMMARY": path})

	if err := WriteSummary(getenv, "## Notploy deployment\n"); err != nil {
		t.Fatalf("WriteSummary() error = %v", err)
	}
	if err := WriteSummary(getenv, "second"); err != nil {
		t.Fatalf("WriteSummary() error = %v", err)
	}

	if got, want := readFile(t, path), "## Notploy deployment\nsecond"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestWriteSummaryIsANoOpOutsideActions(t *testing.T) {
	if err := WriteSummary(mapGetenv(nil), "## hi"); err != nil {
		t.Fatalf("WriteSummary() error = %v", err)
	}
}

func TestSummaryMarkdownEscapesTableCells(t *testing.T) {
	markdown := Summary{
		Application:  "my|app",
		Repository:   "owner/repository",
		DeploymentID: "184",
		Status:       "done",
		URL:          "https://example.com",
	}.Markdown()

	if !strings.Contains(markdown, `my\|app`) {
		t.Fatalf("markdown did not escape the pipe:\n%s", markdown)
	}
	if strings.Contains(markdown, "| my|app |") {
		t.Fatalf("markdown has an unescaped cell separator:\n%s", markdown)
	}
	if !strings.Contains(markdown, "✅ success") {
		t.Fatalf("markdown did not label the status:\n%s", markdown)
	}
}

func TestSummaryMarkdownFlattensNewlines(t *testing.T) {
	markdown := Summary{Application: "app", Status: "error", Error: "line\ninjected"}.Markdown()
	if strings.Contains(markdown, "line\ninjected") {
		t.Fatalf("markdown kept an injected newline:\n%s", markdown)
	}
}

func TestLoggerEscapesWorkflowCommandValues(t *testing.T) {
	var buffer bytes.Buffer
	logger := NewLogger(&buffer)

	logger.Warningf("boom %s", "line1\nline2%")

	if got, want := buffer.String(), "::warning::boom line1%0Aline2%25\n"; got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}

func TestLoggerMask(t *testing.T) {
	var buffer bytes.Buffer
	logger := NewLogger(&buffer)

	logger.Mask("s3cret")
	logger.Mask("")
	logger.Mask("with\nnewline")

	if got, want := buffer.String(), "::add-mask::s3cret\n"; got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}

func TestLoggerGroup(t *testing.T) {
	var buffer bytes.Buffer
	logger := NewLogger(&buffer)

	endGroup := logger.Group("Notploy")
	endGroup()

	if got, want := buffer.String(), "::group::Notploy\n::endgroup::\n"; got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
