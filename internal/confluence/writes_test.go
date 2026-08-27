package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abigotado/confluence-cli/internal/errx"
)

func TestCreatePageUsesBoundedStoragePayload(t *testing.T) {
	tests := []struct {
		name      string
		parentID  string
		wantQuery string
	}{
		{name: "root page declares root level", wantQuery: "root-level=true"},
		{name: "child page sends exact parent", parentID: "7"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls++
				if request.Method != http.MethodPost || request.URL.Path != "/ex/confluence/cloud-id"+pagesPath || request.URL.RawQuery != test.wantQuery {
					t.Errorf("route = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				if request.ContentLength != int64(len(body)) || request.ContentLength <= 0 {
					t.Errorf("Content-Length = %d, body = %d", request.ContentLength, len(body))
				}
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if payload["spaceId"] != "42" || payload["status"] != "current" || payload["title"] != "Roadmap" {
					t.Error("create request JSON identity fields do not match")
				}
				if _, exists := payload["root-level"]; exists {
					t.Error("root-level leaked into the JSON body")
				}
				if test.parentID == "" {
					if _, exists := payload["parentId"]; exists {
						t.Error("root page payload contains parentId")
					}
				} else if payload["parentId"] != test.parentID {
					t.Errorf("parentId = %#v", payload["parentId"])
				}
				assertStorageBody(t, payload, "<p>body</p>")
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(writer, `{"id":"9","status":"current","title":"Roadmap","spaceId":"42","parentId":%q,"version":{"number":1}}`, test.parentID)
			}))
			defer server.Close()

			client := newTestClient(t, routedClient(t, server, assertOneShotWriteRequest(t)))
			page, err := client.CreatePage(context.Background(), CreatePageInput{
				SpaceID: "42", ParentID: test.parentID, Title: "Roadmap", Body: "<p>body</p>",
			})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || page.ID != "9" || page.Version.Number != 1 {
				t.Fatalf("calls=%d page=%#v", calls, page)
			}
		})
	}
}

func TestUpdatePageUsesVersionedIdentityBoundPayload(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.Method != http.MethodPut || request.URL.Path != "/ex/confluence/cloud-id"+pagesPath+"/9" || request.URL.RawQuery != "" {
			t.Errorf("route = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["id"] != "9" || payload["status"] != "current" || payload["title"] != "Updated" {
			t.Error("update request JSON identity fields do not match")
		}
		if _, exists := payload["spaceId"]; exists {
			t.Error("update payload contains spaceId")
		}
		if _, exists := payload["parentId"]; exists {
			t.Error("update payload contains parentId")
		}
		version, ok := payload["version"].(map[string]any)
		if !ok || version["number"] != float64(4) {
			t.Errorf("version payload = %#v", payload["version"])
		}
		assertStorageBody(t, payload, "<p>updated</p>")
		_, _ = io.WriteString(writer, `{"id":"9","status":"current","title":"Updated","spaceId":"42","parentId":"7","version":{"number":4}}`)
	}))
	defer server.Close()

	client := newTestClient(t, routedClient(t, server, assertOneShotWriteRequest(t)))
	page, err := client.UpdatePage(context.Background(), UpdatePageInput{
		PageID: "9", SpaceID: "42", ParentID: "7", ExpectedVersion: 3, Title: "Updated", Body: "<p>updated</p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || page.Version.Number != 4 {
		t.Fatalf("calls=%d page=%#v", calls, page)
	}
}

