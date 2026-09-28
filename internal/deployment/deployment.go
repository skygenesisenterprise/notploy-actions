// Package deployment implements the Notploy deployment lifecycle the action
// exposes: trigger, optionally wait for the result, report.
//
// Nothing here reads environment variables or knows about GitHub Actions. The
// caller supplies every parameter and the logger, which is what keeps the
// lifecycle testable and reusable outside of a runner.
package deployment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/skygenesisenterprise/notploy-actions/internal/notploy"
)

const (
	// StatusTriggered is reported when `wait` is disabled and the created
	// deployment has not been observed yet.
	StatusTriggered = "triggered"

	// defaultPollInterval is a safety net for callers that pass no interval.
	defaultPollInterval = 5 * time.Second
	// maxConsecutiveErrors is how many broken status lookups in a row are
	// tolerated before the wait is abandoned.
	maxConsecutiveErrors = 5
	// urlLookupTimeout bounds the best-effort call that resolves the
	// application's public URL.
	urlLookupTimeout = 10 * time.Second
)

// Client is the slice of the Notploy client the service needs.
type Client interface {
	TriggerDeployment(ctx context.Context, applicationID, title, description string) error
	ListDeployments(ctx context.Context, applicationID string) ([]notploy.Deployment, error)
	GetApplication(ctx context.Context, applicationID string) (*notploy.Application, error)
}

// Logger is what the service reports through. `github.Logger` implements it.
type Logger interface {
	Infof(format string, args ...any)
	Warningf(format string, args ...any)
}

// Params describes one deployment request.
type Params struct {
	ApplicationID string
	Title         string
	Description   string
	// Wait blocks until the deployment reaches a final state.
	Wait bool
	// Timeout bounds the total wait, including the trigger request.
	Timeout time.Duration
	// PollInterval is the delay between two status lookups.
	PollInterval time.Duration
}

// Result is what the caller publishes as action outputs.
type Result struct {
	ApplicationID string
	DeploymentID  string
	Status        string
	URL           string
	Duration      time.Duration
	Error         string
}

// Service triggers and follows deployments.
type Service struct {
	client Client
	log    Logger
}

// New returns a service. A nil logger is replaced by a no-op one.
func New(client Client, log Logger) *Service {
	if log == nil {
		log = discardLogger{}
	}
	return &Service{client: client, log: log}
}

// Run triggers a deployment and, when Params.Wait is set, follows it to a final
// state.
//
// The returned Result is non-nil as soon as a deployment was triggered, even
// when the returned error is non-nil, so the caller can still publish the
// deployment id and status it managed to observe.
func (s *Service) Run(ctx context.Context, params Params) (*Result, error) {
	if params.PollInterval <= 0 {
		params.PollInterval = defaultPollInterval
	}

	runCtx := ctx
	if params.Wait {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, params.Timeout)
		defer cancel()
	}

	started := time.Now()

	known, err := s.snapshot(runCtx, params.ApplicationID)
	if err != nil {
		if params.Wait {
			return nil, fmt.Errorf("list existing deployments: %w", err)
		}
		s.log.Warningf("Could not list existing deployments, the deployment id will not be reported: %v", err)
		known = nil
	}

	s.log.Infof("Triggering deployment for application %s", params.ApplicationID)
	if err := s.client.TriggerDeployment(runCtx, params.ApplicationID, params.Title, params.Description); err != nil {
		return nil, fmt.Errorf("trigger deployment: %w", err)
	}

	result := &Result{ApplicationID: params.ApplicationID, Status: StatusTriggered}

	if !params.Wait {
		// One best-effort lookup only: this call must not block the workflow.
		if known != nil {
			if deployment, err := s.findNewDeployment(runCtx, params.ApplicationID, known); err == nil && deployment != nil {
				applyDeployment(result, deployment)
			}
		}
		result.Duration = time.Since(started)
		result.URL = s.publicURL(ctx, params.ApplicationID)
		return result, nil
	}

	deployment, waitErr := s.wait(runCtx, params.ApplicationID, known, params.PollInterval)
	if deployment != nil {
		applyDeployment(result, deployment)
	}
	result.Duration = time.Since(started)
	// Resolve the URL with the caller's context: runCtx may already be expired.
	result.URL = s.publicURL(ctx, params.ApplicationID)

	switch {
	case waitErr == nil:
		if deployment.Failed() {
			result.Error = strings.TrimSpace(deref(deployment.ErrorMessage))
			return result, fmt.Errorf("deployment %s failed", deployment.DeploymentID)
		}
		return result, nil
	case errors.Is(waitErr, context.DeadlineExceeded):
		return result, fmt.Errorf("deployment did not reach a final state within %s", params.Timeout)
	case errors.Is(waitErr, context.Canceled):
		return result, fmt.Errorf("deployment wait was cancelled: %w", waitErr)
	default:
		return result, waitErr
	}
}

