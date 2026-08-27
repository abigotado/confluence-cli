package cli

import (
	"github.com/abigotado/confluence-cli/internal/auth"
	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/output"
	"github.com/abigotado/confluence-cli/internal/profile"
	"github.com/spf13/cobra"
)

type keychainMigrationReceipt struct {
	Action         string `json:"action"`
	Profile        string `json:"profile"`
	KeychainAccess string `json:"keychain_access"`
	DryRun         bool   `json:"dry_run"`
	Applied        bool   `json:"applied"`
}

func (view keychainMigrationReceipt) Fields() []output.Field {
	return []output.Field{
		{Name: "action", Value: view.Action, Raw: view.Action},
		{Name: "profile", Value: view.Profile, Raw: view.Profile},
		{Name: "keychain_access", Value: view.KeychainAccess, Raw: view.KeychainAccess},
		{Name: "dry_run", Raw: view.DryRun},
		{Name: "applied", Raw: view.Applied},
	}
}

func (a *App) newAuthMigrateKeychainCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate-keychain",
		Short: "Make one existing Keychain entry stable across CLI rebuilds",
		Long: "Change only the access policy of the exact Keychain entry for --profile. " +
			"The operation never reads or rewrites the token and never contacts Confluence. " +
			"macOS may request authorization once; re-running it is safe.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.profileName == "" {
				return errx.ProfileRequired()
			}
			if err := profile.ValidateName(a.profileName); err != nil {
				return translateLocal(err, a.profileName)
			}
			receipt := keychainMigrationReceipt{
				Action: "auth migrate-keychain", Profile: a.profileName,
				KeychainAccess: "same_user_apps", DryRun: a.dryRun, Applied: !a.dryRun,
			}
			if err := a.out.Validate(receipt); err != nil {
				return err
			}
			if a.registry == nil {
				return errx.Internal("profile registry is unavailable")
			}
			if a.dryRun {
				if _, err := a.registry.Get(cmd.Context(), a.profileName); err != nil {
					return translateLocal(err, a.profileName)
				}
				return a.out.Success(receipt)
			}
			if !a.assumeYes {
				return errx.ConfirmRequired("auth migrate-keychain")
			}
			if err := auth.MigrateKeychain(cmd.Context(), a.store, a.registry, a.profileName); err != nil {
				return translateLocal(err, a.profileName)
			}
			return a.out.Success(receipt)
		},
	}
}