func TestPageWritesDispatchExactlyOnceAndAreNotReplayable(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{name: "create", call: func(client *Client) error {
			_, err := client.CreatePage(context.Background(), CreatePageInput{SpaceID: "42", Title: "Page", Body: "body"})
			return err
		}},
		{name: "update", call: func(client *Client) error {
			_, err := client.UpdatePage(context.Background(), UpdatePageInput{PageID: "9", SpaceID: "42", ExpectedVersion: 1, Title: "Page", Body: "body"})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			client := newTestClient(t, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				assertOneShotWriteRequest(t)(request)
				return nil, errors.New("transport sentinel " + testToken)
			})})
			err := test.call(client)
			assertTypedWriteError(t, err, errx.CodeConflict, "WRITE_OUTCOME_UNKNOWN")
			if calls != 1 || strings.Contains(err.Error(), testToken) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestPageWriteRedirectIsNotFollowed(t *testing.T) {
	var sourceCalls int
	var destinationCalls int
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		destinationCalls++
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		sourceCalls++
		http.Redirect(writer, request, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client := newTestClient(t, routedClient(t, source, nil))
	_, err := client.CreatePage(context.Background(), CreatePageInput{SpaceID: "42", Title: "Page", Body: "body"})
	assertTypedWriteError(t, err, errx.CodeConflict, "WRITE_OUTCOME_UNKNOWN")
	if sourceCalls != 1 || destinationCalls != 0 {
		t.Fatalf("source calls=%d destination calls=%d", sourceCalls, destinationCalls)
	}
}

func TestPageWriteDiagnosticsDoNotContainCredentialsOrContent(t *testing.T) {
	var logs bytes.Buffer
	client, err := New(
		Credential{Email: "user@example.com", Token: testToken, CloudID: "cloud-id"},
		WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport failed")
		})}),
		WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := client.CreatePage(context.Background(), CreatePageInput{
		SpaceID: "42", Title: "CONTENT_TITLE_SENTINEL", Body: "CONTENT_BODY_SENTINEL",
	})
	assertTypedWriteError(t, writeErr, errx.CodeConflict, "WRITE_OUTCOME_UNKNOWN")
	for _, forbidden := range []string{testToken, "user@example.com", "cloud-id", "CONTENT_TITLE_SENTINEL", "CONTENT_BODY_SENTINEL", "api.atlassian.com"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatal("write diagnostics contain credential, content, or request-target data")
		}
	}
}

