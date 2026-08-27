// Package cli wires confluence-cli's command tree. HTTP and Keychain
// implementations remain behind injected package boundaries.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/abigotado/confluence-cli/internal/auth"
	"github.com/abigotado/confluence-cli/internal/confluence"
	"github.com/abigotado/confluence-cli/internal/errx"
	"github.com/abigotado/confluence-cli/internal/output"
	"github.com/abigotado/confluence-cli/internal/profile"
	"github.com/abigotado/confluence-cli/internal/writepolicy"
	"github.com/spf13/cobra"
)

const defaultTimeout = 30 * time.Second

type profileRegistry interface {
	WithProfileLock(context.Context, string, func() error) error
	List(context.Context) ([]profile.Profile, error)
	Get(context.Context, string) (profile.Profile, error)
	Add(context.Context, profile.Profile) error
	Put(context.Context, profile.Profile) error
	Remove(context.Context, string) error
}

type confluenceReader interface {
	ListSpaces(context.Context, confluence.ListOptions) (confluence.PageResult[confluence.Spaces], error)
	GetSpace(context.Context, string) (confluence.Space, error)
	ListPages(context.Context, confluence.PageListOptions) (confluence.PageResult[confluence.Pages], error)
	GetPage(context.Context, string, string) (confluence.Page, error)
	Search(context.Context, string, confluence.ListOptions) (confluence.PageResult[confluence.SearchResults], error)
	VerifyRequiredAccess(context.Context) error
}

type confluenceMutationClient interface {
	confluenceReader
	CreatePage(context.Context, confluence.CreatePageInput) (confluence.Page, error)
	UpdatePage(context.Context, confluence.UpdatePageInput) (confluence.Page, error)
}

// App contains only per-invocation state and injectable boundaries.
type App struct {
	registry profileRegistry
	store    auth.CredentialAccessStore
	policies *writepolicy.Registry

	newClient       func(profile.Profile, auth.Credential, *slog.Logger) (confluenceReader, error)
	discoverCloudID func(context.Context, string) (string, error)
	now             func() time.Time

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	profileName string
	format      string
	jsonAlias   bool
	fields      []string
	timeout     time.Duration
	verbose     bool
	assumeYes   bool
	dryRun      bool

	out     *output.Writer
	log     *slog.Logger
	cancels []context.CancelFunc
}

// NewApp builds an App with production boundaries.
func NewApp() *App {
	registry, registryErr := profile.NewDefaultRegistry()
	policies, policyErr := writepolicy.NewDefaultRegistry()
	app := &App{
		store: auth.KeychainStore{}, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now,
	}
	if registryErr == nil {
		app.registry = registry
	}
	if policyErr == nil {
		app.policies = policies
	}
	app.newClient = func(p profile.Profile, credential auth.Credential, logger *slog.Logger) (confluenceReader, error) {
		return confluence.New(confluence.Credential{Email: p.Email, Token: credential.Token, CloudID: p.CloudID}, confluence.WithLogger(logger))
	}
	app.discoverCloudID = func(ctx context.Context, site string) (string, error) {
		return confluence.DiscoverCloudID(ctx, site, nil)
	}
	return app
}

//go:generate go run github.com/abigotado/confluence-cli/tools/gencommands

// NewRootCommand assembles the public command surface.
func (a *App) NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use: "confluence-cli", Short: "Use Confluence Cloud safely from the command line and AI agents",
		SilenceUsage: true, SilenceErrors: true,
		Args: usageArgs(func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return nil
		}),
		RunE: func(cmd *cobra.Command, _ []string) error { return errx.Usage("%s needs a command", cmd.CommandPath()) },
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	flags := root.PersistentFlags()
	flags.StringVar(&a.profileName, "profile", "", "named Confluence profile (required for every network command)")
	flags.StringVarP(&a.format, "output", "o", "", "output format: text, json, or raw")
	flags.BoolVar(&a.jsonAlias, "json", false, "emit the JSON envelope")
	_ = flags.MarkHidden("json")
	flags.StringSliceVar(&a.fields, "fields", nil, "comma-separated fields to emit")
	flags.DurationVar(&a.timeout, "timeout", defaultTimeout, "abort the command after this duration")
	flags.BoolVarP(&a.verbose, "verbose", "v", false, "write redacted request activity to stderr")
	flags.BoolVar(&a.assumeYes, "yes", false, "confirm a supported mutation")
	flags.BoolVar(&a.dryRun, "dry-run", false, "preview a supported mutation without applying it")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return errx.Usage("%v", err) })
	root.PersistentPreRunE = a.setup
	root.AddCommand(
		a.newVersionCommand(), a.newContractCommand(), a.newAuthCommand(), a.newSkillsCommand(),
		a.newSpacesCommand(), a.newSearchCommand(), a.newPagesCommand(),
	)
	return root
}

