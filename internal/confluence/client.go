// Package confluence implements a fixed-origin Confluence Cloud REST client.
//
// Scoped API tokens are sent only to api.atlassian.com. Redirects are refused,
// response bodies are bounded before decoding, and upstream bodies never enter
// user-facing errors.
package confluence

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/abigotado/confluence-cli/internal/atlassian"
	"github.com/abigotado/confluence-cli/internal/errx"
)

const (
	spacesPath = "/wiki/api/v2/spaces"
	pagesPath  = "/wiki/api/v2/pages"
	searchPath = "/wiki/rest/api/search"

	tenantInfoPath           = "/_edge/tenant_info"
	maxAttempts              = 3
	maxCompressedBody        = 4 << 20
	maxDecompressedBody      = 4 << 20
	maxDrainBody             = 64 << 10
	maxCursorBytes           = 4096
	defaultVerificationLimit = 1
)

// Credential is one scoped Confluence API credential. Formatting is always
// redacted so accidental diagnostics cannot reveal email or token material.
type Credential struct {
	Email   string
	Token   string `json:"-"`
	CloudID string
}

// Format redacts the complete credential for every fmt verb.
func (Credential) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<redacted>")
}

// LogValue prevents structured log handlers from reflecting credential fields.
func (Credential) LogValue() slog.Value { return slog.StringValue("<redacted>") }

// Client talks to Confluence through Atlassian's scoped-token gateway.
type Client struct {
	baseURL string
	cred    Credential
	http    *http.Client
	log     *slog.Logger
	sleep   func(context.Context, time.Duration) error
	now     func() time.Time
}

// Option customizes a Client without weakening endpoint validation.
type Option func(*Client)

// WithHTTPClient injects a transport while preserving redirect refusal and
// disabling cookie state.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		if httpClient == nil {
			return
		}
		clone := *httpClient
		clone.CheckRedirect = refuseRedirect
		clone.Jar = nil
		client.http = &clone
	}
}

// WithLogger installs a diagnostic logger. Request URLs, queries, bodies,
// credentials, and response bodies are never logged.
func WithLogger(logger *slog.Logger) Option {
	return func(client *Client) {
		if logger != nil {
			client.log = logger
		}
	}
}

// WithSleep replaces retry sleeping for deterministic tests.
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(client *Client) {
		if sleep != nil {
			client.sleep = sleep
		}
	}
}

// New constructs a scoped-token client with a fixed Atlassian origin.
func New(credential Credential, options ...Option) (*Client, error) {
	if err := atlassian.ValidateEmail(credential.Email); err != nil {
		return nil, errx.Usage("Atlassian account email is invalid")
	}
	if credential.Token == "" {
		return nil, errx.Auth("MISSING_TOKEN", "Confluence API token is missing")
	}
	if err := atlassian.ValidateCloudID(credential.CloudID); err != nil {
		return nil, errx.Usage("cloud ID is invalid")
	}
	client := &Client{
		baseURL: "https://api.atlassian.com/ex/confluence/" + url.PathEscape(credential.CloudID),
		cred:    credential,
		http: &http.Client{
			CheckRedirect: refuseRedirect,
		},
		log:   slog.New(discardHandler{}),
		sleep: sleepContext,
		now:   time.Now,
	}
	for _, option := range options {
		option(client)
	}
	return client, nil
}

type request struct {
	path      string
	query     url.Values
	operation string
}

func (client *Client) get(ctx context.Context, request request, out any) (http.Header, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, errx.Translate(err)
		}
		response, err := client.send(ctx, request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, errx.Translate(ctx.Err())
			}
			lastErr = errx.Retryable("NETWORK", 0, "could not reach Confluence")
			if attempt == maxAttempts {
				return nil, lastErr
			}
			if err := client.sleep(ctx, retryBackoff(attempt)); err != nil {
				return nil, errx.Translate(err)
			}
			continue
		}

		retryAfter, retry, translated := client.handle(response, request, out)
		if translated == nil {
			return response.Header.Clone(), nil
		}
		lastErr = translated
		if !retry || attempt == maxAttempts {
			return nil, translated
		}
		delay := retryAfter
		if delay <= 0 {
			delay = retryBackoff(attempt)
		}
		client.log.Debug("retrying Confluence request", "operation", request.operation, "attempt", attempt, "delay", delay)
		if err := client.sleep(ctx, delay); err != nil {
			return nil, errx.Translate(err)
		}
	}
	return nil, lastErr
}

func (client *Client) send(ctx context.Context, request request) (*http.Response, error) {
	fullURL := client.baseURL + request.path
	if len(request.query) > 0 {
		fullURL += "?" + request.query.Encode()
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, errors.New("invalid Confluence request")
	}
	httpRequest.Header.Set("Accept", "application/json")
	// Setting this explicitly keeps net/http from transparently decompressing;
	// handle can enforce independent compressed and decompressed size limits.
	httpRequest.Header.Set("Accept-Encoding", "gzip")
	// Basic auth is constructed in memory and is never logged or returned.
	httpRequest.SetBasicAuth(client.cred.Email, client.cred.Token)
	client.log.Debug("Confluence request", "operation", request.operation)
	return client.http.Do(httpRequest)
}

func (client *Client) handle(response *http.Response, request request, out any) (time.Duration, bool, error) {
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrainBody))
		_ = response.Body.Close()
	}()

	body, tooLarge, readErr := readResponseBody(response)
	if readErr != nil {
		return 0, false, errx.Internal("could not read Confluence response")
	}
	if tooLarge {
		return 0, false, errx.Internal("Confluence response exceeds the safety limit")
	}

	if response.StatusCode == http.StatusOK {
		if out == nil {
			return 0, false, nil
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return 0, false, invalidReadResponse()
		}
		if err := json.Unmarshal(body, out); err != nil {
			return 0, false, invalidReadResponse()
		}
		return 0, false, nil
	}
	return client.translateStatus(response, request)
}

