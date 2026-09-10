package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/spf13/cobra"
)

func newKeyspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keyspace",
		Short: "Keyspace operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newKeyspaceCreateCmd())
	return cmd
}

func newKeyspaceCreateCmd() *cobra.Command {
	var (
		cf       connFlags
		quota    int
		replicas int
	)
	cmd := &cobra.Command{
		Use:   "create [bucket.scope.collection]",
		Short: "Create a bucket, scope, and collection keyspace",
		Long: `Create the bucket, scope, and collection for a keyspace.

Scope or collection named _default are skipped (they already exist).
The bucket is always ensured.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			bucket, scope, collection, err := parseKeyspace(args[0])
			if err != nil {
				return err
			}
			cfg := cf.config()
			cfg.Bucket = bucket
			cfg.Scope = scope
			cfg.Collection = collection

			s := server.New()
			defer s.Disconnect()
			if err := s.Connect(context.Background(), cfg); err != nil {
				logging.Error("Failed to create keyspace: %v", err)
				exitErr()
			}
			if err := s.CreateBucket(bucket, quota, replicas); err != nil {
				logging.Error("Failed to create keyspace: %v", err)
				exitErr()
			}
			// CreateScope / CreateCollection no-op when name is _default.
			if err := s.CreateScope(bucket, scope); err != nil {
				logging.Error("Failed to create keyspace: %v", err)
				exitErr()
			}
			if err := s.CreateCollection(bucket, scope, collection); err != nil {
				logging.Error("Failed to create keyspace: %v", err)
				exitErr()
			}
			logging.Info("Keyspace %q created", args[0])
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().IntVar(&quota, "quota", 128, "RAM quota in MiB for the bucket")
	cmd.Flags().IntVar(&replicas, "replicas", 0, "Number of replicas for the bucket")
	return cmd
}

func parseKeyspace(keyspace string) (bucket, scope, collection string, err error) {
	parts := strings.Split(keyspace, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("keyspace must use bucket.scope.collection format")
	}
	return parts[0], parts[1], parts[2], nil
}