func (a *App) setup(cmd *cobra.Command, _ []string) error {
	if a.jsonAlias && a.format != "" && a.format != string(output.FormatJSON) {
		return errx.Usage("--json cannot be combined with --output %s", a.format)
	}
	format := defaultFormat(a.stdout)
	if a.jsonAlias {
		format = output.FormatJSON
	} else if a.format != "" {
		parsed, err := output.ParseFormat(a.format)
		if err != nil {
			return err
		}
		format = parsed
	}
	if format == output.FormatRaw && len(a.fields) > 0 {
		return errx.Usage("--fields cannot be combined with --output raw")
	}
	a.out = &output.Writer{Format: format, Fields: a.fields, Out: a.stdout, Err: a.stderr}
	level := slog.LevelWarn
	if a.verbose {
		level = slog.LevelDebug
	}
	a.log = slog.New(slog.NewTextHandler(a.stderr, &slog.HandlerOptions{Level: level}))
	if a.timeout <= 0 {
		return errx.Usage("--timeout must be greater than zero")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), a.timeout)
	a.cancels = append(a.cancels, cancel)
	cmd.SetContext(ctx)
	return nil
}

func defaultFormat(writer io.Writer) output.Format {
	if file, ok := writer.(*os.File); ok {
		return output.DefaultFormat(file)
	}
	return output.FormatJSON
}

func (a *App) client(ctx context.Context) (confluenceReader, profile.Profile, error) {
	if a.profileName == "" {
		return nil, profile.Profile{}, errx.ProfileRequired()
	}
	if a.registry == nil {
		return nil, profile.Profile{}, errx.Internal("profile registry is unavailable")
	}
	var selected profile.Profile
	var reader confluenceReader
	err := a.registry.WithProfileLock(ctx, a.profileName, func() error {
		var loadErr error
		selected, loadErr = a.registry.Get(ctx, a.profileName)
		if loadErr != nil {
			return translateLocal(loadErr, a.profileName)
		}
		if selected.ExpiresAt != nil && !a.now().Before(*selected.ExpiresAt) {
			return errx.Auth("TOKEN_EXPIRED", "the API token for profile %q is expired", selected.Name)
		}
		credential, loadErr := a.store.Load(ctx, selected.Name)
		if loadErr != nil {
			return translateLocal(loadErr, selected.Name)
		}
		if loadErr = auth.ValidateCredentialBinding(credential, selected); loadErr != nil {
			return translateLocal(loadErr, selected.Name)
		}
		reader, loadErr = a.newClient(selected, credential, a.log)
		return loadErr
	})
	if err != nil {
		return nil, profile.Profile{}, err
	}
	a.out.WithContext(selected.Name, selected.Site)
	return reader, selected, nil
}

func usageArgs(validator cobra.PositionalArgs) cobra.PositionalArgs {
	if validator == nil {
		validator = cobra.ArbitraryArgs
	}
	return func(cmd *cobra.Command, args []string) error {
		if err := validator(cmd, args); err != nil {
			var typed *errx.Error
			if errors.As(err, &typed) {
				return err
			}
			return errx.Usage("%v", err)
		}
		return nil
	}
}