func invalidReadResponse() error {
	return errx.Internal("Confluence returned an invalid read response")
}

func (client *Client) translateStatus(response *http.Response, request request) (time.Duration, bool, error) {
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return 0, false, errx.Auth("AUTHENTICATION_FAILED", "Confluence rejected the account credentials")
	case http.StatusForbidden:
		return 0, false, errx.Permission("SCOPE_OR_PERMISSION_DENIED", "the Confluence account lacks the required permission or API token scope")
	case http.StatusConflict:
		return 0, false, errx.Conflict("CONFLICT", "Confluence reported a resource-state conflict")
	case http.StatusNotFound:
		return 0, false, errx.NotFound(resourceKind(request.operation), "requested", nil)
	case http.StatusTooManyRequests:
		delay := parseRetryAfter(response.Header.Get("Retry-After"), client.now())
		return delay, true, errx.Retryable("RATE_LIMITED", delay, "Confluence rate limited the request")
	default:
		if response.StatusCode >= http.StatusInternalServerError {
			return 0, true, errx.Retryable("SERVER_ERROR", 0, "Confluence is temporarily unavailable")
		}
		return 0, false, errx.Internal("Confluence returned unexpected HTTP status %d", response.StatusCode)
	}
}

func readResponseBody(response *http.Response) ([]byte, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(response.Header.Get("Content-Encoding")), "gzip") {
		return readBounded(response.Body, maxDecompressedBody)
	}
	compressed := &boundedReader{reader: response.Body, remaining: maxCompressedBody + 1}
	reader, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, false, err
	}
	decompressed, tooLarge, readErr := readBounded(reader, maxDecompressedBody)
	closeErr := reader.Close()
	if compressed.exceeded {
		tooLarge = true
	}
	if readErr != nil && !compressed.exceeded {
		return nil, false, readErr
	}
	if compressed.exceeded {
		return nil, true, nil
	}
	if closeErr != nil {
		return nil, false, closeErr
	}
	return decompressed, tooLarge, nil
}

type boundedReader struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
}

func (reader *boundedReader) Read(buffer []byte) (int, error) {
	if reader.remaining <= 0 {
		reader.exceeded = true
		return 0, io.EOF
	}
	if int64(len(buffer)) > reader.remaining {
		buffer = buffer[:reader.remaining]
	}
	count, err := reader.reader.Read(buffer)
	reader.remaining -= int64(count)
	if reader.remaining == 0 && err == nil {
		reader.exceeded = true
	}
	return count, err
}

func readBounded(reader io.Reader, limit int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func cursorFromNext(nextURL, expectedPath, cloudID string) (string, error) {
	if strings.TrimSpace(nextURL) == "" {
		return "", nil
	}
	parsed, err := url.Parse(nextURL)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return "", errx.Internal("Confluence returned an invalid pagination link")
	}
	if (parsed.Scheme != "" || parsed.Host != "") && (parsed.Scheme != "https" || parsed.Host != "api.atlassian.com") {
		return "", errx.Internal("Confluence returned a cross-origin pagination link")
	}
	path := parsed.Path
	gatewayPrefix := "/ex/confluence/" + url.PathEscape(cloudID)
	path = strings.TrimPrefix(path, gatewayPrefix)
	pathOK := path == expectedPath
	if expectedPath == searchPath {
		pathOK = pathOK || path == strings.TrimPrefix(searchPath, "/wiki")
	}
	if !pathOK {
		return "", errx.Internal("Confluence returned pagination for an unexpected operation")
	}
	values := parsed.Query()["cursor"]
	if len(values) != 1 || !validCursor(values[0]) {
		return "", errx.Internal("Confluence returned an invalid pagination cursor")
	}
	return values[0], nil
}

func cursorFromLinkHeader(header, expectedPath, cloudID string) (string, error) {
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(part, ";")
		if len(segments) < 2 || !strings.Contains(strings.ToLower(strings.Join(segments[1:], ";")), "rel=\"next\"") {
			continue
		}
		candidate := strings.TrimSpace(segments[0])
		if len(candidate) < 3 || candidate[0] != '<' || candidate[len(candidate)-1] != '>' {
			return "", errx.Internal("Confluence returned an invalid Link header")
		}
		return cursorFromNext(candidate[1:len(candidate)-1], expectedPath, cloudID)
	}
	return "", nil
}

// ValidateCursor validates an opaque cursor before it becomes a query value.
func ValidateCursor(cursor string) error {
	if cursor == "" {
		return nil
	}
	if !validCursor(cursor) {
		return errx.Usage("cursor is invalid")
	}
	return nil
}

func validCursor(cursor string) bool {
	if cursor == "" || len(cursor) > maxCursorBytes || !utf8.ValidString(cursor) {
		return false
	}
	for _, character := range cursor {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func nextCursor(links Links, header http.Header, expectedPath, cloudID string) (string, error) {
	if links.Next != "" {
		return cursorFromNext(links.Next, expectedPath, cloudID)
	}
	return cursorFromLinkHeader(header.Get("Link"), expectedPath, cloudID)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func retryBackoff(attempt int) time.Duration {
	return time.Duration(attempt) * 250 * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func refuseRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

func resourceKind(operation string) string {
	if before, _, ok := strings.Cut(operation, "."); ok && before != "" {
		return strings.TrimSuffix(before, "s")
	}
	return "resource"
}

type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }
