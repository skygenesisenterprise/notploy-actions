package notploy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := NewClient(Options{Endpoint: server.URL, APIKey: "npk_test_key"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func writeJSON(t *testing.T, status int, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestEndpointNormalization(t *testing.T) {
	tests := map[string]struct {
		endpoint string
		want     string
		wantErr  bool
	}{
		"origin":              {endpoint: "https://notploy.example.com", want: "https://notploy.example.com/api"},
		"trailing slash":      {endpoint: "https://notploy.example.com/", want: "https://notploy.example.com/api"},
		"with api suffix":     {endpoint: "https://notploy.example.com/api", want: "https://notploy.example.com/api"},
		"api suffix + slash":  {endpoint: "https://notploy.example.com/api/", want: "https://notploy.example.com/api"},
		"with base path":      {endpoint: "https://example.com/notploy", want: "https://example.com/notploy/api"},
		"local http":          {endpoint: "http://localhost:3000", want: "http://localhost:3000/api"},
		"empty":               {endpoint: "", wantErr: true},
		"unsupported scheme":  {endpoint: "ftp://notploy.example.com", wantErr: true},
		"missing host":        {endpoint: "https:///api", wantErr: true},
		"embedded credential": {endpoint: "https://user:pass@notploy.example.com", wantErr: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			client, err := NewClient(Options{Endpoint: test.endpoint})
			if test.wantErr {
				if err == nil {
					t.Fatalf("NewClient(%q) succeeded, want an error", test.endpoint)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewClient(%q) error = %v", test.endpoint, err)
			}
			if got := client.BaseURL(); got != test.want {
				t.Fatalf("BaseURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTriggerDeployment(t *testing.T) {
	var (
		method string
		path   string
		apiKey string
		body   map[string]string
	)

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		apiKey = r.Header.Get("x-api-key")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		// `application.deploy` answers 200 with an empty body.
		_, _ = io.WriteString(w, "{}")
	})

	err := client.TriggerDeployment(context.Background(), "application-1", "Deploy #42", "event push")
	if err != nil {
		t.Fatalf("TriggerDeployment() error = %v", err)
	}

	if method != http.MethodPost {
		t.Errorf("method = %q", method)
	}
	if path != "/api/application.deploy" {
		t.Errorf("path = %q", path)
	}
	if apiKey != "npk_test_key" {
		t.Errorf("x-api-key = %q", apiKey)
	}
	if body["applicationId"] != "application-1" || body["title"] != "Deploy #42" || body["description"] != "event push" {
		t.Errorf("body = %v", body)
	}
}

func TestTriggerDeploymentAcceptsAnEmptyBody(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := client.TriggerDeployment(context.Background(), "application-1", "", ""); err != nil {
		t.Fatalf("TriggerDeployment() error = %v", err)
	}
}

func TestListDeployments(t *testing.T) {
	var (
		query string
		path  string
	)

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"deploymentId":"d2","title":"second","status":"running","applicationId":"application-1","createdAt":"2026-09-28T10:00:00.000Z"},
			{"deploymentId":"d1","title":"first","status":"done","applicationId":"application-1","createdAt":"2026-09-27T10:00:00.000Z","finishedAt":"2026-09-27T10:05:00.000Z"}
		]`)
	})

	deployments, err := client.ListDeployments(context.Background(), "application-1")
	if err != nil {
		t.Fatalf("ListDeployments() error = %v", err)
	}
	if query != "applicationId=application-1" {
		t.Errorf("query = %q", query)
	}
	if path != "/api/deployment.all" {
		t.Errorf("path = %q", path)
	}
	if len(deployments) != 2 {
		t.Fatalf("got %d deployments", len(deployments))
	}
	if deployments[0].DeploymentID != "d2" || deployments[0].Status != StatusRunning {
		t.Errorf("first deployment = %+v", deployments[0])
	}
	if deployments[1].Status != StatusDone || deployments[1].FinishedAt == nil {
		t.Errorf("second deployment = %+v", deployments[1])
	}
	if deployments[0].IsTerminal() || !deployments[1].IsTerminal() {
		t.Error("IsTerminal() reported the wrong states")
	}
}

func TestListDeploymentsToleratesAnEmptyList(t *testing.T) {
	client := newTestClient(t, writeJSON(t, http.StatusOK, "[]"))

	deployments, err := client.ListDeployments(context.Background(), "application-1")
	if err != nil {
		t.Fatalf("ListDeployments() error = %v", err)
	}
	if deployments == nil || len(deployments) != 0 {
		t.Fatalf("deployments = %#v", deployments)
	}
}

func TestGetApplicationAndPublicURL(t *testing.T) {
	var path string

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
		"applicationId":"application-1",
		"name":"my-app",
		"domains":[
			{"host":"insecure.example.com","https":false,"enabled":true},
			{"host":"secure.example.com","https":true,"enabled":true},
			{"host":"disabled.example.com","https":true,"enabled":false}
		]
	}`)
	})

	application, err := client.GetApplication(context.Background(), "application-1")
	if err != nil {
		t.Fatalf("GetApplication() error = %v", err)
	}
	if path != "/api/application.one" {
		t.Errorf("path = %q", path)
	}
	if application.DisplayName() != "my-app" {
		t.Errorf("DisplayName() = %q", application.DisplayName())
	}
	if got := application.PublicURL(); got != "https://secure.example.com" {
		t.Errorf("PublicURL() = %q, want the https domain", got)
	}
}

func TestPublicURLFallsBackToHTTPAndHandlesMissingDomains(t *testing.T) {
	if got := (Application{}).PublicURL(); got != "" {
		t.Errorf("PublicURL() = %q, want an empty string", got)
	}
	if got := (Application{Domains: []Domain{{Host: "plain.example.com"}}}).PublicURL(); got != "http://plain.example.com" {
		t.Errorf("PublicURL() = %q", got)
	}
	if got := (Application{Domains: []Domain{{Host: "app.example.com", HTTPS: true, Path: "/app"}}}).PublicURL(); got != "https://app.example.com/app" {
		t.Errorf("PublicURL() = %q", got)
	}
}

func TestHTTPStatusErrors(t *testing.T) {
	tests := map[string]struct {
		status    int
		body      string
		code      string
		message   string
		retryable bool
	}{
		"400": {status: 400, body: `{"message":"Invalid input data","code":"BAD_REQUEST"}`, code: "BAD_REQUEST", message: "Invalid input data"},
		"401": {status: 401, body: `{"message":"Authorization not provided","code":"UNAUTHORIZED"}`, code: "UNAUTHORIZED", message: "Authorization not provided"},
		"403": {status: 403, body: `{"message":"Insufficient access","code":"FORBIDDEN"}`, code: "FORBIDDEN", message: "Insufficient access"},
		"404": {status: 404, body: `{"message":"Application not found","code":"NOT_FOUND"}`, code: "NOT_FOUND", message: "Application not found"},
		"409": {status: 409, body: `{"message":"Conflict","code":"CONFLICT"}`, code: "CONFLICT", message: "Conflict"},
		"429": {status: 429, body: `{"message":"Too many requests","code":"TOO_MANY_REQUESTS"}`, code: "TOO_MANY_REQUESTS", message: "Too many requests", retryable: true},
		"500": {status: 500, body: `{"message":"Internal server error","code":"INTERNAL_SERVER_ERROR"}`, code: "INTERNAL_SERVER_ERROR", message: "Internal server error", retryable: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, writeJSON(t, test.status, test.body))

			// Retryable statuses would back off for up to 1.5s; a short context
			// keeps the test fast without changing what is reported.
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			_, err := client.ListDeployments(ctx, "application-1")
			if err == nil {
				t.Fatal("ListDeployments() succeeded, want an error")
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error type = %T (%v), want *APIError", err, err)
			}
			if apiErr.StatusCode != test.status {
				t.Errorf("StatusCode = %d", apiErr.StatusCode)
			}
			if apiErr.Code != test.code {
				t.Errorf("Code = %q", apiErr.Code)
			}
			if apiErr.Message != test.message {
				t.Errorf("Message = %q", apiErr.Message)
			}
			if apiErr.IsRetryable() != test.retryable {
				t.Errorf("IsRetryable() = %v, want %v", apiErr.IsRetryable(), test.retryable)
			}
			if apiErr.Hint() == "" {
				t.Errorf("Hint() is empty for HTTP %d", test.status)
			}
			if !strings.Contains(err.Error(), "deployment.all") {
				t.Errorf("error %q does not name the operation", err)
			}
		})
	}
}