// snapshot records the deployments that already exist, so the one created by
// this run can be told apart from its predecessors and from deployments started
// by someone else.
func (s *Service) snapshot(ctx context.Context, applicationID string) (map[string]struct{}, error) {
	deployments, err := s.client.ListDeployments(ctx, applicationID)
	if err != nil {
		return nil, err
	}

	known := make(map[string]struct{}, len(deployments))
	for _, deployment := range deployments {
		known[deployment.DeploymentID] = struct{}{}
	}
	return known, nil
}

// findNewDeployment returns the newest deployment that is not in the snapshot,
// or nil while the deployment row does not exist yet.
func (s *Service) findNewDeployment(ctx context.Context, applicationID string, known map[string]struct{}) (*notploy.Deployment, error) {
	deployments, err := s.client.ListDeployments(ctx, applicationID)
	if err != nil {
		return nil, err
	}

	// `deployment.all` is ordered newest first.
	for i := range deployments {
		if _, seen := known[deployments[i].DeploymentID]; !seen {
			return &deployments[i], nil
		}
	}
	return nil, nil
}

// wait polls until the deployment is terminal, the context is done or the
// instance stops answering.
func (s *Service) wait(ctx context.Context, applicationID string, known map[string]struct{}, interval time.Duration) (*notploy.Deployment, error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var (
		latest         *notploy.Deployment
		consecutiveErr int
	)

	for {
		deployment, err := s.findNewDeployment(ctx, applicationID, known)
		switch {
		case err != nil:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return latest, ctxErr
			}
			consecutiveErr++
			s.log.Warningf("Could not read the deployment status (attempt %d/%d): %v", consecutiveErr, maxConsecutiveErrors, err)
			if consecutiveErr >= maxConsecutiveErrors {
				return latest, fmt.Errorf("gave up after %d failed status lookups: %w", consecutiveErr, err)
			}
		case deployment == nil:
			consecutiveErr = 0
		default:
			consecutiveErr = 0
			s.reportProgress(latest, deployment)
			latest = deployment
			if deployment.IsTerminal() {
				return deployment, nil
			}
		}

		select {
		case <-ctx.Done():
			return latest, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) reportProgress(previous, current *notploy.Deployment) {
	switch {
	case previous == nil || previous.DeploymentID != current.DeploymentID:
		s.log.Infof("Deployment %s is %s", current.DeploymentID, current.Status)
	case previous.Status != current.Status:
		s.log.Infof("Deployment %s is now %s", current.DeploymentID, current.Status)
	}
}

// publicURL resolves the application's public URL. It is best effort: a missing
// domain or an instance that hides the endpoint must not fail the deployment.
func (s *Service) publicURL(ctx context.Context, applicationID string) string {
	lookupCtx, cancel := context.WithTimeout(ctx, urlLookupTimeout)
	defer cancel()

	application, err := s.client.GetApplication(lookupCtx, applicationID)
	if err != nil {
		s.log.Warningf("Could not resolve the application URL: %v", err)
		return ""
	}
	return application.PublicURL()
}

func applyDeployment(result *Result, deployment *notploy.Deployment) {
	result.DeploymentID = deployment.DeploymentID
	if deployment.Status != "" {
		result.Status = deployment.Status
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// discardLogger is used when the caller provides no logger.
type discardLogger struct{}

func (discardLogger) Infof(string, ...any)    {}
func (discardLogger) Warningf(string, ...any) {}
