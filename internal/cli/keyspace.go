package cli

import (
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/spf13/cobra"
)

func newBucketCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bucket",
		Short: "Bucket operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newBucketCreateCmd())
	cmd.AddCommand(newBucketExistsCmd())
	return cmd
}

func newBucketCreateCmd() *cobra.Command {
	var (
		cf       connFlags
		quota    int
		replicas int
	)
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a bucket",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg := cf.config()
			cfg.Bucket = name
			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(cmd.Context(), cfg); err != nil {
				return failure("Failed to create bucket", err)
			}
			if err := s.CreateBucket(name, quota, replicas); err != nil {
				return failure("Failed to create bucket", err)
			}
			logging.Info("Bucket %q created", name)
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().IntVar(&quota, "quota", 128, "RAM quota in MiB")
	cmd.Flags().IntVar(&replicas, "replicas", 0, "Number of replicas")
	return cmd
}

func newBucketExistsCmd() *cobra.Command {
	var cf connFlags
	cmd := &cobra.Command{
		Use:   "exists [name]",
		Short: "Print whether a bucket exists",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(cmd.Context(), cf.config()); err != nil {
				return failure("", err)
			}
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

func newScopeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scope",
		Short: "Scope operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newScopeCreateCmd())
	cmd.AddCommand(newScopeExistsCmd())
	return cmd
}

func newScopeCreateCmd() *cobra.Command {
	var (
		cf     connFlags
		bucket string
	)
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg := cf.config()
			cfg.Bucket = bucket
			cfg.Scope = name
			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(cmd.Context(), cfg); err != nil {
				return failure("Failed to create scope", err)
			}
			if err := s.CreateScope(bucket, name); err != nil {
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

func newScopeExistsCmd() *cobra.Command {
	var (
		cf     connFlags
		bucket string
	)
	cmd := &cobra.Command{
		Use:   "exists [name]",
		Short: "Print whether a scope exists",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(cmd.Context(), cf.config()); err != nil {
				return failure("", err)
			}
			ok, err := s.ScopeExists(bucket, args[0])
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
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name")
	_ = cmd.MarkFlagRequired("bucket")
	return cmd
}

func newCollectionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collection",
		Short: "Collection operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newCollectionCreateCmd())
	cmd.AddCommand(newCollectionExistsCmd())
	return cmd
}

func newCollectionCreateCmd() *cobra.Command {
	var (
		cf     connFlags
		bucket string
		scope  string
	)
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a collection",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg := cf.config()
			cfg.Bucket = bucket
			cfg.Scope = scope
			cfg.Collection = name
			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(cmd.Context(), cfg); err != nil {
				return failure("Failed to create collection", err)
			}
			if err := s.CreateCollection(bucket, scope, name); err != nil {
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

func newCollectionExistsCmd() *cobra.Command {
	var (
		cf     connFlags
		bucket string
		scope  string
	)
	cmd := &cobra.Command{
		Use:   "exists [name]",
		Short: "Print whether a collection exists",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(cmd.Context(), cf.config()); err != nil {
				return failure("", err)
			}
			ok, err := s.CollectionExists(bucket, scope, args[0])
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
	cmd.Flags().StringVarP(&bucket, "bucket", "b", "", "Parent bucket name")
	cmd.Flags().StringVarP(&scope, "scope", "s", "", "Parent scope name")
	_ = cmd.MarkFlagRequired("bucket")
	_ = cmd.MarkFlagRequired("scope")
	return cmd
}

func newImportCmd() *cobra.Command {
	var cf connFlags
	cmd := &cobra.Command{
		Use:   "import [jsonl-file] [bucket.scope.collection]",
		Short: "Import JSON Lines into an empty collection",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			bucket, scope, collection, err := parseKeyspace(args[1])
			if err != nil {
				return err
			}
			cfg := cf.config()
			cfg.Bucket = bucket
			cfg.Scope = scope
			cfg.Collection = collection
			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(cmd.Context(), cfg); err != nil {
				return failure("Failed to import JSON Lines file", err)
			}
			if _, err := s.EnsureCollection(bucket, scope, collection); err != nil {
				return failure("Failed to import JSON Lines file", err)
			}
			empty, err := s.CollectionIsEmpty(bucket, scope, collection)
			if err != nil {
				return failure("Failed to import JSON Lines file", err)
			}
			if !empty {
				logging.Warning("Collection not empty")
				return nil
			}
			imported, err := s.PopulateCollection(cmd.Context(), path, bucket, scope, collection)
			if err != nil {
				return failure("Failed to import JSON Lines file", err)
			}
			logging.Info("Imported %d documents", imported)
			return nil
		},
	}
	cf.addTo(cmd)
	return cmd
}
