// Package notploy is a small, dependency-free HTTP client for the Notploy REST
// API.
//
// Notploy exposes its tRPC routers over HTTP as `POST|GET /api/<router>.<procedure>`
// and authenticates with an `x-api-key` header. The OpenAPI document declares
// most responses as empty objects, so only the fields the action actually needs
// are modelled here — deliberately not a copy of the TypeScript schemas.
//
// This package knows nothing about GitHub Actions.
package notploy

import "strings"

// Deployment statuses, mirroring the `deploymentStatus` enum of the Notploy
// database schema. There is no separate `queued` state: a deployment row is
// created with the status `running`.
const (
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusError     = "error"
	StatusCancelled = "cancelled"
)

// Deployment is one deployment record, as returned by `deployment.all`.
type Deployment struct {
	DeploymentID  string  `json:"deploymentId"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Status        string  `json:"status"`
	ApplicationID *string `json:"applicationId"`
	CreatedAt     string  `json:"createdAt"`
	StartedAt     *string `json:"startedAt"`
	FinishedAt    *string `json:"finishedAt"`
	ErrorMessage  *string `json:"errorMessage"`
}

// IsTerminal reports whether the deployment reached a final state.
func (d Deployment) IsTerminal() bool {
	switch d.Status {
	case StatusDone, StatusError, StatusCancelled:
		return true
	default:
		return false
	}
}

// Failed reports whether the deployment ended unsuccessfully.
func (d Deployment) Failed() bool {
	return d.Status == StatusError
}

// Domain is a domain attached to an application (`application.one`).
type Domain struct {
	Host    string `json:"host"`
	HTTPS   bool   `json:"https"`
	Port    int    `json:"port"`
	Path    string `json:"path"`
	Enabled *bool  `json:"enabled"`
}

// IsEnabled reports whether the domain is currently served. Instances that do
// not report the flag at all are treated as enabled.
func (d Domain) IsEnabled() bool {
	return d.Enabled == nil || *d.Enabled
}

// URL renders the domain as a browsable URL, or "" when it has no host.
func (d Domain) URL() string {
	host := strings.TrimSpace(d.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if d.HTTPS {
		scheme = "https"
	}
	if path := strings.Trim(strings.TrimSpace(d.Path), "/"); path != "" {
		return scheme + "://" + host + "/" + path
	}
	return scheme + "://" + host
}

// Application is the subset of `application.one` the action needs.
type Application struct {
	ApplicationID     string   `json:"applicationId"`
	Name              string   `json:"name"`
	AppName           string   `json:"appName"`
	ApplicationStatus string   `json:"applicationStatus"`
	Domains           []Domain `json:"domains"`
}

// PublicURL returns the URL of the first enabled domain, preferring HTTPS.
//
// Notploy does not expose a per-deployment URL, so the application's public
// endpoint is the closest useful value. It is empty when the application has no
// enabled domain yet.
func (a Application) PublicURL() string {
	fallback := ""
	for _, domain := range a.Domains {
		if !domain.IsEnabled() {
			continue
		}
		url := domain.URL()
		if url == "" {
			continue
		}
		if domain.HTTPS {
			return url
		}
		if fallback == "" {
			fallback = url
		}
	}
	return fallback
}

// DisplayName returns the most useful human readable name for the application.
func (a Application) DisplayName() string {
	if a.Name != "" {
		return a.Name
	}
	return a.AppName
}
