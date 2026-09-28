package github

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const summaryEnv = "GITHUB_STEP_SUMMARY"

// Summary is the data rendered into the job summary.
//
// Every field is either public repository metadata or a Notploy identifier;
// credentials are never part of it.
type Summary struct {
	Application  string
	Repository   string
	Commit       string
	CommitURL    string
	Ref          string
	Actor        string
	Workflow     string
	RunNumber    string
	DeploymentID string
	Status       string
	URL          string
	Duration     time.Duration
	Error        string
}

// Markdown renders the summary as a table.
func (s Summary) Markdown() string {
	var b strings.Builder
	b.WriteString("## Notploy deployment\n\n")
	b.WriteString("| Field | Value |\n")
	b.WriteString("| --- | --- |\n")

	writeRow(&b, "Application", s.Application)

	commit := s.Commit
	if s.CommitURL != "" && s.Commit != "" {
		commit = fmt.Sprintf("[%s](%s)", escapeCell(s.Commit), s.CommitURL)
	}
	writeRow(&b, "Commit", commit)

	writeRow(&b, "Repository", s.Repository)
	writeRow(&b, "Ref", s.Ref)
	writeRow(&b, "Triggered by", s.Actor)
	if workflow := s.workflowLabel(); workflow != "" {
		writeRow(&b, "Workflow", workflow)
	}
	writeRow(&b, "Deployment", s.DeploymentID)
	writeRow(&b, "Status", statusLabel(s.Status))
	if s.URL != "" {
		writeRow(&b, "URL", s.URL)
	}
	if s.Duration > 0 {
		writeRow(&b, "Duration", s.Duration.Round(time.Second).String())
	}

	if s.Error != "" {
		b.WriteString("\n**Error:** ")
		b.WriteString(escapeCell(s.Error))
		b.WriteString("\n")
	}
	return b.String()
}

func (s Summary) workflowLabel() string {
	switch {
	case s.Workflow != "" && s.RunNumber != "":
		return s.Workflow + " #" + s.RunNumber
	case s.Workflow != "":
		return s.Workflow
	case s.RunNumber != "":
		return "#" + s.RunNumber
	default:
		return ""
	}
}

func writeRow(b *strings.Builder, field, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "| %s | %s |\n", escapeCell(field), escapeCell(value))
}

// escapeCell keeps user-controlled values from breaking out of the table cell.
func escapeCell(value string) string {
	replacer := strings.NewReplacer(
		"|", "\\|",
		"\\", "\\\\",
		"\r\n", " ",
		"\r", " ",
		"\n", " ",
	)
	return strings.TrimSpace(replacer.Replace(value))
}

// statusLabel renders a Notploy deployment status for humans.
func statusLabel(status string) string {
	switch status {
	case "done":
		return "✅ success"
	case "error":
		return "❌ failed"
	case "cancelled":
		return "⚠️ cancelled"
	case "running":
		return "⏳ running"
	case "":
		return ""
	case "triggered":
		return "🚀 triggered"
	default:
		return status
	}
}

// WriteSummary appends markdown to the job summary ($GITHUB_STEP_SUMMARY).
// It is a no-op outside of GitHub Actions.
func WriteSummary(getenv Getenv, markdown string) error {
	path := getenv(summaryEnv)
	if path == "" || strings.TrimSpace(markdown) == "" {
		return nil
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", summaryEnv, err)
	}
	defer file.Close()

	if _, err := io.WriteString(file, markdown); err != nil {
		return fmt.Errorf("write %s: %w", summaryEnv, err)
	}
	return nil
}
