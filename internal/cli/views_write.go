package cli

import (
	"strconv"

	"github.com/abigotado/confluence-cli/internal/output"
	"github.com/abigotado/confluence-cli/internal/writepolicy"
)

type writePolicyView struct {
	Profile  string   `json:"profile"`
	Identity string   `json:"identity,omitempty"`
	Spaces   []string `json:"spaces"`
	State    string   `json:"state"`
	DryRun   bool     `json:"dry_run"`
	Applied  bool     `json:"applied"`
}

func newWritePolicyView(policy writepolicy.Policy, state string, dryRun, applied bool) writePolicyView {
	return writePolicyView{
		Profile: policy.Profile, Identity: policy.Identity,
		Spaces: append([]string(nil), policy.Spaces...), State: state,
		DryRun: dryRun, Applied: applied,
	}
}

func (view writePolicyView) Fields() []output.Field {
	return []output.Field{
		{Name: "profile", Value: view.Profile, Raw: view.Profile},
		{Name: "spaces", Raw: view.Spaces},
		{Name: "state", Value: view.State, Raw: view.State},
		{Name: "identity", Value: view.Identity, Raw: view.Identity, OnRequest: true},
		{Name: "dry_run", Value: strconv.FormatBool(view.DryRun), Raw: view.DryRun},
		{Name: "applied", Value: strconv.FormatBool(view.Applied), Raw: view.Applied},
	}
}

type pageMutationReceipt struct {
	Action          string `json:"action"`
	Profile         string `json:"profile"`
	SpaceID         string `json:"space_id"`
	PageID          string `json:"page_id,omitempty"`
	ParentID        string `json:"parent_id,omitempty"`
	ExpectedVersion int    `json:"expected_version,omitempty"`
	ResultVersion   int    `json:"result_version,omitempty"`
	TitleBytes      int    `json:"title_bytes"`
	BodyBytes       int    `json:"body_bytes"`
	ContentSHA256   string `json:"content_sha256"`
	IntentSHA256    string `json:"intent_sha256"`
	Representation  string `json:"representation"`
	RemoteChecks    string `json:"remote_checks"`
	DryRun          bool   `json:"dry_run"`
	Applied         bool   `json:"applied"`
}

func (view pageMutationReceipt) Fields() []output.Field {
	return []output.Field{
		{Name: "action", Value: view.Action, Raw: view.Action},
		{Name: "profile", Value: view.Profile, Raw: view.Profile},
		{Name: "space_id", Value: view.SpaceID, Raw: view.SpaceID},
		{Name: "page_id", Value: view.PageID, Raw: view.PageID},
		{Name: "parent_id", Value: view.ParentID, Raw: view.ParentID, OnRequest: view.ParentID == ""},
		{Name: "expected_version", Value: intText(view.ExpectedVersion), Raw: view.ExpectedVersion, OnRequest: view.ExpectedVersion == 0},
		{Name: "result_version", Value: intText(view.ResultVersion), Raw: view.ResultVersion, OnRequest: view.ResultVersion == 0},
		{Name: "title_bytes", Value: intText(view.TitleBytes), Raw: view.TitleBytes},
		{Name: "body_bytes", Value: intText(view.BodyBytes), Raw: view.BodyBytes},
		{Name: "content_sha256", Value: view.ContentSHA256, Raw: view.ContentSHA256},
		{Name: "intent_sha256", Value: view.IntentSHA256, Raw: view.IntentSHA256},
		{Name: "representation", Value: view.Representation, Raw: view.Representation},
		{Name: "remote_checks", Value: view.RemoteChecks, Raw: view.RemoteChecks},
		{Name: "dry_run", Value: strconv.FormatBool(view.DryRun), Raw: view.DryRun},
		{Name: "applied", Value: strconv.FormatBool(view.Applied), Raw: view.Applied},
	}
}

func intText(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}
