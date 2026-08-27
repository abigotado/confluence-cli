package confluence

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/abigotado/confluence-cli/internal/atlassian"
	"github.com/abigotado/confluence-cli/internal/errx"
)

// ListOptions controls a bounded cursor page.
type ListOptions struct {
	Limit  int
	Cursor string
}

// PageListOptions controls a bounded v2 pages query.
type PageListOptions struct {
	ListOptions
	SpaceID string
	Status  string
	Title   string
}

// PageResult pairs a modeled collection with its opaque continuation cursor.
type PageResult[T any] struct {
	Results    T
	NextCursor string
}

// ListSpaces returns a bounded cursor page of visible spaces.
func (client *Client) ListSpaces(ctx context.Context, options ListOptions) (PageResult[Spaces], error) {
	if err := validateListOptions(options); err != nil {
		return PageResult[Spaces]{}, err
	}
	query := listQuery(options)
	var payload struct {
		Results Spaces `json:"results"`
		Links   Links  `json:"_links"`
	}
	header, err := client.get(ctx, request{path: spacesPath, query: query, operation: "spaces.list"}, &payload)
	if err != nil {
		return PageResult[Spaces]{}, err
	}
	if payload.Results == nil {
		return PageResult[Spaces]{}, invalidReadResponse()
	}
	cursor, err := nextCursor(payload.Links, header, spacesPath, client.cred.CloudID)
	if err != nil {
		return PageResult[Spaces]{}, err
	}
	return PageResult[Spaces]{Results: payload.Results, NextCursor: cursor}, nil
}

// GetSpace returns one visible space by exact numeric ID.
func (client *Client) GetSpace(ctx context.Context, id string) (Space, error) {
	if err := validateNumericID("space", id); err != nil {
		return Space{}, err
	}
	var result Space
	_, err := client.get(ctx, request{path: spacesPath + "/" + url.PathEscape(id), operation: "spaces.get"}, &result)
	if err != nil {
		return Space{}, err
	}
	if result.ID != id {
		return Space{}, invalidReadResponse()
	}
	return result, nil
}

// ListPages returns a bounded cursor page of visible pages.
func (client *Client) ListPages(ctx context.Context, options PageListOptions) (PageResult[Pages], error) {
	if err := validateListOptions(options.ListOptions); err != nil {
		return PageResult[Pages]{}, err
	}
	query := listQuery(options.ListOptions)
	if options.SpaceID != "" {
		if err := validateNumericID("space", options.SpaceID); err != nil {
			return PageResult[Pages]{}, err
		}
		query.Set("space-id", options.SpaceID)
	}
	if options.Status != "" {
		if options.Status != "current" && options.Status != "archived" && options.Status != "draft" {
			return PageResult[Pages]{}, errx.Usage("page status must be current, archived, or draft")
		}
		query.Set("status", options.Status)
	}
	if options.Title != "" {
		if err := validateBoundedText("title", options.Title, 512); err != nil {
			return PageResult[Pages]{}, err
		}
		query.Set("title", options.Title)
	}
	var payload struct {
		Results Pages `json:"results"`
		Links   Links `json:"_links"`
	}
	header, err := client.get(ctx, request{path: pagesPath, query: query, operation: "pages.list"}, &payload)
	if err != nil {
		return PageResult[Pages]{}, err
	}
	if payload.Results == nil {
		return PageResult[Pages]{}, invalidReadResponse()
	}
	cursor, err := nextCursor(payload.Links, header, pagesPath, client.cred.CloudID)
	if err != nil {
		return PageResult[Pages]{}, err
	}
	return PageResult[Pages]{Results: payload.Results, NextCursor: cursor}, nil
}

// GetPage returns one page by exact numeric ID and an optional body format.
func (client *Client) GetPage(ctx context.Context, id, bodyFormat string) (Page, error) {
	if err := validateNumericID("page", id); err != nil {
		return Page{}, err
	}
	query := url.Values{}
	if bodyFormat != "" && bodyFormat != "none" {
		switch bodyFormat {
		case "storage", "view", "atlas_doc_format":
			query.Set("body-format", bodyFormat)
		default:
			return Page{}, errx.Usage("body format must be none, storage, view, or atlas_doc_format")
		}
	}
	var result Page
	_, err := client.get(ctx, request{path: pagesPath + "/" + url.PathEscape(id), query: query, operation: "pages.get"}, &result)
	if err != nil {
		return Page{}, err
	}
	if result.ID != id {
		return Page{}, invalidReadResponse()
	}
	return result, nil
}

