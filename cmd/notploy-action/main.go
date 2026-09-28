// Command notploy-action is the entry point of the Notploy GitHub Action.
//
// It only wires packages together: read the action inputs, build a Notploy
// client, run the deployment lifecycle and publish the results back to the
// runner. All behaviour lives in internal/.
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/skygenesisenterprise/notploy-actions/internal/config"
	"github.com/skygenesisenterprise/notploy-actions/internal/deployment"
	"github.com/skygenesisenterprise/notploy-actions/internal/github"
	"github.com/skygenesisenterprise/notploy-actions/internal/notploy"
	"github.com/skygenesisenterprise/notploy-actions/internal/version"
)

func main() {
	os.Exit(run())
}

func run() int {
	getenv := os.Getenv
	log := github.NewLogger(os.Stdout)

	cfg, err := config.Load(getenv)
	if err != nil {
		log.Errorf("Invalid configuration: %v", err)
		return 1
	}
	// Register the secret with the runner before anything else can print it.
	log.Mask(cfg.APIKey)

	gh := github.LoadContext(getenv)
	if gh.Repository == "" {
		log.Warningf("GITHUB_REPOSITORY is not set; is this running inside GitHub Actions?")
	}

	client, err := notploy.NewClient(notploy.Options{
		Endpoint:  cfg.Endpoint,
		APIKey:    cfg.APIKey,
		UserAgent: "notploy-action/" + version.Version,
	})
	if err != nil {
		log.Errorf("Invalid Notploy endpoint: %v", err)
		return 1
	}

	// Cancel the wait cleanly when the job is cancelled.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	reportHeader(log, gh, cfg)

	service := deployment.New(client, log)
	result, runErr := service.Run(ctx, deployment.Params{
		ApplicationID: cfg.ApplicationID,
		Title:         gh.DeploymentTitle(),
		Description:   gh.DeploymentDescription(),
		Wait:          cfg.Wait,
		Timeout:       cfg.Timeout,
		PollInterval:  cfg.PollInterval,
	})

	if result != nil {
		if err := github.WriteOutputs(getenv, outputs(result)); err != nil {
			log.Warningf("Could not write the step outputs: %v", err)
		}
		if err := github.WriteSummary(getenv, summary(gh, result)); err != nil {
			log.Warningf("Could not write the job summary: %v", err)
		}
	}

	if runErr != nil {
		log.Errorf("Notploy deployment failed: %v", runErr)
		reportHint(log, runErr)
		return 1
	}

	reportSuccess(log, result)
	return 0
}

func reportHeader(log *github.Logger, gh github.Context, cfg *config.Config) {
	log.Infof("Notploy")
	log.Infof("%s", strings.Repeat("-", 28))
	log.Infof("Endpoint:    %s", cfg.Endpoint)
	log.Infof("Application: %s", cfg.ApplicationID)
	if gh.Repository != "" {
		log.Infof("Repository:  %s", gh.Repository)
	}
	if gh.ShortSha() != "" {
		log.Infof("Commit:      %s", gh.ShortSha())
	}
	if gh.Ref != "" {
		log.Infof("Ref:         %s", gh.Ref)
	}
	if gh.Actor != "" {
		log.Infof("Actor:       %s", gh.Actor)
	}
	if gh.EventName != "" {
		log.Infof("Event:       %s", gh.EventName)
	}
	if cfg.Wait {
		log.Infof("Timeout:     %s", cfg.Timeout)
		log.Infof("Poll:        %s", cfg.PollInterval)
	} else {
		log.Infof("Wait:        disabled")
	}
	log.Infof("")
}

func outputs(result *deployment.Result) map[string]string {
	return map[string]string{
		"deployment-id":     result.DeploymentID,
		"deployment-status": result.Status,
		"deployment-url":    result.URL,
	}
}

func summary(gh github.Context, result *deployment.Result) string {
	return github.Summary{
		Application:  result.ApplicationID,
		Repository:   gh.Repository,
		Commit:       gh.Sha,
		CommitURL:    gh.CommitURL(),
		Ref:          gh.Ref,
		Actor:        gh.Actor,
		Workflow:     gh.Workflow,
		RunNumber:    gh.RunNumber,
		DeploymentID: result.DeploymentID,
		Status:       result.Status,
		URL:          result.URL,
		Duration:     result.Duration,
		Error:        result.Error,
	}.Markdown()
}

func reportSuccess(log *github.Logger, result *deployment.Result) {
	if result == nil {
		return
	}

	if result.DeploymentID != "" {
		log.Infof("Deployment ID: %s", result.DeploymentID)
	}
	log.Infof("Status: %s", result.Status)
	if result.URL != "" {
		log.Infof("URL: %s", result.URL)
	}
	if result.Duration > 0 {
		log.Infof("Duration: %s", result.Duration.Round(time.Second))
	}

	switch result.Status {
	case notploy.StatusDone:
		log.Infof("✓ Deployment successful")
	case notploy.StatusCancelled:
		log.Warningf("Deployment was cancelled")
	case deployment.StatusTriggered:
		log.Infof("🚀 Deployment triggered (not waiting for it to finish)")
	}
}

func reportHint(log *github.Logger, err error) {
	var apiErr *notploy.APIError
	if !errors.As(err, &apiErr) {
		return
	}
	if hint := apiErr.Hint(); hint != "" {
		log.Infof("Hint: %s", hint)
	}
}
