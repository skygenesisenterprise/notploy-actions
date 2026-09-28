package notploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds a single HTTP request when the caller's context has
	// no earlier deadline.
	DefaultTimeout = 60 * time.Second
	// DefaultUserAgent is used when the caller does not provide one.
	DefaultUserAgent = "notploy-action"

	// apiPath is appended to the instance endpoint when it is missing.
	apiPath = "/api"

	// maxErrorBodyBytes limits how much of a failed response is read.
	maxErrorBodyBytes = 8 << 10
	// maxResponseBodyBytes limits how much of a successful response is read.
	maxResponseBodyBytes = 8 << 20
	// errorSnippetRunes limits the length of an unrecognised error body.
	errorSnippetRunes = 200

	// transientAttempts is the number of tries for an idempotent request.
	transientAttempts = 3
)

// Options configures a Client.
type Options struct {
	// Endpoint is the instance origin, with or without a trailing `/api`.
	Endpoint string
	// APIKey is sent as the `x-api-key` header. It is never logged.
	APIKey string
	// UserAgent identifies the caller. Defaults to DefaultUserAgent.
	UserAgent string
	// HTTPClient overrides the default client. Its transport is used as-is, so
	// TLS verification stays enabled unless the caller explicitly changes it.
	HTTPClient *http.Client
}

// Client talks to one Notploy instance. It is safe for concurrent use.
type Client struct {
	baseURL    *url.URL
	apiKey     string
	userAgent  string
	httpClient *http.Client
}

// NewClient validates the endpoint and returns a client for it.
func NewClient(options Options) (*Client, error) {
	baseURL, err := normalizeEndpoint(options.Endpoint)
	if err != nil {
		return nil, err
	}

	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultTimeout}
	}

	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}

	return &Client{
		baseURL:    baseURL,
		apiKey:     options.APIKey,
		userAgent:  userAgent,
		httpClient: httpClient,
	}, nil
}

// BaseURL returns the normalised API base URL, e.g. `https://notploy.example.com/api`.
func (c *Client) BaseURL() string {
	return c.baseURL.String()
}

