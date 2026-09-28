package github

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const outputEnv = "GITHUB_OUTPUT"

// WriteOutputs appends every step output to the file the runner reads
// ($GITHUB_OUTPUT).
//
// It is a no-op when the variable is unset, which is the case when the binary is
// run by hand. Keys are written in a stable (sorted) order so that two runs with
// the same inputs produce the same file.
func WriteOutputs(getenv Getenv, values map[string]string) error {
	path := getenv(outputEnv)
	if path == "" || len(values) == 0 {
		return nil
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", outputEnv, err)
	}
	defer file.Close()

	for _, key := range keys {
		if err := writeKeyValue(file, key, values[key]); err != nil {
			return err
		}
	}
	return nil
}

// writeKeyValue writes `name=value` for single-line values and the delimiter
// form GitHub requires for multi-line ones.
func writeKeyValue(w io.Writer, name, value string) error {
	if !strings.ContainsAny(value, "\r\n") {
		_, err := fmt.Fprintf(w, "%s=%s\n", name, value)
		return err
	}

	delimiter, err := randomDelimiter()
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s<<%s\n%s\n%s\n", name, delimiter, value, delimiter)
	return err
}

func randomDelimiter() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate output delimiter: %w", err)
	}
	return "ghadelimiter_" + hex.EncodeToString(buf), nil
}