func TestPageWriteStatusMatrix(t *testing.T) {
	tests := []struct {
		name       string
		create     bool
		status     int
		wantCode   errx.Code
		wantReason string
	}{
		{name: "create 400 is usage", create: true, status: http.StatusBadRequest, wantCode: errx.CodeUsage, wantReason: "USAGE"},
		{name: "update 400 is stale version", status: http.StatusBadRequest, wantCode: errx.CodeConflict, wantReason: "STALE_PAGE_VERSION"},
		{name: "401 is auth", status: http.StatusUnauthorized, wantCode: errx.CodeAuth, wantReason: "AUTHENTICATION_FAILED"},
		{name: "403 is permission", status: http.StatusForbidden, wantCode: errx.CodePermission, wantReason: "SCOPE_OR_PERMISSION_DENIED"},
		{name: "404 means target changed", status: http.StatusNotFound, wantCode: errx.CodeConflict, wantReason: "TARGET_CHANGED"},
		{name: "409 is conflict", status: http.StatusConflict, wantCode: errx.CodeConflict, wantReason: "CONFLICT"},
		{name: "413 is payload", status: http.StatusRequestEntityTooLarge, wantCode: errx.CodeUsage, wantReason: "PAYLOAD_TOO_LARGE"},
		{name: "429 is definite retryable", status: http.StatusTooManyRequests, wantCode: errx.CodeRetryable, wantReason: "RATE_LIMITED"},
		{name: "redirect is unknown", status: http.StatusTemporaryRedirect, wantCode: errx.CodeConflict, wantReason: "WRITE_OUTCOME_UNKNOWN"},
		{name: "unexpected success is unknown", status: http.StatusCreated, wantCode: errx.CodeConflict, wantReason: "WRITE_OUTCOME_UNKNOWN"},
		{name: "server failure is unknown", status: http.StatusServiceUnavailable, wantCode: errx.CodeConflict, wantReason: "WRITE_OUTCOME_UNKNOWN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				calls++
				if test.status == http.StatusTooManyRequests {
					writer.Header().Set("Retry-After", "3")
				}
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, "UPSTREAM_BODY_SENTINEL "+testToken)
			}))
			defer server.Close()
			client := newTestClient(t, routedClient(t, server, nil))
			var err error
			if test.create {
				_, err = client.CreatePage(context.Background(), CreatePageInput{SpaceID: "42", Title: "Page", Body: "body"})
			} else {
				_, err = client.UpdatePage(context.Background(), UpdatePageInput{PageID: "9", SpaceID: "42", ExpectedVersion: 1, Title: "Page", Body: "body"})
			}
			assertTypedWriteError(t, err, test.wantCode, test.wantReason)
			if calls != 1 || strings.Contains(err.Error(), "UPSTREAM_BODY_SENTINEL") || strings.Contains(err.Error(), testToken) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestPageWriteSuccessRequiresCompleteIdentity(t *testing.T) {
	valid := `{"id":"9","status":"current","title":"Page","spaceId":"42","version":{"number":2}}`
	tests := []struct {
		name string
		body io.ReadCloser
	}{
		{name: "empty body", body: io.NopCloser(strings.NewReader(""))},
		{name: "invalid JSON", body: io.NopCloser(strings.NewReader("{"))},
		{name: "oversized response", body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), maxDecompressedBody+1)))},
		{name: "read failure", body: io.NopCloser(errorReader{})},
		{name: "missing ID", body: io.NopCloser(strings.NewReader(strings.Replace(valid, `"id":"9",`, "", 1)))},
		{name: "non-numeric ID", body: io.NopCloser(strings.NewReader(strings.Replace(valid, `"id":"9"`, `"id":"x"`, 1)))},
		{name: "wrong space", body: io.NopCloser(strings.NewReader(strings.Replace(valid, `"spaceId":"42"`, `"spaceId":"7"`, 1)))},
		{name: "wrong title", body: io.NopCloser(strings.NewReader(strings.Replace(valid, `"title":"Page"`, `"title":"Other"`, 1)))},
		{name: "wrong status", body: io.NopCloser(strings.NewReader(strings.Replace(valid, `"status":"current"`, `"status":"draft"`, 1)))},
		{name: "wrong parent", body: io.NopCloser(strings.NewReader(strings.Replace(valid, `"version"`, `"parentId":"7","version"`, 1)))},
		{name: "wrong version", body: io.NopCloser(strings.NewReader(strings.Replace(valid, `"number":2`, `"number":3`, 1)))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			client := newTestClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: test.body}, nil
			})})
			_, err := client.UpdatePage(context.Background(), UpdatePageInput{
				PageID: "9", SpaceID: "42", ExpectedVersion: 1, Title: "Page", Body: "body",
			})
			assertTypedWriteError(t, err, errx.CodeConflict, "WRITE_OUTCOME_UNKNOWN")
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
		})
	}
}

func TestCreatePageSuccessIdentityRequiresParentAndPositiveVersion(t *testing.T) {
	tests := []string{
		`{"id":"9","status":"current","title":"Page","spaceId":"42","parentId":"8","version":{"number":1}}`,
		`{"id":"9","status":"current","title":"Page","spaceId":"42","parentId":"7","version":{"number":0}}`,
	}
	for _, body := range tests {
		client := newTestClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})})
		_, err := client.CreatePage(context.Background(), CreatePageInput{
			SpaceID: "42", ParentID: "7", Title: "Page", Body: "body",
		})
		assertTypedWriteError(t, err, errx.CodeConflict, "WRITE_OUTCOME_UNKNOWN")
	}
}

