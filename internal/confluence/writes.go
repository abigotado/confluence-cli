package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/abigotado/confluence-cli/internal/errx"
)

// MaxPageStorageBodyBytes is the largest storage-format page body accepted by
// the guarded write surface. Callers can use the same bound while reading a
// local file, before credentials or the network are involved.
const MaxPageStorageBodyBytes = 1 << 20

// CreatePageInput is the complete typed intent for one storage-format page
// creation. An empty ParentID creates a root-level page.
type CreatePageInput struct {
	SpaceID  string
	ParentID string
	Title    string
	Body     string
}

// UpdatePageInput is the complete typed intent for one storage-format page
// update. SpaceID and ParentID bind response verification to the preflight
// identity; neither field is sent in the update request body.
type UpdatePageInput struct {
	PageID          string
	SpaceID         string
	ParentID        string
	ExpectedVersion int
	Title           string
	Body            string
}

type pageWriteBody struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

type createPagePayload struct {
	SpaceID  string        `json:"spaceId"`
	Status   string        `json:"status"`
	Title    string        `json:"title"`
	ParentID string        `json:"parentId,omitempty"`
	Body     pageWriteBody `json:"body"`
}

type updatePagePayload struct {
	ID      string        `json:"id"`
	Status  string        `json:"status"`
	Title   string        `json:"title"`
	Body    pageWriteBody `json:"body"`
	Version struct {
		Number int `json:"number"`
	} `json:"version"`
}

// ValidateCreatePageInput validates create intent without reading credentials
// or performing a network operation.
func ValidateCreatePageInput(input CreatePageInput) error {
	if err := validateNumericID("space", input.SpaceID); err != nil {
		return err
	}
	if input.ParentID != "" {
		if err := validateNumericID("parent page", input.ParentID); err != nil {
			return err
		}
	}
	if err := validateBoundedText("title", input.Title, 512); err != nil {
		return err
	}
	return validateStorageBody(input.Body)
}

// ValidateUpdatePageInput validates update intent without reading credentials
// or performing a network operation.
func ValidateUpdatePageInput(input UpdatePageInput) error {
	if err := validateNumericID("page", input.PageID); err != nil {
		return err
	}
	if err := validateNumericID("space", input.SpaceID); err != nil {
		return err
	}
	if input.ParentID != "" {
		if err := validateNumericID("parent page", input.ParentID); err != nil {
			return err
		}
	}
	if input.ExpectedVersion < 1 || input.ExpectedVersion >= math.MaxInt {
		return errx.Usage("expected version must be between 1 and %d", math.MaxInt-1)
	}
	if err := validateBoundedText("title", input.Title, 512); err != nil {
		return err
	}
	return validateStorageBody(input.Body)
}

func validateStorageBody(body string) error {
	if body == "" {
		return errx.Usage("storage body is required")
	}
	if !utf8.ValidString(body) {
		return errx.Usage("storage body must be valid UTF-8")
	}
	if len(body) > MaxPageStorageBodyBytes {
		return errx.Usage("storage body must be no longer than %d bytes", MaxPageStorageBodyBytes)
	}
	return nil
}

// CreatePage dispatches exactly one non-replayable Confluence page creation.
func (client *Client) CreatePage(ctx context.Context, input CreatePageInput) (Page, error) {
	if err := ValidateCreatePageInput(input); err != nil {
		return Page{}, err
	}
	payload := createPagePayload{
		SpaceID:  input.SpaceID,
		Status:   "current",
		Title:    input.Title,
		ParentID: input.ParentID,
		Body:     pageWriteBody{Representation: "storage", Value: input.Body},
	}
	path := pagesPath
	if input.ParentID == "" {
		path += "?root-level=true"
	}
	result, err := client.writePage(ctx, http.MethodPost, path, "create page", payload, true)
	if err != nil {
		return Page{}, err
	}
	if !validCreateIdentity(result, input) {
		return Page{}, errx.WriteOutcomeUnknown("create page")
	}
	return result, nil
}

