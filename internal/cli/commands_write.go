package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/abigotado/confluence-cli/internal/atlassian"
	"github.com/abigotado/confluence-cli/internal/auth"
	"github.com/abigotado/confluence-cli/internal/confluence"
	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/profile"
	"github.com/abigotado/confluence-cli/internal/writepolicy"
	"github.com/spf13/cobra"
)

func (a *App) newAuthAllowSpacesCommand() *cobra.Command {
	command := commandGroup("allow-spaces", "Manage the identity-bound local page-write allowlist")
	command.AddCommand(
		a.newAuthAllowSpacesShowCommand(),
		a.newAuthAllowSpacesSetCommand(),
		a.newAuthAllowSpacesClearCommand(),
	)
	return command
}

func (a *App) newAuthAllowSpacesShowCommand() *cobra.Command {
	return &cobra.Command{
		Use: "show", Short: "Show the current profile's non-secret page-write policy", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.dryRun {
				return errx.Usage("--dry-run is not supported by auth allow-spaces show")
			}
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if err := a.out.Validate(writePolicyView{}); err != nil {
				return err
			}
			if a.registry == nil || a.policies == nil {
				return errx.Internal("profile or write policy registry is unavailable")
			}
			var view writePolicyView
			err := a.registry.WithProfileLock(cmd.Context(), a.profileName, func() error {
				selected, err := a.registry.Get(cmd.Context(), a.profileName)
				if err != nil {
					return translateLocal(err, a.profileName)
				}
				return a.policies.WithPolicyLock(cmd.Context(), a.profileName, func() error {
					policy, err := a.policies.Get(cmd.Context(), a.profileName)
					if err != nil {
						return translateWritePolicy(err, a.profileName, "")
					}
					state := "bound"
					if policy.Identity != writepolicy.IdentityFor(selected) {
						state = "stale"
					}
					view = newWritePolicyView(policy, state, false, false)
					return nil
				})
			})
			if err != nil {
				return err
			}
			return a.out.Success(view)
		},
	}
}

func (a *App) newAuthAllowSpacesSetCommand() *cobra.Command {
	var spaces []string
	command := &cobra.Command{
		Use: "set", Short: "Replace the exact spaces allowed for page writes", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if err := profile.ValidateName(a.profileName); err != nil {
				return translateLocal(err, a.profileName)
			}
			canonical, err := writepolicy.CanonicalSpaces(spaces)
			if err != nil {
				return translateWritePolicy(err, a.profileName, "")
			}
			if !a.dryRun && !a.assumeYes {
				return errx.ConfirmRequired("auth allow-spaces set")
			}
			if err := a.out.Validate(writePolicyView{}); err != nil {
				return err
			}
			if a.registry == nil || a.policies == nil {
				return errx.Internal("profile or write policy registry is unavailable")
			}
			var view writePolicyView
			policyApplied := false
			err = a.registry.WithProfileLock(cmd.Context(), a.profileName, func() error {
				selected, err := a.registry.Get(cmd.Context(), a.profileName)
				if err != nil {
					return translateLocal(err, a.profileName)
				}
				if err := requirePageWriteProfile(selected); err != nil {
					return err
				}
				return a.policies.WithPolicyLock(cmd.Context(), a.profileName, func() error {
					policy := writepolicy.Policy{
						Profile: selected.Name, Identity: writepolicy.IdentityFor(selected), Spaces: canonical,
					}
					if !a.dryRun {
						policy, err = a.policies.Set(cmd.Context(), selected, canonical)
						if err != nil {
							return translateWritePolicy(err, selected.Name, "")
						}
						policyApplied = true
					}
					view = newWritePolicyView(policy, "bound", a.dryRun, !a.dryRun)
					return nil
				})
			})
			if err != nil {
				if policyApplied {
					return errx.WritePolicyOutcomeUnknown(a.profileName)
				}
				return translateWritePolicy(err, a.profileName, "")
			}
			return a.out.Success(view)
		},
	}
	command.Flags().StringArrayVar(&spaces, "space-id", nil, "exact numeric Confluence space ID to allow (repeatable)")
	return command
}