func TestPageWriteValidationIsLocalAndBounded(t *testing.T) {
	validCreate := CreatePageInput{SpaceID: "42", Title: "Page", Body: "body"}
	validUpdate := UpdatePageInput{PageID: "9", SpaceID: "42", ExpectedVersion: 1, Title: "Page", Body: "body"}
	tests := []struct {
		name string
		err  error
	}{
		{name: "create invalid space", err: ValidateCreatePageInput(CreatePageInput{SpaceID: "x", Title: "Page", Body: "body"})},
		{name: "create invalid parent", err: ValidateCreatePageInput(CreatePageInput{SpaceID: "42", ParentID: "x", Title: "Page", Body: "body"})},
		{name: "create missing title", err: ValidateCreatePageInput(CreatePageInput{SpaceID: "42", Body: "body"})},
		{name: "create missing body", err: ValidateCreatePageInput(CreatePageInput{SpaceID: "42", Title: "Page"})},
		{name: "create invalid UTF-8 body", err: ValidateCreatePageInput(CreatePageInput{SpaceID: "42", Title: "Page", Body: string([]byte{0xff})})},
		{name: "create oversized body", err: ValidateCreatePageInput(CreatePageInput{SpaceID: "42", Title: "Page", Body: strings.Repeat("x", MaxPageStorageBodyBytes+1)})},
		{name: "update missing page", err: ValidateUpdatePageInput(UpdatePageInput{SpaceID: "42", ExpectedVersion: 1, Title: "Page", Body: "body"})},
		{name: "update zero version", err: ValidateUpdatePageInput(UpdatePageInput{PageID: "9", SpaceID: "42", Title: "Page", Body: "body"})},
		{name: "update max version", err: ValidateUpdatePageInput(UpdatePageInput{PageID: "9", SpaceID: "42", ExpectedVersion: math.MaxInt, Title: "Page", Body: "body"})},
	}
	if err := ValidateCreatePageInput(validCreate); err != nil {
		t.Fatalf("valid create: %v", err)
	}
	if err := ValidateUpdatePageInput(validUpdate); err != nil {
		t.Fatalf("valid update: %v", err)
	}
	if err := ValidateCreatePageInput(CreatePageInput{SpaceID: "42", Title: "Page", Body: strings.Repeat("x", MaxPageStorageBodyBytes)}); err != nil {
		t.Fatalf("body at limit: %v", err)
	}
	if err := ValidateUpdatePageInput(UpdatePageInput{PageID: "9", SpaceID: "42", ExpectedVersion: math.MaxInt - 1, Title: "Page", Body: "body"}); err != nil {
		t.Fatalf("version below MaxInt: %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if errx.ExitCode(test.err) != errx.CodeUsage {
				t.Fatalf("code=%d err=%v", errx.ExitCode(test.err), test.err)
			}
		})
	}

	var calls int
	client := newTestClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not dispatch")
	})})
	_, err := client.CreatePage(context.Background(), CreatePageInput{SpaceID: "x", Title: "Page", Body: "body"})
	if errx.ExitCode(err) != errx.CodeUsage || calls != 0 {
		t.Fatalf("invalid write code=%d calls=%d", errx.ExitCode(err), calls)
	}
}

func assertOneShotWriteRequest(t *testing.T) func(*http.Request) {
	t.Helper()
	return func(request *http.Request) {
		assertCredential(t, request)
		if request.GetBody != nil {
			t.Error("write request is replayable")
		}
		if request.ContentLength <= 0 {
			t.Errorf("Content-Length = %d", request.ContentLength)
		}
		if request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Accept") != "application/json" {
			t.Errorf("content type = %q, accept = %q", request.Header.Get("Content-Type"), request.Header.Get("Accept"))
		}
		for name := range request.Header {
			if strings.Contains(strings.ToLower(name), "idempotency") {
				t.Errorf("undocumented idempotency header %q", name)
			}
		}
	}
}

func assertStorageBody(t *testing.T, payload map[string]any, want string) {
	t.Helper()
	body, ok := payload["body"].(map[string]any)
	if !ok || body["representation"] != "storage" || body["value"] != want {
		t.Error("storage body request JSON does not match")
	}
}

func assertTypedWriteError(t *testing.T, err error, wantCode errx.Code, wantReason string) {
	t.Helper()
	var typed *errx.Error
	if !errors.As(err, &typed) || errx.ExitCode(err) != wantCode || typed.Reason != wantReason {
		t.Fatalf("error=%#v code=%d, want code=%d reason=%s", err, errx.ExitCode(err), wantCode, wantReason)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("response read sentinel") }
