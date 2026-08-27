package cli

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/abigotado/confluence-cli/internal/auth"
	"github.com/abigotado/confluence-cli/internal/confluence"
	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/profile"
	"github.com/abigotado/confluence-cli/internal/skills"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) newContractCommand() *cobra.Command {
	return &cobra.Command{
		Use: "contract", Short: "Print the versioned envelope and exit-code contract", Args: usageArgs(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error { return a.out.Success(errx.Describe()) },
	}
}

func (a *App) newAuthCommand() *cobra.Command {
	command := commandGroup("auth", "Manage named Confluence profiles")
	command.AddCommand(
		a.newAuthLoginCommand(),
		a.newAuthMigrateKeychainCommand(),
		a.newAuthListCommand(),
		a.newAuthStatusCommand(),
		a.newAuthLogoutCommand(),
		a.newAuthAllowSpacesCommand(),
	)
	return command
}

func (a *App) newAuthLoginCommand() *cobra.Command {
	var site, email, expiresAt, capability string
	var tokenStdin bool
	command := &cobra.Command{
		Use: "login", Short: "Verify and store one scoped token in macOS Keychain", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.dryRun {
				return errx.Usage("--dry-run is not supported by auth login")
			}
			if len(a.fields) > 0 {
				return errx.Usage("--fields is not supported by auth login")
			}
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if !tokenStdin {
				return errx.Usage("auth login requires --token-stdin; tokens are never accepted through argv")
			}
			if err := profile.ValidateName(a.profileName); err != nil {
				return translateLocal(err, a.profileName)
			}
			if err := profile.ValidateSite(site); err != nil {
				return translateLocal(err, a.profileName)
			}
			if err := profile.ValidateEmail(email); err != nil {
				return translateLocal(err, a.profileName)
			}
			candidate := profile.Profile{Name: a.profileName, Site: site, Email: email}
			switch capability {
			case string(profile.CapabilityRead):
				candidate.Capabilities = []profile.Capability{profile.CapabilityRead}
			case string(profile.CapabilityPageWrite):
				candidate.Capabilities = []profile.Capability{profile.CapabilityRead, profile.CapabilityPageWrite}
			default:
				return errx.Usage("--capability must be read or page-write")
			}
			if expiresAt != "" {
				parsed, err := time.Parse(time.DateOnly, expiresAt)
				if err != nil {
					return errx.Usage("--expires-at must use YYYY-MM-DD")
				}
				candidate.ExpiresAt = &parsed
			}
			if err := a.out.Validate(profileView{}); err != nil {
				return err
			}
			cloudID, err := a.discoverCloudID(cmd.Context(), site)
			if err != nil {
				return err
			}
			candidate.CloudID = cloudID
			if err := candidate.ValidateLoginIntent(); err != nil {
				return translateLocal(err, candidate.Name)
			}
			if a.registry == nil {
				return errx.Internal("profile registry is unavailable")
			}
			metadataExists := false
			if _, err := a.registry.Get(cmd.Context(), candidate.Name); err == nil {
				metadataExists = true
			} else if !errors.Is(err, profile.ErrNotFound) {
				return translateLocal(err, candidate.Name)
			}
			credentialExists, err := a.store.Exists(cmd.Context(), candidate.Name)
			if err != nil {
				return translateLocal(err, candidate.Name)
			}
			if (metadataExists || credentialExists) && !a.assumeYes {
				return errx.ConfirmRequired("auth login overwrite")
			}
			if file, ok := a.stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
				_, _ = fmt.Fprint(a.stderr, "API token: ")
				defer func() { _, _ = fmt.Fprintln(a.stderr) }()
			}
			credential, err := auth.ReadToken(a.stdin)
			if err != nil {
				return translateLocal(err, candidate.Name)
			}
			client, err := a.newClient(candidate, credential, a.log)
			if err != nil {
				return err
			}
			if err := client.VerifyRequiredAccess(cmd.Context()); err != nil {
				return err
			}
			stored, err := auth.Login(cmd.Context(), a.store, a.registry, candidate, credential, a.assumeYes)
			if err != nil {
				return translateLocal(err, candidate.Name)
			}
			state := "verified"
			if stored.HasCapability(profile.CapabilityPageWrite) {
				state = "verified_reads_write_declared"
			}
			return a.out.Success(newProfileView(stored, state))
		},
	}
	flags := command.Flags()
	flags.StringVar(&site, "site", "", "Confluence Cloud site, exactly https://<tenant>.atlassian.net")
	flags.StringVar(&email, "email", "", "Atlassian account email")
	flags.BoolVar(&tokenStdin, "token-stdin", false, "read one bounded scoped token from stdin without echo")
	flags.StringVar(&expiresAt, "expires-at", "", "optional token expiry date (YYYY-MM-DD)")
	flags.StringVar(&capability, "capability", string(profile.CapabilityRead), "declared credential capability: read or page-write")
	return command
}

