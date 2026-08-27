package confluence

import (
	"encoding/json"

	"github.com/abigotado/confluence-cli/internal/output"
)

// Links is the bounded subset of Confluence navigation metadata used by the
// client. Continuation URLs are never followed directly.
type Links struct {
	Next  string `json:"next,omitempty"`
	Base  string `json:"base,omitempty"`
	WebUI string `json:"webui,omitempty"`
}

// Space is the modeled subset of a Confluence v2 space.
type Space struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	Status     string `json:"status,omitempty"`
	HomepageID string `json:"homepage_id,omitempty"`
	Links      Links  `json:"_links,omitempty"`
}

// UnmarshalJSON maps Atlassian's camelCase wire fields onto the CLI's stable
// snake_case machine model.
func (space *Space) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID         string `json:"id"`
		Key        string `json:"key"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Status     string `json:"status"`
		HomepageID string `json:"homepageId"`
		Links      Links  `json:"_links"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*space = Space{ID: wire.ID, Key: wire.Key, Name: wire.Name, Type: wire.Type, Status: wire.Status, HomepageID: wire.HomepageID, Links: wire.Links}
	return nil
}

// Fields implements output.Renderable.
func (space Space) Fields() []output.Field {
	return []output.Field{
		{Name: "id", Value: space.ID, Raw: space.ID},
		{Name: "key", Value: space.Key, Raw: space.Key},
		{Name: "name", Value: space.Name, Raw: space.Name},
		{Name: "type", Value: space.Type, Raw: space.Type, OnRequest: true},
		{Name: "status", Value: space.Status, Raw: space.Status},
		{Name: "homepage_id", Value: space.HomepageID, Raw: space.HomepageID, OnRequest: true},
	}
}

// Spaces is one cursor page of spaces.
type Spaces []Space

// RenderRows implements output.RenderableCollection.
func (spaces Spaces) RenderRows() []output.Renderable {
	rows := make([]output.Renderable, len(spaces))
	for index := range spaces {
		rows[index] = spaces[index]
	}
	return rows
}

// SchemaFields implements output.RenderableCollection.
func (Spaces) SchemaFields() []output.Field { return (Space{}).Fields() }

// PageVersion is the modeled subset of a Confluence page version.
type PageVersion struct {
	Number    int    `json:"number"`
	CreatedAt string `json:"created_at,omitempty"`
	Message   string `json:"message,omitempty"`
}

// BodyValue is one explicitly requested page-body representation.
type BodyValue struct {
	Representation string `json:"representation,omitempty"`
	Value          string `json:"value,omitempty"`
}

// PageBody contains only supported, explicitly modeled body formats.
type PageBody struct {
	Storage        BodyValue `json:"storage,omitempty"`
	View           BodyValue `json:"view,omitempty"`
	AtlasDocFormat BodyValue `json:"atlas_doc_format,omitempty"`
}

// Page is the modeled subset of a Confluence v2 page.
type Page struct {
	ID        string      `json:"id"`
	Status    string      `json:"status,omitempty"`
	Title     string      `json:"title"`
	SpaceID   string      `json:"space_id"`
	ParentID  string      `json:"parent_id,omitempty"`
	CreatedAt string      `json:"created_at,omitempty"`
	Version   PageVersion `json:"version,omitempty"`
	Body      PageBody    `json:"body,omitempty"`
	Links     Links       `json:"_links,omitempty"`
}

// UnmarshalJSON maps Atlassian's camelCase wire fields onto the CLI's stable
// snake_case machine model.
func (page *Page) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		Title     string `json:"title"`
		SpaceID   string `json:"spaceId"`
		ParentID  string `json:"parentId"`
		CreatedAt string `json:"createdAt"`
		Version   struct {
			Number    int    `json:"number"`
			CreatedAt string `json:"createdAt"`
			Message   string `json:"message"`
		} `json:"version"`
		Body  PageBody `json:"body"`
		Links Links    `json:"_links"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*page = Page{ID: wire.ID, Status: wire.Status, Title: wire.Title, SpaceID: wire.SpaceID, ParentID: wire.ParentID, CreatedAt: wire.CreatedAt, Version: PageVersion{Number: wire.Version.Number, CreatedAt: wire.Version.CreatedAt, Message: wire.Version.Message}, Body: wire.Body, Links: wire.Links}
	return nil
}

// Fields implements output.Renderable.
func (page Page) Fields() []output.Field {
	return []output.Field{
		{Name: "id", Value: page.ID, Raw: page.ID},
		{Name: "title", Value: page.Title, Raw: page.Title},
		{Name: "status", Value: page.Status, Raw: page.Status},
		{Name: "space_id", Value: page.SpaceID, Raw: page.SpaceID},
		{Name: "parent_id", Value: page.ParentID, Raw: page.ParentID, OnRequest: true},
		{Name: "created_at", Value: page.CreatedAt, Raw: page.CreatedAt, OnRequest: true},
		{Name: "version", Raw: page.Version, OnRequest: true},
		{Name: "body", Raw: page.Body, OnRequest: true},
	}
}

// Pages is one cursor page of pages.
type Pages []Page

// RenderRows implements output.RenderableCollection.
func (pages Pages) RenderRows() []output.Renderable {
	rows := make([]output.Renderable, len(pages))
	for index := range pages {
		rows[index] = pages[index]
	}
	return rows
}

// SchemaFields implements output.RenderableCollection.
func (Pages) SchemaFields() []output.Field { return (Page{}).Fields() }

// SearchResult is a bounded subset of a Confluence v1 CQL result.
type SearchResult struct {
	ID           string `json:"id,omitempty"`
	Type         string `json:"type,omitempty"`
	Status       string `json:"status,omitempty"`
	Title        string `json:"title,omitempty"`
	SpaceKey     string `json:"space_key,omitempty"`
	Excerpt      string `json:"excerpt,omitempty"`
	URL          string `json:"url,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

// Fields implements output.Renderable.
func (result SearchResult) Fields() []output.Field {
	return []output.Field{
		{Name: "id", Value: result.ID, Raw: result.ID},
		{Name: "type", Value: result.Type, Raw: result.Type},
		{Name: "title", Value: result.Title, Raw: result.Title},
		{Name: "status", Value: result.Status, Raw: result.Status},
		{Name: "space_key", Value: result.SpaceKey, Raw: result.SpaceKey},
		{Name: "last_modified", Value: result.LastModified, Raw: result.LastModified},
		{Name: "excerpt", Value: result.Excerpt, Raw: result.Excerpt, OnRequest: true},
		{Name: "url", Value: result.URL, Raw: result.URL, OnRequest: true},
	}
}

// SearchResults is one cursor page of CQL results.
type SearchResults []SearchResult

// RenderRows implements output.RenderableCollection.
func (results SearchResults) RenderRows() []output.Renderable {
	rows := make([]output.Renderable, len(results))
	for index := range results {
		rows[index] = results[index]
	}
	return rows
}

// SchemaFields implements output.RenderableCollection.
func (SearchResults) SchemaFields() []output.Field { return (SearchResult{}).Fields() }