// normalizeEndpoint turns an instance origin into the API base URL. It rejects
// anything that is not a plain http(s) origin without embedded credentials.
func normalizeEndpoint(endpoint string) (*url.URL, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return nil, errors.New("endpoint must not be empty")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("endpoint %q is not a valid URL: %w", raw, err)
	}
	switch parsed.Scheme {
	case "https", "http":
	default:
		return nil, fmt.Errorf("endpoint %q must use http or https", raw)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("endpoint %q is missing a host", raw)
	}
	if parsed.User != nil {
		return nil, errors.New("endpoint must not contain credentials")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, apiPath) {
		parsed.Path += apiPath
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

// TriggerDeployment starts a deployment (`POST /api/application.deploy`).
//
// Notploy answers with an empty 200 body: the deployment id is not returned.
// Callers discover the created deployment through ListDeployments.
func (c *Client) TriggerDeployment(ctx context.Context, applicationID, title, description string) error {
	body := deployRequest{ApplicationID: applicationID}
	if title != "" {
		body.Title = title
	}
	if description != "" {
		body.Description = description
	}

	// Never retried: a duplicate trigger would start a second deployment.
	return c.do(ctx, http.MethodPost, "application.deploy", nil, body, nil)
}

// ListDeployments returns the deployments of an application, newest first
// (`GET /api/deployment.all?applicationId=...`).
func (c *Client) ListDeployments(ctx context.Context, applicationID string) ([]Deployment, error) {
	query := url.Values{"applicationId": {applicationID}}

	var deployments []Deployment
	if err := c.do(ctx, http.MethodGet, "deployment.all", query, nil, &deployments); err != nil {
		return nil, err
	}
	if deployments == nil {
		deployments = []Deployment{}
	}
	return deployments, nil
}

// GetApplication returns one application and its domains
// (`GET /api/application.one?applicationId=...`).
func (c *Client) GetApplication(ctx context.Context, applicationID string) (*Application, error) {
	query := url.Values{"applicationId": {applicationID}}

	var application Application
	if err := c.do(ctx, http.MethodGet, "application.one", query, nil, &application); err != nil {
		return nil, err
	}
	return &application, nil
}

type deployRequest struct {
	ApplicationID string `json:"applicationId"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
}

// do performs a request, retrying idempotent calls on transient failures.
func (c *Client) do(ctx context.Context, method, operation string, query url.Values, requestBody, responseBody any) error {
	attempts := 1
	if method == http.MethodGet {
		attempts = transientAttempts
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if err := sleep(ctx, retryDelay(attempt)); err != nil {
				// The context expired while backing off. The failure that caused
				// the backoff is more informative than the cancelled timer, so it
				// is the one reported; callers that care about cancellation still
				// get it from their own context.
				return lastErr
			}
		}

		err := c.attempt(ctx, method, operation, query, requestBody, responseBody)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == attempts || !isTransient(err) {
			return err
		}
	}
	return lastErr
}

// isTransient reports whether a failed request is worth repeating. Only
// transport level failures and retryable HTTP statuses qualify: a malformed
// response will never become valid by asking again.
func isTransient(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsRetryable()
	}
	// A connection reset, a DNS hiccup or a per-request timeout is reported as
	// a *url.Error (which satisfies net.Error) and may succeed on the next try.
	var netErr net.Error
	return errors.As(err, &netErr)
}

func retryDelay(attempt int) time.Duration {
	return time.Duration(attempt-1) * 500 * time.Millisecond
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) attempt(ctx context.Context, method, operation string, query url.Values, requestBody, responseBody any) error {
	requestURL := *c.baseURL
	// Operations are compile-time constants (`application.deploy`), so the path
	// is concatenated rather than escaped like user input would be.
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + "/" + operation
	requestURL.RawPath = ""
	if len(query) > 0 {
		requestURL.RawQuery = query.Encode()
	}

	var payload io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("%s: encode request body: %w", operation, err)
		}
		payload = bytes.NewReader(encoded)
	}

	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), payload)
	if err != nil {
		return fmt.Errorf("%s: build request: %w", operation, err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	// The credential only ever goes on the wire.
	request.Header.Set("x-api-key", c.apiKey)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: %w", operation, ctxErr)
		}
		return fmt.Errorf("%s: request to %s failed: %w", operation, requestURL.Host, err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode > 299 {
		return c.errorFromResponse(operation, response)
	}

	if responseBody == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBodyBytes))
		return nil
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBodyBytes))
	if err := decoder.Decode(responseBody); err != nil {
		// `application.deploy` answers 200 with an empty body.
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%s: decode Notploy response as JSON: %w", operation, err)
	}
	return nil
}

// errorFromResponse converts a failed response into an *APIError, preferring the
// public message Notploy provides.
func (c *Client) errorFromResponse(operation string, response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))

	apiErr := &APIError{Operation: operation, StatusCode: response.StatusCode}

	var parsed errorResponse
	if err := json.Unmarshal(body, &parsed); err == nil {
		apiErr.Code = strings.TrimSpace(parsed.Code)
		apiErr.Message = strings.TrimSpace(parsed.Message)
		if apiErr.Message == "" && len(parsed.Issues) > 0 {
			apiErr.Message = strings.TrimSpace(parsed.Issues[0].Message)
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = snippet(body)
	}
	// Defence in depth: should the instance ever echo the credential back, it
	// does not end up in the error the caller prints.
	apiErr.Message = c.redact(apiErr.Message)
	return apiErr
}

func (c *Client) redact(text string) string {
	if c.apiKey == "" {
		return text
	}
	return strings.ReplaceAll(text, c.apiKey, "[redacted]")
}

// snippet renders an unexpected error body as a short, single-line string.
func snippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")

	runes := []rune(text)
	if len(runes) > errorSnippetRunes {
		return string(runes[:errorSnippetRunes]) + "…"
	}
	return text
}