func (a *App) newAuthListCommand() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List non-secret profile metadata without reading Keychain", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.registry == nil {
				return errx.Internal("profile registry is unavailable")
			}
			profiles, err := a.registry.List(cmd.Context())
			if err != nil {
				return translateLocal(err, "")
			}
			views := make([]profileView, len(profiles))
			for index, value := range profiles {
				views[index] = newProfileView(value, "unchecked")
			}
			return a.out.Success(views)
		},
	}
}

func (a *App) newAuthStatusCommand() *cobra.Command {
	var check bool
	command := &cobra.Command{
		Use: "status", Short: "Show local profile and Keychain presence, optionally checking Confluence", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if a.registry == nil {
				return errx.Internal("profile registry is unavailable")
			}
			selected, metadataErr := a.registry.Get(cmd.Context(), a.profileName)
			metadataExists := metadataErr == nil
			if metadataErr != nil && !errors.Is(metadataErr, profile.ErrNotFound) {
				return translateLocal(metadataErr, a.profileName)
			}
			credentialExists, err := a.store.Exists(cmd.Context(), a.profileName)
			if err != nil {
				return translateLocal(err, a.profileName)
			}
			switch {
			case metadataExists && credentialExists:
				state := "ready"
				if selected.ExpiresAt != nil && !a.now().Before(*selected.ExpiresAt) {
					state = "expired"
				}
				view := newProfileView(selected, state)
				if err := a.out.Validate(view); err != nil {
					return err
				}
				if check && state != "expired" {
					reader, checked, err := a.client(cmd.Context())
					if err != nil {
						return err
					}
					if err := reader.VerifyRequiredAccess(cmd.Context()); err != nil {
						return err
					}
					checkedState := "verified"
					if checked.HasCapability(profile.CapabilityPageWrite) {
						checkedState = "verified_reads_write_declared"
					}
					return a.out.Success(newProfileView(checked, checkedState))
				}
				return a.out.Success(view)
			case metadataExists:
				return a.out.Success(newProfileView(selected, "metadata_only"))
			case credentialExists:
				return a.out.Success(profileView{Name: a.profileName, State: "orphaned_credential"})
			default:
				return errx.NotFound("profile", a.profileName, nil)
			}
		},
	}
	command.Flags().BoolVar(&check, "check", false, "verify the three required read operations against Confluence")
	return command
}

func (a *App) newAuthLogoutCommand() *cobra.Command {
	return &cobra.Command{
		Use: "logout", Short: "Delete one exact credential and its profile metadata", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.dryRun {
				return errx.Usage("--dry-run is not supported by auth logout")
			}
			if len(a.fields) > 0 {
				return errx.Usage("--fields is not supported by auth logout")
			}
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if !a.assumeYes {
				return errx.ConfirmRequired("auth logout")
			}
			if a.registry == nil {
				return errx.Internal("profile registry is unavailable")
			}
			if err := auth.Logout(cmd.Context(), a.store, a.registry, a.profileName); err != nil {
				return translateLocal(err, a.profileName)
			}
			return a.out.Success(map[string]any{"profile": a.profileName, "removed": true})
		},
	}
}

func (a *App) newSkillsCommand() *cobra.Command {
	command := commandGroup("skills", "Install the canonical Confluence Agent Skill")
	command.AddCommand(a.newSkillsActionCommand("install"), a.newSkillsActionCommand("uninstall"))
	return command
}

func (a *App) newSkillsActionCommand(action string) *cobra.Command {
	var providerValue, scopeValue, projectDir, dest string
	command := &cobra.Command{
		Use: action, Short: action + " the Confluence Agent Skill for Codex and/or Claude Code", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(a.fields) > 0 {
				return errx.Usage("--fields is not supported by skills %s", action)
			}
			if providerValue == "" {
				return errx.Usage("--provider is required: codex, claude, or all")
			}
			provider, err := skills.ParseProvider(providerValue)
			if err != nil {
				return err
			}
			scope, err := skills.ParseScope(scopeValue)
			if err != nil {
				return err
			}
			options := skills.Options{Provider: provider, Scope: scope, ProjectDir: projectDir, Dest: dest, Confirmed: a.assumeYes, DryRun: a.dryRun}
			var results []skills.Result
			if action == "install" {
				results, err = skills.Install(cmd.Context(), options)
			} else {
				results, err = skills.Uninstall(cmd.Context(), options)
			}
			if err != nil {
				return err
			}
			return a.out.Success(results)
		},
	}
	flags := command.Flags()
	flags.StringVar(&providerValue, "provider", "", "target provider: codex, claude, or all")
	flags.StringVar(&scopeValue, "scope", string(skills.ScopeUser), "install scope: user or project")
	flags.StringVar(&projectDir, "project-dir", ".", "project directory for project scope")
	flags.StringVar(&dest, "dest", "", "explicit skills root for one provider")
	return command
}

