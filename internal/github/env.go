// Package github encapsulates everything that is specific to the GitHub Actions
// runtime: the runner context, action inputs, step outputs and log commands.
//
// No other package in this module reads an environment variable, which keeps the
// Notploy client and the deployment logic usable outside of GitHub Actions.
package github

import "strings"

// Getenv reads an environment variable. It is expected to be os.Getenv in
// production and a map-backed lookup in tests.
type Getenv func(name string) string

// inputEnvPrefix is the prefix the runner adds to action inputs.
const inputEnvPrefix = "INPUT_"

// ReadInput returns the value of an action input by the name declared in
// `action.yml`.
//
// The runner upper-cases input names and replaces spaces with underscores; the
// handling of hyphens has varied between runner versions, so both the
// hyphenated and the underscored spelling are accepted. An input that is unset
// or explicitly empty reads as "".
func ReadInput(getenv Getenv, name string) string {
	for _, key := range inputKeys(name) {
		if value := getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func inputKeys(name string) []string {
	upper := strings.ToUpper(name)
	hyphenated := inputEnvPrefix + upper
	underscored := inputEnvPrefix + strings.ReplaceAll(upper, "-", "_")
	if hyphenated == underscored {
		return []string{hyphenated}
	}
	return []string{hyphenated, underscored}
}
