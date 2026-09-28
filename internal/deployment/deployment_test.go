package deployment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/skygenesisenterprise/notploy-actions/internal/notploy"
)

type listResult struct {
	deployments []notploy.Deployment
	err         error
}

// fakeClient replays a scripted list of `deployment.all` responses. Once the
// script is exhausted the last entry is repeated, which is how a test expresses
// "and then it stays like that".
type fakeClient struct {
	lists          []listResult
	listCalls      int
	triggerErr     error
	triggerCalls   int
	lastTrigger    triggerCall
	application    *notploy.Application
	applicationErr error
}

type triggerCall struct {
	applicationID string
	title         string
	description   string
}

func (f *fakeClient) TriggerDeployment(_ context.Context, applicationID, title, description string) error {
	f.triggerCalls++
	f.lastTrigger = triggerCall{applicationID: applicationID, title: title, description: description}
	return f.triggerErr
}

func (f *fakeClient) ListDeployments(_ context.Context, _ string) ([]notploy.Deployment, error) {
	if len(f.lists) == 0 {
		return nil, nil
	}
	index := f.listCalls
	f.listCalls++
	if index >= len(f.lists) {
		index = len(f.lists) - 1
	}
	result := f.lists[index]
	return result.deployments, result.err
}

func (f *fakeClient) GetApplication(_ context.Context, _ string) (*notploy.Application, error) {
	if f.applicationErr != nil {
		return nil, f.applicationErr
	}
	if f.application == nil {
		return &notploy.Application{}, nil
	}
	return f.application, nil
}

func deployment(id, status string) notploy.Deployment {
	return notploy.Deployment{DeploymentID: id, Status: status, Title: "deploy"}
}

func TestRunWithoutWaitReturnsAfterTheTrigger(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusRunning), deployment("old", notploy.StatusDone)}},
		},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          false,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if client.triggerCalls != 1 {
		t.Fatalf("triggerCalls = %d", client.triggerCalls)
	}
	if result.DeploymentID != "new" {
		t.Errorf("DeploymentID = %q", result.DeploymentID)
	}
	if result.Status != notploy.StatusRunning {
		t.Errorf("Status = %q", result.Status)
	}
}

func TestRunWithoutWaitStillTriggersWhenTheSnapshotFails(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{{err: errors.New("deployment.all is not permitted")}},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          false,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if client.triggerCalls != 1 {
		t.Fatalf("triggerCalls = %d, want the deployment to be triggered anyway", client.triggerCalls)
	}
	if result.Status != StatusTriggered {
		t.Errorf("Status = %q, want %q", result.Status, StatusTriggered)
	}
	if result.DeploymentID != "" {
		t.Errorf("DeploymentID = %q, want empty", result.DeploymentID)
	}
}

func TestRunFailsWhenTheSnapshotFailsAndItMustWait(t *testing.T) {
	client := &fakeClient{lists: []listResult{{err: errors.New("deployment.all is not permitted")}}}

	_, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Second,
		PollInterval:  time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "list existing deployments") {
		t.Fatalf("error = %q", err)
	}
	if client.triggerCalls != 0 {
		t.Fatalf("triggerCalls = %d, want 0", client.triggerCalls)
	}
}

func TestRunWaitsForSuccess(t *testing.T) {
	old := deployment("old", notploy.StatusDone)
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{old}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusRunning), old}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusRunning), old}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusDone), old}},
		},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       2 * time.Second,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != notploy.StatusDone {
		t.Errorf("Status = %q", result.Status)
	}
	if result.DeploymentID != "new" {
		t.Errorf("DeploymentID = %q", result.DeploymentID)
	}
	if result.Duration <= 0 {
		t.Error("Duration was not measured")
	}
}

func TestRunFailsWhenTheDeploymentFails(t *testing.T) {
	message := "build failed: Dockerfile not found"
	failed := notploy.Deployment{
		DeploymentID: "new",
		Status:       notploy.StatusError,
		ErrorMessage: &message,
	}
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
			{deployments: []notploy.Deployment{failed}},
		},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Second,
		PollInterval:  time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error = %q", err)
	}
	if result == nil {
		t.Fatal("Run() returned no result on failure")
	}
	if result.Status != notploy.StatusError {
		t.Errorf("Status = %q", result.Status)
	}
	if result.Error != message {
		t.Errorf("Error = %q, want %q", result.Error, message)
	}
}