// Search performs one bounded CQL content search.
func (client *Client) Search(ctx context.Context, cql string, options ListOptions) (PageResult[SearchResults], error) {
	if err := validateListOptions(options); err != nil {
		return PageResult[SearchResults]{}, err
	}
	if err := validateBoundedText("CQL", cql, 4096); err != nil {
		return PageResult[SearchResults]{}, err
	}
	query := listQuery(options)
	query.Set("cql", cql)
	var payload struct {
		Results []struct {
			Content struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				Status string `json:"status"`
				Title  string `json:"title"`
				Space  struct {
					Key string `json:"key"`
				} `json:"space"`
			} `json:"content"`
			Title        string `json:"title"`
			Excerpt      string `json:"excerpt"`
			URL          string `json:"url"`
			LastModified string `json:"lastModified"`
		} `json:"results"`
		Links Links `json:"_links"`
	}
	header, err := client.get(ctx, request{path: searchPath, query: query, operation: "search.cql"}, &payload)
	if err != nil {
		return PageResult[SearchResults]{}, err
	}
	if payload.Results == nil {
		return PageResult[SearchResults]{}, invalidReadResponse()
	}
	results := make(SearchResults, 0, len(payload.Results))
	for _, item := range payload.Results {
		title := item.Content.Title
		if title == "" {
			title = item.Title
		}
		results = append(results, SearchResult{
			ID: item.Content.ID, Type: item.Content.Type, Status: item.Content.Status,
			Title: title, SpaceKey: item.Content.Space.Key, Excerpt: item.Excerpt,
			URL: item.URL, LastModified: item.LastModified,
		})
	}
	cursor, err := nextCursor(payload.Links, header, searchPath, client.cred.CloudID)
	if err != nil {
		return PageResult[SearchResults]{}, err
	}
	return PageResult[SearchResults]{Results: results, NextCursor: cursor}, nil
}

// VerifyRequiredAccess proves the three MVP operations are authorized. It
// cannot prove that the token has no additional scopes.
func (client *Client) VerifyRequiredAccess(ctx context.Context) error {
	if _, err := client.ListSpaces(ctx, ListOptions{Limit: defaultVerificationLimit}); err != nil {
		return fmt.Errorf("verify read:space:confluence: %w", err)
	}
	if _, err := client.ListPages(ctx, PageListOptions{ListOptions: ListOptions{Limit: defaultVerificationLimit}}); err != nil {
		return fmt.Errorf("verify read:page:confluence: %w", err)
	}
	if _, err := client.Search(ctx, "type=page", ListOptions{Limit: defaultVerificationLimit}); err != nil {
		return fmt.Errorf("verify search:confluence: %w", err)
	}
	return nil
}

func listQuery(options ListOptions) url.Values {
	query := url.Values{}
	query.Set("limit", strconv.Itoa(options.Limit))
	if options.Cursor != "" {
		query.Set("cursor", options.Cursor)
	}
	return query
}

func validateListOptions(options ListOptions) error {
	if options.Limit < 1 || options.Limit > 100 {
		return errx.Usage("limit must be between 1 and 100")
	}
	return ValidateCursor(options.Cursor)
}

func validateNumericID(kind, id string) error {
	if id == "" || len(id) > atlassian.MaxNumericIDLength {
		return errx.Usage("%s ID must be a non-empty numeric value", kind)
	}
	for _, character := range id {
		if character < '0' || character > '9' {
			return errx.Usage("%s ID must be numeric", kind)
		}
	}
	return nil
}

func validateBoundedText(name, value string, limit int) error {
	if strings.TrimSpace(value) == "" {
		return errx.Usage("%s is required", name)
	}
	if len(value) > limit || strings.ContainsAny(value, "\x00\r\n") {
		return errx.Usage("%s must be a single value no longer than %d bytes", name, limit)
	}
	return nil
}