// UpdatePage dispatches exactly one non-replayable Confluence page update.
func (client *Client) UpdatePage(ctx context.Context, input UpdatePageInput) (Page, error) {
	if err := ValidateUpdatePageInput(input); err != nil {
		return Page{}, err
	}
	payload := updatePagePayload{
		ID:     input.PageID,
		Status: "current",
		Title:  input.Title,
		Body:   pageWriteBody{Representation: "storage", Value: input.Body},
	}
	payload.Version.Number = input.ExpectedVersion + 1
	result, err := client.writePage(ctx, http.MethodPut, pagesPath+"/"+url.PathEscape(input.PageID), "update page", payload, false)
	if err != nil {
		return Page{}, err
	}
	if !validUpdateIdentity(result, input) {
		return Page{}, errx.WriteOutcomeUnknown("update page")
	}
	return result, nil
}

func (client *Client) writePage(ctx context.Context, method, path, operation string, payload any, create bool) (Page, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Page{}, errx.Internal("could not encode Confluence page write")
	}
	reader := &nonReplayableReader{reader: bytes.NewReader(body)}
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, reader)
	if err != nil {
		return Page{}, errx.Internal("could not build Confluence page write")
	}
	request.ContentLength = int64(len(body))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(client.cred.Email, client.cred.Token)
	client.log.Debug("Confluence write", "operation", operation)

	response, err := client.http.Do(request)
	if err != nil {
		return Page{}, errx.WriteOutcomeUnknown(operation)
	}
	if response == nil || response.Body == nil {
		return Page{}, errx.WriteOutcomeUnknown(operation)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrainBody))
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return Page{}, client.translateWriteStatus(response, operation, create)
	}
	body, tooLarge, readErr := readResponseBody(response)
	if readErr != nil || tooLarge {
		return Page{}, errx.WriteOutcomeUnknown(operation)
	}
	var result Page
	if err := json.Unmarshal(body, &result); err != nil {
		return Page{}, errx.WriteOutcomeUnknown(operation)
	}
	return result, nil
}

func (client *Client) translateWriteStatus(response *http.Response, operation string, create bool) error {
	switch response.StatusCode {
	case http.StatusBadRequest:
		if create {
			return errx.Usage("Confluence rejected the create page request")
		}
		return errx.Conflict("STALE_PAGE_VERSION", "the Confluence page version changed before the update")
	case http.StatusUnauthorized:
		return errx.Auth("AUTHENTICATION_FAILED", "Confluence rejected the account credentials")
	case http.StatusForbidden:
		return errx.Permission("SCOPE_OR_PERMISSION_DENIED", "the Confluence account lacks page-write permission or API token scope")
	case http.StatusNotFound:
		return errx.Conflict("TARGET_CHANGED", "the Confluence write target changed after preflight")
	case http.StatusConflict:
		return errx.Conflict("CONFLICT", "Confluence reported a page-state conflict")
	case http.StatusRequestEntityTooLarge:
		return errx.PayloadTooLarge(operation)
	case http.StatusTooManyRequests:
		delay := parseRetryAfter(response.Header.Get("Retry-After"), client.now())
		return errx.Retryable("RATE_LIMITED", delay, "Confluence rate limited the page write")
	default:
		return errx.WriteOutcomeUnknown(operation)
	}
}

func validCreateIdentity(page Page, input CreatePageInput) bool {
	return numericID(page.ID) && page.SpaceID == input.SpaceID && page.Title == input.Title &&
		page.Status == "current" && page.ParentID == input.ParentID && page.Version.Number > 0
}

func validUpdateIdentity(page Page, input UpdatePageInput) bool {
	return page.ID == input.PageID && page.SpaceID == input.SpaceID && page.Title == input.Title &&
		page.Status == "current" && page.ParentID == input.ParentID &&
		page.Version.Number == input.ExpectedVersion+1
}

func numericID(value string) bool {
	return validateNumericID("page", value) == nil
}

type nonReplayableReader struct {
	reader *bytes.Reader
}

func (reader *nonReplayableReader) Read(buffer []byte) (int, error) {
	return reader.reader.Read(buffer)
}

var _ io.Reader = (*nonReplayableReader)(nil)