func (a *App) newAuthAllowSpacesClearCommand() *cobra.Command {
	return &cobra.Command{
		Use: "clear", Short: "Remove the current profile's local page-write policy", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if !a.dryRun && !a.assumeYes {
				return errx.ConfirmRequired("auth allow-spaces clear")
			}
			if a.registry == nil || a.policies == nil {
				return errx.Internal("profile or write policy registry is unavailable")
			}
			view := writePolicyView{Profile: a.profileName, Spaces: []string{}, State: "cleared", DryRun: a.dryRun, Applied: !a.dryRun}
			if err := a.out.Validate(view); err != nil {
				return err
			}
			policyApplied := false
			err := a.registry.WithProfileLock(cmd.Context(), a.profileName, func() error {
				selected, err := a.registry.Get(cmd.Context(), a.profileName)
				if err != nil {
					return translateLocal(err, a.profileName)
				}
				view.Identity = writepolicy.IdentityFor(selected)
				return a.policies.WithPolicyLock(cmd.Context(), a.profileName, func() error {
					if a.dryRun {
						return nil
					}
					err := a.policies.Clear(cmd.Context(), selected.Name)
					if err == nil {
						policyApplied = true
					}
					return err
				})
			})
			if err != nil {
				if policyApplied {
					return errx.WritePolicyOutcomeUnknown(a.profileName)
				}
				return translateWritePolicy(err, a.profileName, "")
			}
			return a.out.Success(view)
		},
	}
}

