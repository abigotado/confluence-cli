package cli

import (
	"time"

	"github.com/abigotado/confluence-cli/internal/output"
	"github.com/abigotado/confluence-cli/internal/profile"
)

type profileView struct {
	Name                 string               `json:"name"`
	Site                 string               `json:"site,omitempty"`
	Email                string               `json:"email,omitempty"`
	CloudID              string               `json:"cloud_id,omitempty"`
	ExpiresAt            *string              `json:"expires_at,omitempty"`
	CredentialGeneration string               `json:"credential_generation,omitempty"`
	Capabilities         []profile.Capability `json:"capabilities,omitempty"`
	State                string               `json:"credential_state"`
}

func newProfileView(value profile.Profile, state string) profileView {
	view := profileView{
		Name: value.Name, Site: value.Site, Email: value.Email, CloudID: value.CloudID,
		CredentialGeneration: value.CredentialGeneration,
		Capabilities:         append([]profile.Capability(nil), value.Capabilities...),
		State:                state,
	}
	if value.ExpiresAt != nil {
		formatted := value.ExpiresAt.Format(time.DateOnly)
		view.ExpiresAt = &formatted
	}
	return view
}

func (view profileView) Fields() []output.Field {
	return []output.Field{
		{Name: "name", Value: view.Name, Raw: view.Name},
		{Name: "site", Value: view.Site, Raw: view.Site},
		{Name: "email", Value: view.Email, Raw: view.Email},
		{Name: "cloud_id", Value: view.CloudID, Raw: view.CloudID, OnRequest: true},
		{Name: "expires_at", Raw: view.ExpiresAt, OnRequest: view.ExpiresAt == nil},
		{Name: "credential_generation", Value: view.CredentialGeneration, Raw: view.CredentialGeneration, OnRequest: true},
		{Name: "capabilities", Raw: view.Capabilities},
		{Name: "credential_state", Value: view.State, Raw: view.State},
	}
}

type versionView struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	CommitTime string `json:"commit_time"`
	Go         string `json:"go"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}

func (view versionView) Fields() []output.Field {
	return []output.Field{
		{Name: "version", Value: view.Version, Raw: view.Version},
		{Name: "commit", Value: view.Commit, Raw: view.Commit, OnRequest: true},
		{Name: "commit_time", Value: view.CommitTime, Raw: view.CommitTime, OnRequest: true},
		{Name: "go", Value: view.Go, Raw: view.Go, OnRequest: true},
		{Name: "os", Value: view.OS, Raw: view.OS, OnRequest: true},
		{Name: "arch", Value: view.Arch, Raw: view.Arch, OnRequest: true},
	}
}
