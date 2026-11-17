package cli

import (
	"context"
	"strings"

	"github.com/mminichino/cbctl/internal/capella"
	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/spf13/cobra"
)

// withCapellaSDK connects the legacy backend with cfg and runs fn on the SDK connection.
func withCapellaSDK(ctx context.Context, cfg *config.Config, fn func(*server.Server, *capella.Backend) error) error {
	backend := newLegacyBackend()
	cluster, cleanup, _, _, err := backend.Connect(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	s := server.New()
	s.Cluster = cluster
	s.Config = cfg
	return fn(s, backend)
}

func printBool(ok bool) {
	if ok {
		logging.Println("true")
	} else {
		logging.Println("false")
	}
}

func newCapellaScopeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scope",
		Short: "Capella scope operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCapellaScopeCreateCmd())
	cmd.AddCommand(newCapellaScopeExistsCmd())
	cmd.AddCommand(newCapellaScopeListCmd())
	cmd.AddCommand(newCapellaScopeAddCmd())
	cmd.AddCommand(newCapellaScopeDeleteCmd())
	return cmd
}

func newCapellaScopeCreateCmd() *cobra.Command {
	var (
		cf     capellaFlags
		bucket string
	)
	cmd := &cobra.Command{
		Use:   "create CLUSTER BUCKET SCOPE",
		Short: "Add a scope (capellactl spelling of 'scope add')",
		Long: "'scope create CLUSTER BUCKET SCOPE' is capellactl's spelling of 'scope add'. The pre-port form\n" +
			"'scope create SCOPE --bucket B --database CLUSTER' connects with the SDK and still works.",
		Args: cobra.RangeArgs(1, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			if capellactlArity("scope", args) {
				return forwardToAdd(cmd, args)
			}
			if len(args) != 1 || bucket == "" {
				return usageError("run 'cbctl capella scope add CLUSTER BUCKET SCOPE'",
					"'scope create' takes CLUSTER BUCKET SCOPE (or SCOPE --bucket B --database CLUSTER)")
			}
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
			name := args[0]
			err = withCapellaSDK(cmd.Context(), cfg, func(s *server.Server, _ *capella.Backend) error {
				return s.CreateScope(bucket, name)
			})
			if err != nil {
				return failure("Failed to create scope", err)
			}
			logging.Info("Scope %q created in bucket %q", name, bucket)
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name (pre-port form)")
	return cmd
}

func newCapellaScopeExistsCmd() *cobra.Command {
	var (
		cf     capellaFlags
		bucket string
	)
	cmd := &cobra.Command{
		Use:   "exists [name]",
		Short: "Print whether a Capella scope exists",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
			err = withCapellaSDK(cmd.Context(), cfg, func(s *server.Server, _ *capella.Backend) error {
				ok, err := s.ScopeExists(bucket, args[0])
				if err != nil {
					return err
				}
				printBool(ok)
				return nil
			})
			if err != nil {
				return failure("", err)
			}
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name")
	_ = cmd.MarkFlagRequired("bucket")
	return cmd
}

func newCapellaCollectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collection",
		Short: "Capella collection operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCapellaCollectionCreateCmd())
	cmd.AddCommand(newCapellaCollectionExistsCmd())
	cmd.AddCommand(newCapellaCollectionListCmd())
	cmd.AddCommand(newCapellaCollectionAddCmd())
	cmd.AddCommand(newCapellaCollectionUpdateCmd())
	cmd.AddCommand(newCapellaCollectionDeleteCmd())
	return cmd
}

func newCapellaCollectionCreateCmd() *cobra.Command {
	var (
		cf     capellaFlags
		bucket string
		scope  string
	)
	cmd := &cobra.Command{
		Use:   "create CLUSTER BUCKET SCOPE COLLECTION",
		Short: "Add a collection (capellactl spelling of 'collection add')",
		Long: "'collection create CLUSTER BUCKET SCOPE COLLECTION' is capellactl's spelling of 'collection add'. The\n" +
			"pre-port form 'collection create NAME --bucket B --scope S --database CLUSTER' still works.",
		Args: cobra.RangeArgs(1, 4),
		RunE: func(cmd *cobra.Command, args []string) error {
			if capellactlArity("collection", args) {
				return forwardToAdd(cmd, args)
			}
			if len(args) != 1 || bucket == "" || scope == "" {
				return usageError("run 'cbctl capella collection add CLUSTER BUCKET SCOPE COLLECTION'",
					"'collection create' takes CLUSTER BUCKET SCOPE COLLECTION (or NAME --bucket B --scope S --database CLUSTER)")
			}
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
			name := args[0]
			err = withCapellaSDK(cmd.Context(), cfg, func(s *server.Server, _ *capella.Backend) error {
				return s.CreateCollection(bucket, scope, name)
			})
			if err != nil {
				return failure("Failed to create collection", err)
			}
			logging.Info("Collection %q created in bucket %q scope %q", name, bucket, scope)
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name (pre-port form)")
	cmd.Flags().StringVarP(&scope, "scope", "s", "", "Parent scope name (pre-port form)")
	return cmd
}

func newCapellaCollectionExistsCmd() *cobra.Command {
	var (
		cf     capellaFlags
		bucket string
		scope  string
	)
	cmd := &cobra.Command{
		Use:   "exists [name]",
		Short: "Print whether a Capella collection exists",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
			err = withCapellaSDK(cmd.Context(), cfg, func(s *server.Server, _ *capella.Backend) error {
				ok, err := s.CollectionExists(bucket, scope, args[0])
				if err != nil {
					return err
				}
				printBool(ok)
				return nil
			})
			if err != nil {
				return failure("", err)
			}
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name")
	cmd.Flags().StringVarP(&scope, "scope", "s", "", "Parent scope name")
	_ = cmd.MarkFlagRequired("bucket")
	_ = cmd.MarkFlagRequired("scope")
	return cmd
}

// syncOnlyFlags are flags of capellactl's "import" (cbctl's "capella sync"). They are registered
// hidden on "capella import" only so a capellactl invocation gets the sync hint, not
// "unknown flag".
var syncOnlyFlags = []string{"cluster", "cluster-id", "file", "full", "stdout", "dry-run"}

const syncHint = "capellactl's 'import' is 'cbctl capella sync'; run 'cbctl capella sync --help'"

func newCapellaImportCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "import JSONL-FILE BUCKET.SCOPE.COLLECTION",
		Short: "Import JSON Lines into an empty Capella collection",
		Long: "Import a JSON Lines file into an empty collection over the SDK.\n\n" +
			"To write capella.yaml from what exists in Capella (capellactl's 'import'), use 'cbctl capella sync'.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, name := range syncOnlyFlags {
				if cmd.Flags().Changed(name) {
					return usageError(syncHint, "--%s is a 'sync' flag", name)
				}
			}
			if len(args) == 0 {
				return usageError(syncHint, "'capella import' takes JSONL-FILE BUCKET.SCOPE.COLLECTION")
			}
			if len(args) != 2 {
				return usageError("run 'cbctl capella import FILE BUCKET.SCOPE.COLLECTION'", "accepts 2 arg(s), received %d", len(args))
			}
			path := args[0]
			parts := strings.Split(args[1], ".")
			if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
				return usageError("write the keyspace as bucket.scope.collection", "keyspace must use bucket.scope.collection format")
			}
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
			bucket, scope, collection := parts[0], parts[1], parts[2]
			err = withCapellaSDK(cmd.Context(), cfg, func(s *server.Server, _ *capella.Backend) error {
				if _, err := s.EnsureCollection(bucket, scope, collection); err != nil {
					return err
				}
				empty, err := s.CollectionIsEmpty(bucket, scope, collection)
				if err != nil {
					return err
				}
				if !empty {
					logging.Warning("Collection not empty")
					return nil
				}
				imported, err := s.PopulateCollection(cmd.Context(), path, bucket, scope, collection)
				if err != nil {
					return err
				}
				logging.Info("Imported %d documents", imported)
				return nil
			})
			if err != nil {
				return failure("Failed to import JSON Lines file", err)
			}
			return nil
		},
	}
	cf.addTo(cmd)
	f := cmd.Flags()
	f.String("cluster", "", "")
	f.String("cluster-id", "", "")
	f.String("file", "", "")
	f.Bool("full", false, "")
	f.Bool("stdout", false, "")
	f.Bool("dry-run", false, "")
	for _, name := range syncOnlyFlags {
		_ = f.MarkHidden(name)
	}
	return cmd
}

// newLegacyBackend builds the pre-port Capella backend with the invocation's clock, so legacy
// cluster waits run on the same (injectable) clock as the managed commands.
func newLegacyBackend() *capella.Backend {
	b := capella.NewBackend()
	b.Clock = clockFactory()
	return b
}