func (a *App) newSpacesCommand() *cobra.Command {
	command := commandGroup("spaces", "Read Confluence spaces")
	command.AddCommand(a.newSpacesListCommand(), a.newSpacesGetCommand())
	return command
}

func (a *App) newSpacesListCommand() *cobra.Command {
	var limit int
	var cursor string
	command := &cobra.Command{
		Use: "list", Short: "Read one bounded space page", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			page, err := client.ListSpaces(cmd.Context(), confluence.ListOptions{Limit: limit, Cursor: cursor})
			if err != nil {
				return err
			}
			return a.out.SuccessPage(page.Results, page.NextCursor != "", page.NextCursor)
		},
	}
	command.Flags().IntVar(&limit, "limit", 25, "maximum spaces in this page (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from meta.next_cursor")
	return command
}

func (a *App) newSpacesGetCommand() *cobra.Command {
	return &cobra.Command{
		Use: "get SPACE_ID", Short: "Read one space by exact numeric ID", Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			space, err := client.GetSpace(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return a.out.Success(space)
		},
	}
}

func (a *App) newSearchCommand() *cobra.Command {
	var cql, cursor string
	var limit int
	command := &cobra.Command{
		Use: "search", Short: "Run one bounded CQL content search", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			page, err := client.Search(cmd.Context(), cql, confluence.ListOptions{Limit: limit, Cursor: cursor})
			if err != nil {
				return err
			}
			return a.out.SuccessPage(page.Results, page.NextCursor != "", page.NextCursor)
		},
	}
	command.Flags().StringVar(&cql, "cql", "", "bounded Confluence Query Language expression")
	command.Flags().IntVar(&limit, "limit", 25, "maximum results in this page (1-100)")
	command.Flags().StringVar(&cursor, "cursor", "", "opaque cursor from meta.next_cursor")
	return command
}

func (a *App) newPagesCommand() *cobra.Command {
	command := commandGroup("pages", "Read and safely write Confluence pages")
	command.AddCommand(a.newPagesListCommand(), a.newPagesGetCommand(), a.newPagesCreateCommand(), a.newPagesUpdateCommand())
	return command
}

func (a *App) newPagesListCommand() *cobra.Command {
	var spaceID, status, title, cursor string
	var limit int
	command := &cobra.Command{
		Use: "list", Short: "Read one bounded page collection", Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			page, err := client.ListPages(cmd.Context(), confluence.PageListOptions{
				ListOptions: confluence.ListOptions{Limit: limit, Cursor: cursor}, SpaceID: spaceID, Status: status, Title: title,
			})
			if err != nil {
				return err
			}
			return a.out.SuccessPage(page.Results, page.NextCursor != "", page.NextCursor)
		},
	}
	flags := command.Flags()
	flags.StringVar(&spaceID, "space-id", "", "optional exact numeric space ID")
	flags.StringVar(&status, "status", "current", "page status: current, archived, or draft")
	flags.StringVar(&title, "title", "", "optional exact page title filter")
	flags.IntVar(&limit, "limit", 25, "maximum pages in this page (1-100)")
	flags.StringVar(&cursor, "cursor", "", "opaque cursor from meta.next_cursor")
	return command
}

func (a *App) newPagesGetCommand() *cobra.Command {
	var bodyFormat string
	command := &cobra.Command{
		Use: "get PAGE_ID", Short: "Read one page by exact numeric ID", Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client(cmd.Context())
			if err != nil {
				return err
			}
			page, err := client.GetPage(cmd.Context(), args[0], bodyFormat)
			if err != nil {
				return err
			}
			return a.out.Success(page)
		},
	}
	command.Flags().StringVar(&bodyFormat, "body-format", "none", "body format: none, storage, view, or atlas_doc_format")
	return command
}

func commandGroup(use, short string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return errx.Usage("%s needs a subcommand", cmd.CommandPath())
		},
	}
}
