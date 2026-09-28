package github

import (
	"fmt"
	"io"
	"strings"
)

// Logger writes plain lines and GitHub workflow commands.
//
// It satisfies the small logging contract expected by the deployment service,
// which therefore never has to know how the runner renders messages.
type Logger struct {
	w io.Writer
}

// NewLogger returns a logger writing to w (os.Stdout in the action).
func NewLogger(w io.Writer) *Logger {
	return &Logger{w: w}
}

// Infof writes a plain log line. Values are formatted as-is because plain lines
// have no workflow-command syntax to escape.
func (l *Logger) Infof(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
}

// Noticef writes an annotated notice.
func (l *Logger) Noticef(format string, args ...any) {
	l.command("notice", format, args...)
}

// Warningf writes an annotated warning.
func (l *Logger) Warningf(format string, args ...any) {
	l.command("warning", format, args...)
}

// Errorf writes an annotated error.
func (l *Logger) Errorf(format string, args ...any) {
	l.command("error", format, args...)
}

// Group starts a collapsible log group and returns the function that closes it.
func (l *Logger) Group(title string) func() {
	fmt.Fprintf(l.w, "::group::%s\n", escapeCommandValue(title))
	return func() { fmt.Fprintln(l.w, "::endgroup::") }
}

// Mask registers a value with the runner so every later log line containing it
// is redacted. Empty values — and values a workflow command cannot carry — are
// ignored rather than printed half-escaped.
func (l *Logger) Mask(value string) {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return
	}
	fmt.Fprintf(l.w, "::add-mask::%s\n", value)
}

// command formats first and escapes second, so a value coming from the API can
// never inject its own workflow command.
func (l *Logger) command(name, format string, args ...any) {
	fmt.Fprintf(l.w, "::%s::%s\n", name, escapeCommandValue(fmt.Sprintf(format, args...)))
}

// escapeCommandValue implements the escaping GitHub requires in the message
// part of a workflow command.
func escapeCommandValue(value string) string {
	return strings.NewReplacer(
		"%", "%25",
		"\r", "%0D",
		"\n", "%0A",
	).Replace(value)
}