func TestRunSucceedsWhenTheDeploymentIsCancelled(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusCancelled)}},
		},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Second,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil for a cancelled deployment", err)
	}
	if result.Status != notploy.StatusCancelled {
		t.Errorf("Status = %q", result.Status)
	}
}

func TestRunTimesOut(t *testing.T) {
	old := deployment("old", notploy.StatusDone)
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{old}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusRunning), old}},
		},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       40 * time.Millisecond,
		PollInterval:  5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want a timeout")
	}
	if !strings.Contains(err.Error(), "did not reach a final state within") {
		t.Fatalf("error = %q", err)
	}
	// The caller still learns what the last observed state was.
	if result == nil || result.DeploymentID != "new" || result.Status != notploy.StatusRunning {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	old := deployment("old", notploy.StatusDone)
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{old}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusRunning), old}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	result, err := New(client, nil).Run(ctx, Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Minute,
		PollInterval:  5 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want a cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if result == nil {
		t.Fatal("Run() returned no result on cancellation")
	}
}

func TestRunReportsATriggerFailure(t *testing.T) {
	client := &fakeClient{triggerErr: errors.New("boom")}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Second,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "trigger deployment") {
		t.Fatalf("error = %q", err)
	}
	if result != nil {
		t.Fatalf("result = %+v, want nil", result)
	}
}

func TestRunPassesTheGitHubMetadataToNotploy(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusDone)}},
		},
	}

	if _, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Title:         "Deploy #42",
		Description:   "event push",
		Wait:          true,
		Timeout:       time.Second,
		PollInterval:  time.Millisecond,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if client.lastTrigger != (triggerCall{applicationID: "application-1", title: "Deploy #42", description: "event push"}) {
		t.Fatalf("trigger = %+v", client.lastTrigger)
	}
}

func TestRunToleratesTransientStatusLookupFailures(t *testing.T) {
	old := deployment("old", notploy.StatusDone)
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{old}},
			{err: errors.New("connection reset")},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusRunning), old}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusDone), old}},
		},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       2 * time.Second,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != notploy.StatusDone {
		t.Fatalf("Status = %q", result.Status)
	}
}

func TestRunGivesUpAfterRepeatedStatusLookupFailures(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
			{err: errors.New("connection reset")},
		},
	}

	_, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       5 * time.Second,
		PollInterval:  time.Millisecond,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "gave up") {
		t.Fatalf("error = %q", err)
	}
}

func TestRunResolvesTheApplicationURL(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusDone)}},
		},
		application: &notploy.Application{
			ApplicationID: "application-1",
			Name:          "my-app",
			Domains:       []notploy.Domain{{Host: "my-app.example.com", HTTPS: true, Enabled: boolPtr(true)}},
		},
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Second,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.URL != "https://my-app.example.com" {
		t.Fatalf("URL = %q", result.URL)
	}
}

func TestRunSurvivesAnUnresolvableApplicationURL(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
			{deployments: []notploy.Deployment{deployment("new", notploy.StatusDone)}},
		},
		applicationErr: errors.New("application.one is not permitted"),
	}

	result, err := New(client, nil).Run(context.Background(), Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Second,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want the URL lookup to be best effort", err)
	}
	if result.URL != "" {
		t.Fatalf("URL = %q, want empty", result.URL)
	}
}

func TestRunUsesADefaultPollInterval(t *testing.T) {
	client := &fakeClient{
		lists: []listResult{
			{deployments: []notploy.Deployment{deployment("old", notploy.StatusDone)}},
		},
	}

	// PollInterval is left at zero on purpose: the service must not spin.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	result, err := New(client, nil).Run(ctx, Params{
		ApplicationID: "application-1",
		Wait:          true,
		Timeout:       time.Minute,
	})
	if err == nil {
		t.Fatal("Run() succeeded, want the wait to be interrupted")
	}
	// A zero interval would have hammered the fake; a default one keeps the
	// call count low.
	if client.listCalls > 5 {
		t.Fatalf("listCalls = %d, want the default poll interval to be used", client.listCalls)
	}
	if result == nil {
		t.Fatal("Run() returned no result")
	}
}

func boolPtr(value bool) *bool {
	return &value
}