func (a *App) newPagesCreateCommand() *cobra.Command {
	var spaceID, parentID, title, bodyFile, representation, confirmedIntent string
	command := &cobra.Command{
		Use: "create", Short: "Create one page in an explicitly allowed space", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if err := profile.ValidateName(a.profileName); err != nil {
				return translateLocal(err, a.profileName)
			}
			if representation != "storage" {
				return errx.Usage("representation must be storage")
			}
			if err := requireCanonicalSpaceID(spaceID); err != nil {
				return err
			}
			if parentID != "" {
				if err := requireCanonicalNumericID("parent page", parentID); err != nil {
					return err
				}
			}
			body, err := readPageBody(bodyFile)
			if err != nil {
				return err
			}
			input := confluence.CreatePageInput{SpaceID: spaceID, ParentID: parentID, Title: title, Body: string(body)}
			if err := confluence.ValidateCreatePageInput(input); err != nil {
				return err
			}
			if err := a.out.Validate(pageMutationReceipt{}); err != nil {
				return err
			}
			if a.dryRun {
				identity, err := a.currentWriteProfileIdentity(cmd.Context())
				if err != nil {
					return err
				}
				receipt, err := newPageMutationReceipt("pages.create", a.profileName, identity, spaceID, "", parentID, 0, title, body, true)
				if err != nil {
					return err
				}
				return a.out.Success(receipt)
			}
			if !a.assumeYes {
				return writeIntentMismatch()
			}
			var receipt pageMutationReceipt
			result, err := a.runGuardedPageMutation(cmd.Context(), spaceID, "pages.create", func(selected profile.Profile) error {
				receipt, err = newPageMutationReceipt("pages.create", a.profileName, writepolicy.IdentityFor(selected), spaceID, "", parentID, 0, title, body, false)
				if err != nil {
					return err
				}
				if confirmedIntent != receipt.IntentSHA256 {
					return writeIntentMismatch()
				}
				return nil
			}, func(client confluenceMutationClient, _ profile.Profile) (pageMutationReceipt, error) {
				space, err := client.GetSpace(cmd.Context(), spaceID)
				if err != nil {
					return receipt, err
				}
				if space.ID != spaceID {
					return receipt, errx.Conflict("TARGET_CHANGED", "the Confluence space identity changed during preflight")
				}
				if parentID != "" {
					parent, err := client.GetPage(cmd.Context(), parentID, "none")
					if err != nil {
						return receipt, err
					}
					if parent.ID != parentID || parent.SpaceID != spaceID || parent.Status != "current" {
						return receipt, errx.Conflict("TARGET_CHANGED", "the requested parent page is not current in the allowed space")
					}
				}
				created, err := client.CreatePage(cmd.Context(), input)
				if err != nil {
					return receipt, err
				}
				receipt.PageID = created.ID
				receipt.ResultVersion = created.Version.Number
				receipt.RemoteChecks = "performed"
				receipt.Applied = true
				return receipt, nil
			})
			if err != nil {
				return err
			}
			return a.out.Success(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&spaceID, "space-id", "", "exact numeric destination space ID")
	flags.StringVar(&parentID, "parent-id", "", "optional exact numeric parent page ID; omit for a root page")
	flags.StringVar(&title, "title", "", "bounded page title")
	flags.StringVar(&bodyFile, "body-file", "", "path to one bounded storage-format body file")
	flags.StringVar(&representation, "representation", "storage", "page body representation: storage")
	flags.StringVar(&confirmedIntent, "confirm-intent", "", "exact intent_sha256 from the reviewed dry-run receipt")
	return command
}

func (a *App) newPagesUpdateCommand() *cobra.Command {
	var spaceID, title, bodyFile, representation, confirmedIntent string
	var expectedVersion int
	command := &cobra.Command{
		Use: "update PAGE_ID", Short: "Update one current page in an explicitly allowed space", Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if err := profile.ValidateName(a.profileName); err != nil {
				return translateLocal(err, a.profileName)
			}
			if representation != "storage" {
				return errx.Usage("representation must be storage")
			}
			if err := requireCanonicalSpaceID(spaceID); err != nil {
				return err
			}
			if err := requireCanonicalNumericID("page", args[0]); err != nil {
				return err
			}
			body, err := readPageBody(bodyFile)
			if err != nil {
				return err
			}
			input := confluence.UpdatePageInput{
				PageID: args[0], SpaceID: spaceID, ExpectedVersion: expectedVersion, Title: title, Body: string(body),
			}
			if err := confluence.ValidateUpdatePageInput(input); err != nil {
				return err
			}
			if err := a.out.Validate(pageMutationReceipt{}); err != nil {
				return err
			}
			if a.dryRun {
				identity, err := a.currentWriteProfileIdentity(cmd.Context())
				if err != nil {
					return err
				}
				receipt, err := newPageMutationReceipt("pages.update", a.profileName, identity, spaceID, args[0], "", expectedVersion, title, body, true)
				if err != nil {
					return err
				}
				return a.out.Success(receipt)
			}
			if !a.assumeYes {
				return writeIntentMismatch()
			}
			var receipt pageMutationReceipt
			result, err := a.runGuardedPageMutation(cmd.Context(), spaceID, "pages.update", func(selected profile.Profile) error {
				receipt, err = newPageMutationReceipt("pages.update", a.profileName, writepolicy.IdentityFor(selected), spaceID, args[0], "", expectedVersion, title, body, false)
				if err != nil {
					return err
				}
				if confirmedIntent != receipt.IntentSHA256 {
					return writeIntentMismatch()
				}
				return nil
			}, func(client confluenceMutationClient, _ profile.Profile) (pageMutationReceipt, error) {
				space, err := client.GetSpace(cmd.Context(), spaceID)
				if err != nil {
					return receipt, err
				}
				if space.ID != spaceID {
					return receipt, errx.Conflict("TARGET_CHANGED", "the Confluence space identity changed during preflight")
				}
				current, err := client.GetPage(cmd.Context(), args[0], "none")
				if err != nil {
					return receipt, err
				}
				if current.ID != args[0] || current.SpaceID != spaceID || current.Status != "current" {
					return receipt, errx.Conflict("TARGET_CHANGED", "the page is not current in the allowed space")
				}
				if current.Version.Number != expectedVersion {
					return receipt, errx.Conflict("STALE_PAGE_VERSION", "the page version changed before update")
				}
				input.ParentID = current.ParentID
				updated, err := client.UpdatePage(cmd.Context(), input)
				if err != nil {
					return receipt, err
				}
				receipt.ParentID = current.ParentID
				receipt.ResultVersion = updated.Version.Number
				receipt.RemoteChecks = "performed"
				receipt.Applied = true
				return receipt, nil
			})
			if err != nil {
				return err
			}
			return a.out.Success(result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&spaceID, "space-id", "", "exact numeric space ID that must own the page")
	flags.IntVar(&expectedVersion, "expected-version", 0, "exact current positive page version")
	flags.StringVar(&title, "title", "", "bounded replacement page title")
	flags.StringVar(&bodyFile, "body-file", "", "path to one bounded storage-format body file")
	flags.StringVar(&representation, "representation", "storage", "page body representation: storage")
	flags.StringVar(&confirmedIntent, "confirm-intent", "", "exact intent_sha256 from the reviewed dry-run receipt")
	return command
}

func (a *App) runGuardedPageMutation(
	ctx context.Context,
	spaceID string,
	operation string,
	confirm func(profile.Profile) error,
	mutate func(confluenceMutationClient, profile.Profile) (pageMutationReceipt, error),
) (pageMutationReceipt, error) {
	if a.registry == nil || a.policies == nil {
		return pageMutationReceipt{}, errx.Internal("profile or write policy registry is unavailable")
	}
	var result pageMutationReceipt
	verifiedApplied := false
	err := a.registry.WithProfileLock(ctx, a.profileName, func() error {
		selected, err := a.registry.Get(ctx, a.profileName)
		if err != nil {
			return translateLocal(err, a.profileName)
		}
		if selected.ExpiresAt != nil && !a.now().Before(*selected.ExpiresAt) {
			return errx.Auth("TOKEN_EXPIRED", "the API token for profile %q is expired", selected.Name)
		}
		if err := requirePageWriteProfile(selected); err != nil {
			return err
		}
		return a.policies.WithPolicyLock(ctx, a.profileName, func() error {
			if _, err := a.policies.RequireSpace(ctx, selected, spaceID); err != nil {
				return translateWritePolicy(err, selected.Name, spaceID)
			}
			if confirm == nil {
				return errx.Internal("page write confirmation binding is unavailable")
			}
			if err := confirm(selected); err != nil {
				return err
			}
			credential, err := a.store.Load(ctx, selected.Name)
			if err != nil {
				return translateLocal(err, selected.Name)
			}
			if err := auth.ValidateCredentialBinding(credential, selected); err != nil {
				return translateLocal(err, selected.Name)
			}
			reader, err := a.newClient(selected, credential, a.log)
			if err != nil {
				return err
			}
			client, ok := reader.(confluenceMutationClient)
			if !ok {
				return errx.Internal("Confluence client does not implement guarded page writes")
			}
			a.out.WithContext(selected.Name, selected.Site)
			result, err = mutate(client, selected)
			if err == nil && result.Applied {
				verifiedApplied = true
			}
			return err
		})
	})
	if err != nil {
		var typed *errx.Error
		if errors.As(err, &typed) {
			return pageMutationReceipt{}, typed
		}
		if verifiedApplied {
			return pageMutationReceipt{}, errx.WriteAppliedLocalFailure(operation)
		}
		return pageMutationReceipt{}, translateLocal(err, a.profileName)
	}
	return result, nil
}

func requirePageWriteProfile(selected profile.Profile) error {
	if selected.CredentialGeneration == "" || !selected.HasCapability(profile.CapabilityPageWrite) {
		return errx.Permission("PAGE_WRITE_CAPABILITY_REQUIRED", "profile %q has no declared page-write credential", selected.Name).
			WithHint("re-run auth login for this profile with --capability page-write and a matching scoped token")
	}
	return nil
}

func requireCanonicalSpaceID(spaceID string) error {
	canonical, err := writepolicy.CanonicalSpaces([]string{spaceID})
	if err != nil || len(canonical) != 1 || canonical[0] != spaceID {
		return errx.Usage("space ID must be a canonical positive numeric value")
	}
	return nil
}

func requireCanonicalNumericID(kind, value string) error {
	if value == "" || len(value) > atlassian.MaxNumericIDLength || value[0] == '0' {
		return errx.Usage("%s ID must be a canonical positive numeric value", kind)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return errx.Usage("%s ID must be a canonical positive numeric value", kind)
		}
	}
	return nil
}

func readPageBody(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" || path == "-" {
		return nil, errx.Usage("--body-file must name a regular local file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errx.Usage("could not open --body-file")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, errx.Usage("could not inspect --body-file")
	}
	if !info.Mode().IsRegular() {
		return nil, errx.Usage("--body-file must be a regular local file")
	}
	if info.Size() > confluence.MaxPageStorageBodyBytes {
		return nil, errx.Usage("storage body must be no longer than %d bytes", confluence.MaxPageStorageBodyBytes)
	}
	body, err := io.ReadAll(io.LimitReader(file, confluence.MaxPageStorageBodyBytes+1))
	if err != nil {
		return nil, errx.Usage("could not read --body-file")
	}
	if len(body) > confluence.MaxPageStorageBodyBytes {
		return nil, errx.Usage("storage body must be no longer than %d bytes", confluence.MaxPageStorageBodyBytes)
	}
	return body, nil
}

func newPageMutationReceipt(action, profileName, profileIdentity, spaceID, pageID, parentID string, expectedVersion int, title string, body []byte, dryRun bool) (pageMutationReceipt, error) {
	digest := sha256.Sum256(body)
	contentDigest := hex.EncodeToString(digest[:])
	intent, err := json.Marshal(struct {
		Action          string `json:"action"`
		Profile         string `json:"profile"`
		ProfileIdentity string `json:"profile_identity"`
		SpaceID         string `json:"space_id"`
		PageID          string `json:"page_id"`
		ParentID        string `json:"parent_id"`
		ExpectedVersion int    `json:"expected_version"`
		Title           string `json:"title"`
		ContentSHA256   string `json:"content_sha256"`
		Representation  string `json:"representation"`
	}{action, profileName, profileIdentity, spaceID, pageID, parentID, expectedVersion, title, contentDigest, "storage"})
	if err != nil {
		return pageMutationReceipt{}, errx.Internal("could not bind the page write intent")
	}
	intentDigest := sha256.Sum256(intent)
	return pageMutationReceipt{
		Action: action, Profile: profileName, SpaceID: spaceID, PageID: pageID, ParentID: parentID,
		ExpectedVersion: expectedVersion, TitleBytes: len(title), BodyBytes: len(body),
		ContentSHA256: contentDigest, IntentSHA256: hex.EncodeToString(intentDigest[:]), Representation: "storage",
		RemoteChecks: remoteChecks(dryRun), DryRun: dryRun, Applied: false,
	}, nil
}

func (a *App) currentWriteProfileIdentity(ctx context.Context) (string, error) {
	if a.registry == nil {
		return "", errx.Internal("profile registry is unavailable")
	}
	selected, err := a.registry.Get(ctx, a.profileName)
	if err != nil {
		return "", translateLocal(err, a.profileName)
	}
	if err := requirePageWriteProfile(selected); err != nil {
		return "", err
	}
	return writepolicy.IdentityFor(selected), nil
}

func writeIntentMismatch() error {
	return errx.IntentConfirmationRequired()
}

func remoteChecks(dryRun bool) string {
	if dryRun {
		return "not_performed"
	}
	return "pending"
}

func translateWritePolicy(err error, profileName, spaceID string) error {
	if err == nil {
		return nil
	}
	var typed *errx.Error
	if errors.As(err, &typed) {
		return typed
	}
	switch {
	case writepolicy.WasCommitted(err):
		return errx.WritePolicyOutcomeUnknown(profileName)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errx.Translate(err)
	case errors.Is(err, writepolicy.ErrNotFound):
		return errx.NotFound("write policy", profileName, nil).WithHint("set an exact space allowlist with 'auth allow-spaces set --yes'")
	case errors.Is(err, writepolicy.ErrStale):
		return errx.Conflict("STALE_WRITE_POLICY", "the local space allowlist belongs to an older credential generation").
			WithHint("review and replace the allowlist with 'auth allow-spaces set --yes'")
	case errors.Is(err, writepolicy.ErrSpaceDenied):
		return errx.Permission("SPACE_NOT_ALLOWED", "space %q is not in the profile's local write allowlist", spaceID).
			WithHint("review the target, then replace the allowlist with 'auth allow-spaces set --yes'")
	case errors.Is(err, writepolicy.ErrInvalid):
		return errx.Usage("write policy input is invalid")
	case errors.Is(err, writepolicy.ErrCorruptRegistry), errors.Is(err, writepolicy.ErrInsecurePermissions):
		return errx.Internal("write policy registry cannot be used safely")
	default:
		return errx.Internal("local write policy operation failed")
	}
}
