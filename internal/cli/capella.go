package cli

import (
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
	}
	cmd.AddCommand(newCapellaClusterCmd())
	cmd.AddCommand(newCapellaBucketCmd())
	cmd.AddCommand(newCapellaScopeCmd())
	cmd.AddCommand(newCapellaCollectionCmd())
	cmd.AddCommand(newCapellaImportCmd())
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
	return cmd
}

func newCapellaClusterCreateCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a Capella cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			backend := capella.NewBackend()
			created, err := backend.CreateCluster(cf.config())
			if err != nil {
				logging.Error("Failed to create cluster: %v", err)
				exitErr()
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
			ok := capella.NewBackend().ClusterExists(cf.config())
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
			backend := capella.NewBackend()
			cfg := cf.config()
			// Ensure cluster handle is resolved via Connect path / exists lookup.
			if !backend.ClusterExists(cfg) {
				logging.Warning("Cluster does not exist")
				return nil
			}
			if err := backend.DestroyCluster(cfg); err != nil {
				logging.Error("Failed to destroy cluster: %v", err)
				exitErr()
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
			backend := capella.NewBackend()
			cfg := cf.config()
			cluster, cleanup, _, _, err := backend.Connect(cfg)
			if err != nil {
				logging.Error("Cluster test failed: %v", err)
				exitErr()
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
				logging.Error("Cluster test failed: %v", err)
				exitErr()
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
			backend := capella.NewBackend()
			cfg := cf.config()
			_, cleanup, _, _, err := backend.Connect(cfg)
			if err != nil {
				logging.Error("Failed to create bucket: %v", err)
				exitErr()
			}
			defer cleanup()
			if err := backend.CreateBucket(name, quota, replicas); err != nil {
				logging.Error("Failed to create bucket: %v", err)
				exitErr()
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
			backend := capella.NewBackend()
			cfg := cf.config()
			cluster, cleanup, _, _, err := backend.Connect(cfg)
			if err != nil {
				logging.Error("%v", err)
				exitErr()
			}
			defer cleanup()
			s := server.New()
			s.Cluster = cluster
			ok, err := s.BucketExists(args[0])
			if err != nil {
				logging.Error("%v", err)
				exitErr()
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

func withCapellaSDK(cf capellaFlags, fn func(*server.Server, *capella.Backend) error) error {
	backend := capella.NewBackend()
	cfg := cf.config()
	cluster, cleanup, _, _, err := backend.Connect(cfg)
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
			err := withCapellaSDK(cf, func(s *server.Server, _ *capella.Backend) error {
				return s.CreateScope(bucket, name)
			})
			if err != nil {
				logging.Error("Failed to create scope: %v", err)
				exitErr()
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
			err := withCapellaSDK(cf, func(s *server.Server, _ *capella.Backend) error {
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
				logging.Error("%v", err)
				exitErr()
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
			err := withCapellaSDK(cf, func(s *server.Server, _ *capella.Backend) error {
				return s.CreateCollection(bucket, scope, name)
			})
			if err != nil {
				logging.Error("Failed to create collection: %v", err)
				exitErr()
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
			err := withCapellaSDK(cf, func(s *server.Server, _ *capella.Backend) error {
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
				logging.Error("%v", err)
				exitErr()
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
			err := withCapellaSDK(cf, func(s *server.Server, _ *capella.Backend) error {
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
				imported, err := s.PopulateCollection(path, bucket, scope, collection)
				if err != nil {
					return err
				}
				logging.Info("Imported %d documents", imported)
				return nil
			})
			if err != nil {
				logging.Error("Failed to import JSON Lines file: %v", err)
				exitErr()
			}
			return nil
		},
	}
	cf.addTo(cmd)
	return cmd
}
