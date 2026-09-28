package github

import (
	"fmt"
	"strings"
)

// Context is the subset of the GitHub Actions runtime context the action uses.
//
// It is read once from the environment so that no other package has to know
// about runner variables. Every field is optional: the action is expected to
// work from `push`, `workflow_dispatch`, `schedule`, `pull_request` and any
// other event, and even outside of GitHub Actions.
type Context struct {
	Repository string
	Ref        string
	RefName    string
	Sha        string
	Actor      string
	EventName  string
	RunID      string
	RunNumber  string
	ServerURL  string
	Workflow   string
}

// LoadContext reads the GitHub Actions context from the environment.
func LoadContext(getenv Getenv) Context {
	return Context{
		Repository: getenv("GITHUB_REPOSITORY"),
		Ref:        getenv("GITHUB_REF"),
		RefName:    getenv("GITHUB_REF_NAME"),
		Sha:        getenv("GITHUB_SHA"),
		Actor:      getenv("GITHUB_ACTOR"),
		EventName:  getenv("GITHUB_EVENT_NAME"),
		RunID:      getenv("GITHUB_RUN_ID"),
		RunNumber:  getenv("GITHUB_RUN_NUMBER"),
		ServerURL:  getenv("GITHUB_SERVER_URL"),
		Workflow:   getenv("GITHUB_WORKFLOW"),
	}
}

// ShortSha returns the abbreviated commit sha, or "" when it is unknown.
func (c Context) ShortSha() string {
	const short = 7
	if len(c.Sha) > short {
		return c.Sha[:short]
	}
	return c.Sha
}

// CommitURL returns a link to the commit, or "" when a variable is missing.
func (c Context) CommitURL() string {
	if c.ServerURL == "" || c.Repository == "" || c.Sha == "" {
		return ""
	}
	return fmt.Sprintf(
		"%s/%s/commit/%s",
		strings.TrimRight(c.ServerURL, "/"),
		c.Repository,
		c.Sha,
	)
}

// Revision describes the commit under test as `owner/repository@abc1234`.
func (c Context) Revision() string {
	switch {
	case c.Repository != "" && c.ShortSha() != "":
		return c.Repository + "@" + c.ShortSha()
	case c.Repository != "":
		return c.Repository
	default:
		return c.ShortSha()
	}
}

// DeploymentTitle is the human readable title sent to Notploy. It derives from
// public repository metadata only, so it can never contain a secret.
func (c Context) DeploymentTitle() string {
	parts := make([]string, 0, 3)
	if c.Workflow != "" {
		parts = append(parts, c.Workflow)
	}
	if c.RunNumber != "" {
		parts = append(parts, "#"+c.RunNumber)
	}
	if revision := c.Revision(); revision != "" {
		parts = append(parts, revision)
	}
	if len(parts) == 0 {
		return "GitHub Actions deployment"
	}
	return "GitHub Actions: " + strings.Join(parts, " ")
}

// DeploymentDescription explains where the deployment came from.
func (c Context) DeploymentDescription() string {
	parts := make([]string, 0, 4)
	if c.EventName != "" {
		parts = append(parts, "event "+c.EventName)
	}
	if c.Ref != "" {
		parts = append(parts, "ref "+c.Ref)
	}
	if c.Actor != "" {
		parts = append(parts, "by "+c.Actor)
	}
	if c.RunID != "" {
		parts = append(parts, "run "+c.RunID)
	}
	return strings.Join(parts, ", ")
}