func translateLocal(err error, name string) error {
	if err == nil {
		return nil
	}
	var typed *errx.Error
	if errors.As(err, &typed) {
		return err
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errx.Translate(err)
	case errors.Is(err, profile.ErrProfileRequired):
		return errx.ProfileRequired()
	case errors.Is(err, profile.ErrInvalidProfile):
		return errx.Usage("profile metadata is invalid")
	case errors.Is(err, profile.ErrNotFound):
		return errx.NotFound("profile", name, nil)
	case errors.Is(err, profile.ErrAlreadyExists), errors.Is(err, auth.ErrOverwriteConfirmationRequired):
		return errx.ConfirmRequired("auth login overwrite")
	case errors.Is(err, profile.ErrCorruptRegistry), errors.Is(err, profile.ErrInsecurePermissions):
		return errx.Internal("profile registry cannot be used safely")
	case errors.Is(err, auth.ErrNotFound):
		return errx.Auth("CREDENTIAL_NOT_FOUND", "no stored credential exists for profile %q", name)
	case errors.Is(err, auth.ErrCredentialBindingMismatch):
		return errx.Auth("CREDENTIAL_BINDING_MISMATCH", "the stored credential does not belong to the current profile generation").
			WithHint("re-run auth login for this exact profile; existing write allowlists will remain stale until explicitly replaced")
	case errors.Is(err, auth.ErrUnsupported):
		return errx.Auth("KEYCHAIN_UNSUPPORTED", "this build has no supported native macOS credential store")
	case errors.Is(err, auth.ErrKeychainMigrationRequired):
		return errx.Auth("KEYCHAIN_MIGRATION_REQUIRED", "stored Keychain access for profile %q requires migration", name).
			WithHint("run 'confluence-cli auth migrate-keychain --profile %s --yes'", name)
	case errors.Is(err, auth.ErrInteractionNotAllowed):
		return errx.Auth("KEYCHAIN_INTERACTION_REQUIRED", "Keychain access for profile %q requires user interaction", name)
	case errors.Is(err, auth.ErrKeychainEntryNotFound):
		return errx.Auth("KEYCHAIN_ENTRY_NOT_FOUND", "no stored Keychain entry exists for profile %q", name).
			WithHint("run 'confluence-cli auth login --profile %s --token-stdin'", name)
	case errors.Is(err, auth.ErrKeychainMigrationBlocked):
		return errx.Auth("KEYCHAIN_MIGRATION_BLOCKED", "Keychain access migration for profile %q requires an interactive macOS session", name).
			WithHint("run from an interactive macOS session: 'confluence-cli auth migrate-keychain --profile %s --yes'", name)
	case errors.Is(err, auth.ErrKeychainMigrationCanceled):
		return errx.Auth("KEYCHAIN_MIGRATION_CANCELED", "Keychain access migration was canceled for profile %q", name).
			WithHint("retry 'confluence-cli auth migrate-keychain --profile %s --yes'", name)
	case errors.Is(err, auth.ErrKeychainMigrationUnavailable):
		return errx.Auth("KEYCHAIN_MIGRATION_UNAVAILABLE", "Keychain access migration is unavailable for profile %q", name).
			WithHint("use a cgo-enabled macOS build, then retry 'confluence-cli auth migrate-keychain --profile %s --yes'", name)
	case errors.Is(err, auth.ErrInvalidToken):
		return errx.Usage("API token input is invalid")
	default:
		return errx.Internal("local operation failed without exposing credential details")
	}
}

// Run executes one command tree and returns its process status.
func (a *App) Run(ctx context.Context, root *cobra.Command, args []string) (code errx.Code) {
	defer func() {
		for _, cancel := range a.cancels {
			cancel()
		}
		if recover() != nil {
			if a.out == nil {
				a.out = &output.Writer{Format: defaultFormat(a.stdout), Out: a.stdout, Err: a.stderr}
			}
			code = a.out.Failure(errx.Internal("confluence-cli stopped after an unexpected internal failure"))
		}
	}()
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return errx.CodeOK
	}
	if a.out == nil {
		format := defaultFormat(a.stdout)
		if a.jsonAlias {
			format = output.FormatJSON
		} else if parsed, parseErr := output.ParseFormat(a.format); a.format != "" && parseErr == nil {
			format = parsed
		}
		a.out = &output.Writer{Format: format, Out: a.stdout, Err: a.stderr}
	}
	return a.out.Failure(translateLocal(err, a.profileName))
}

// Execute runs confluence-cli with signal cancellation.
func Execute(args []string) errx.Code {
	app := NewApp()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, app.NewRootCommand(), args)
}