func TestErrorUsesTheFirstIssueWhenNoMessageIsGiven(t *testing.T) {
	client := newTestClient(t, writeJSON(t, http.StatusBadRequest, `{"code":"BAD_REQUEST","issues":[{"message":"applicationId is required"}]}`))

	_, err := client.ListDeployments(context.Background(), "application-1")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T", err)
	}
	if apiErr.Message != "applicationId is required" {
		t.Fatalf("Message = %q", apiErr.Message)
	}
}

func TestErrorFallsBackToTheBodySnippet(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>\n  <body>upstream down</body>\n</html>")
	})

	_, err := client.ListDeployments(context.Background(), "application-1")

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T", err)
	}
	if !strings.Contains(apiErr.Message, "upstream down") {
		t.Fatalf("Message = %q", apiErr.Message)
	}
	if strings.Contains(apiErr.Message, "\n") {
		t.Fatalf("Message kept a newline: %q", apiErr.Message)
	}
}

func TestErrorNeverEchoesRequestHeaders(t *testing.T) {
	client := newTestClient(t, writeJSON(t, http.StatusUnauthorized, `{"message":"Authorization not provided","code":"UNAUTHORIZED"}`))

	err := client.TriggerDeployment(context.Background(), "application-1", "", "")
	if err == nil {
		t.Fatal("TriggerDeployment() succeeded, want an error")
	}
	if strings.Contains(err.Error(), "x-api-key") {
		t.Fatalf("error leaked a header name: %q", err)
	}
	if strings.Contains(err.Error(), "npk_test_key") {
		t.Fatalf("error leaked the API key: %q", err)
	}
}

