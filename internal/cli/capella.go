package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/capella"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/spf13/cobra"
)

func newCapellaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capella",
		Short: "Capella cloud operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
		// Applies --log-level/--quiet to every subcommand; no leaf defines its own persistent hook.
		PersistentPreRunE: capellaPersistentPreRun,
	}
	addCapellaGlobalFlags(cmd)
	cmd.AddCommand(newCapellaProjectCmd())
	cmd.AddCommand(newCapellaClusterCmd())
	cmd.AddCommand(newCapellaBucketCmd())
	cmd.AddCommand(newCapellaScopeCmd())
	cmd.AddCommand(newCapellaCollectionCmd())
	cmd.AddCommand(newCapellaCIDRCmd())
	cmd.AddCommand(newCapellaCredentialCmd())
	cmd.AddCommand(newCapellaImportCmd())
	cmd.AddCommand(newConfigureCmd())
	cmd.AddCommand(newCapellaJanitorCmd())
	cmd.AddCommand(newCapellaValidateCmd())
	cmd.AddCommand(newCapellaPlanCmd())
	cmd.AddCommand(newCapellaApplyCmd())
	cmd.AddCommand(newCapellaEnvDestroyCmd())
	cmd.AddCommand(newCapellaDriftCmd())
	cmd.AddCommand(newCapellaSyncCmd())
	cmd.AddCommand(newCapellaInitCmd())
	cmd.AddCommand(newCapellaGuideCmd())
	installManagedModes(cmd)
	return cmd
}

func newCapellaClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Capella cluster operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCapellaClusterCreateCmd())
	cmd.AddCommand(newCapellaClusterExistsCmd())
	cmd.AddCommand(newCapellaClusterDestroyCmd())
	cmd.AddCommand(newCapellaClusterTestCmd())
	registerCapellaClusterCommands(cmd)
	return cmd
}

func newCapellaClusterCreateCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Capella cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := newLegacyBackend()
			created, err := backend.CreateCluster(cmd.Context(), cf.config())
			if err != nil {
				return failure("Failed to create cluster", err)
			}
			if !created {
				logging.Info("Cluster already configured")
				return nil
			}
			name := cf.database
			if name == "" {
				name = cf.databaseID
			}
			logging.Info("Cluster created on %s", name)
			return nil
		},
	}
	cf.addTo(cmd)
	return cmd
}

func newCapellaClusterExistsCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "exists",
		Short: "Print whether a Capella cluster exists",
		RunE: func(cmd *cobra.Command, args []string) error {
			ok, err := newLegacyBackend().ClusterExists(cmd.Context(), cf.config())
			if err != nil {
				return failure("", err)
			}
			if ok {
				logging.Println("true")
			} else {
				logging.Println("false")
			}
			return nil
		},
	}
	cf.addTo(cmd)
	return cmd
}

func newCapellaClusterDestroyCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy a Capella cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := newLegacyBackend()
			cfg := cf.config()
			// Ensure cluster handle is resolved via Connect path / exists lookup.
			exists, err := backend.ClusterExists(cmd.Context(), cfg)
			if err != nil {
				return failure("Failed to destroy cluster", err)
			}
			if !exists {
				logging.Warning("Cluster does not exist")
				return nil
			}
			if err := backend.DestroyCluster(cmd.Context(), cfg); err != nil {
				return failure("Failed to destroy cluster", err)
			}
			name := cf.database
			if name == "" {
				name = cf.databaseID
			}
			logging.Info("Cluster %q destroyed", name)
			return nil
		},
	}
	cf.addTo(cmd)
	return cmd
}

func newCapellaClusterTestCmd() *cobra.Command {
	var (
		cf     capellaFlags
		bucket string
	)
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Connect to Capella and verify KV put/get",
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := newLegacyBackend()
			cfg := cf.config()
			cluster, cleanup, _, _, err := backend.Connect(cmd.Context(), cfg)
			if err != nil {
				return failure("Cluster test failed", err)
			}
			defer cleanup()

			name := cf.database
			if name == "" {
				name = cf.databaseID
			}
			logging.Info("Connected to %s", name)

			s := server.New()
			s.Cluster = cluster
			s.Config = cfg
			if err := s.ClusterTest(bucket); err != nil {
				return failure("Cluster test failed", err)
			}
			logging.Info("Cluster test passed")
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Existing bucket for put/get only")
	return cmd
}

func newCapellaBucketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bucket",
		Short: "Capella bucket operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCapellaBucketCreateCmd())
	cmd.AddCommand(newCapellaBucketExistsCmd())
	cmd.AddCommand(newCapellaBucketListCmd())
	cmd.AddCommand(newCapellaBucketGetCmd())
	cmd.AddCommand(newCapellaBucketAddCmd())
	cmd.AddCommand(newCapellaBucketUpdateCmd())
	cmd.AddCommand(newCapellaBucketDeleteCmd())
	cmd.AddCommand(newCapellaBucketBackupCmd())
	return cmd
}

func newCapellaBucketCreateCmd() *cobra.Command {
	var (
		cf       capellaFlags
		quota    int
		replicas int
	)
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a Capella bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			backend := newLegacyBackend()
			cfg := cf.config()
			_, cleanup, _, _, err := backend.Connect(cmd.Context(), cfg)
			if err != nil {
				return failure("Failed to create bucket", err)
			}
			defer cleanup()
			if err := backend.CreateBucket(name, quota, replicas); err != nil {
				return failure("Failed to create bucket", err)
			}
			logging.Info("Bucket %q created", name)
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().IntVar(&quota, "quota", 128, "RAM quota in MiB")
	cmd.Flags().IntVar(&replicas, "replicas", 1, "Number of replicas")
	return cmd
}

func newCapellaBucketExistsCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "exists [name]",
		Short: "Print whether a Capella bucket exists",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := newLegacyBackend()
			cfg := cf.config()
			cluster, cleanup, _, _, err := backend.Connect(cmd.Context(), cfg)
			if err != nil {
				return failure("", err)
			}
			defer cleanup()
			s := server.New()
			s.Cluster = cluster
			ok, err := s.BucketExists(args[0])
			if err != nil {
				return failure("", err)
			}
			if ok {
				logging.Println("true")
			} else {
				logging.Println("false")
			}
			return nil
		},
	}
	cf.addTo(cmd)
	return cmd
}

func withCapellaSDK(ctx context.Context, cf capellaFlags, fn func(*server.Server, *capella.Backend) error) error {
	backend := newLegacyBackend()
	cfg := cf.config()
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
		Use:   "create [name]",
		Short: "Create a scope on Capella",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			err := withCapellaSDK(cmd.Context(), cf, func(s *server.Server, _ *capella.Backend) error {
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
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name")
	_ = cmd.MarkFlagRequired("bucket")
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
			err := withCapellaSDK(cmd.Context(), cf, func(s *server.Server, _ *capella.Backend) error {
				ok, err := s.ScopeExists(bucket, args[0])
				if err != nil {
					return err
				}
				if ok {
					logging.Println("true")
				} else {
					logging.Println("false")
				}
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
		Use:   "create [name]",
		Short: "Create a collection on Capella",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			err := withCapellaSDK(cmd.Context(), cf, func(s *server.Server, _ *capella.Backend) error {
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
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name")
	cmd.Flags().StringVarP(&scope, "scope", "s", "", "Parent scope name")
	_ = cmd.MarkFlagRequired("bucket")
	_ = cmd.MarkFlagRequired("scope")
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
			err := withCapellaSDK(cmd.Context(), cf, func(s *server.Server, _ *capella.Backend) error {
				ok, err := s.CollectionExists(bucket, scope, args[0])
				if err != nil {
					return err
				}
				if ok {
					logging.Println("true")
				} else {
					logging.Println("false")
				}
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

func newCapellaImportCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "import [jsonl-file] [bucket.scope.collection]",
		Short: "Import JSON Lines into an empty Capella collection",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			parts := strings.Split(args[1], ".")
			if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
				return fmt.Errorf("keyspace must use bucket.scope.collection format")
			}
			bucket, scope, collection := parts[0], parts[1], parts[2]
			err := withCapellaSDK(cmd.Context(), cf, func(s *server.Server, _ *capella.Backend) error {
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
	return cmd
}

// newLegacyBackend builds the pre-port Capella backend with the invocation's clock, so legacy
// cluster waits run on the same (injectable) clock as the managed commands.
func newLegacyBackend() *capella.Backend {
	b := capella.NewBackend()
	b.Clock = clockFactory()
	return b
}