func TestErrorRedactsTheAPIKeyFromAPublicMessage(t *testing.T) {
	client := newTestClient(t, writeJSON(t, http.StatusUnauthorized, `{"message":"key npk_test_key is not allowed","code":"UNAUTHORIZED"}`))

	_, err := client.ListDeployments(context.Background(), "application-1")
	if err == nil {
		t.Fatal("ListDeployments() succeeded, want an error")
	}
	if strings.Contains(err.Error(), "npk_test_key") {
		t.Fatalf("error leaked the API key: %q", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error did not redact: %q", err)
	}
}

func TestErrorBodySnippetRedactsTheAPIKey(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "used credential npk_test_key somewhere")
	})

	_, err := client.ListDeployments(context.Background(), "application-1")
	if err == nil {
		t.Fatal("ListDeployments() succeeded, want an error")
	}
	if strings.Contains(err.Error(), "npk_test_key") {
		t.Fatalf("error leaked the API key: %q", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("error did not redact: %q", err)
	}
}

func TestInvalidJSONIsReported(t *testing.T) {
	client := newTestClient(t, writeJSON(t, http.StatusOK, "this is not json"))

	_, err := client.ListDeployments(context.Background(), "application-1")
	if err == nil {
		t.Fatal("ListDeployments() succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Fatalf("error = %q, want a decode error", err)
	}
}

func TestNetworkTimeout(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(250 * time.Millisecond)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := client.ListDeployments(ctx, "application-1")
	if err == nil {
		t.Fatal("ListDeployments() succeeded, want a timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestUnreachableInstanceIsReported(t *testing.T) {
	// Bind a server and close it immediately to get a free, dead address.
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL
	server.Close()

	client, err := NewClient(Options{Endpoint: endpoint, APIKey: "npk_test_key"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err = client.ListDeployments(ctx, "application-1")
	if err == nil {
		t.Fatal("ListDeployments() succeeded, want a connection error")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error = %q", err)
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"message":"try again"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "[]")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.ListDeployments(ctx, "application-1"); err != nil {
		t.Fatalf("ListDeployments() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestDoesNotRetryTheTrigger(t *testing.T) {
	var calls int
	client := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"message":"try again"}`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.TriggerDeployment(ctx, "application-1", "", ""); err == nil {
		t.Fatal("TriggerDeployment() succeeded, want an error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1: a retried trigger would deploy twice", calls)
	}
}

func TestUserAgentAndDefaultTimeout(t *testing.T) {
	var userAgent string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "[]")
	})

	if _, err := client.ListDeployments(context.Background(), "application-1"); err != nil {
		t.Fatalf("ListDeployments() error = %v", err)
	}
	if userAgent != DefaultUserAgent {
		t.Fatalf("User-Agent = %q", userAgent)
	}
}

func TestCustomUserAgent(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	client, err := NewClient(Options{Endpoint: server.URL, UserAgent: "notploy-action/1.0.0"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.userAgent != "notploy-action/1.0.0" {
		t.Fatalf("userAgent = %q", client.userAgent)
	}
}
