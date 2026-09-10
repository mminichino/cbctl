package cli

import (
	"fmt"
	"strings"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/models"
	"github.com/mminichino/cbctl/internal/rest"
	"github.com/mminichino/cbctl/internal/server"
	"github.com/spf13/cobra"
)

func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Cluster operations",
		Run:   func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}
	cmd.AddCommand(newClusterCreateCmd())
	cmd.AddCommand(newClusterExistsCmd())
	cmd.AddCommand(newClusterMapCmd())
	cmd.AddCommand(newClusterTestCmd())
	return cmd
}

func newClusterCreateCmd() *cobra.Command {
	var (
		cf       connFlags
		nodes    []string
		services string
		ram      int
		altAddr  string
		extAPI   bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create and initialize a Couchbase Server cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultServices := rest.ParseServices(services, rest.DefaultServerServices)
			if len(defaultServices) == 0 {
				return fmt.Errorf("at least one service is required")
			}

			var specs []models.NodeSpec
			if len(nodes) > 0 {
				if altAddr != "" {
					return fmt.Errorf("use #ALTERNATE in --node instead of --alternate-address when specifying multiple nodes")
				}
				for _, spec := range nodes {
					parsed, err := rest.ParseNodeSpec(spec, defaultServices, ram)
					if err != nil {
						return err
					}
					specs = append(specs, parsed)
				}
			} else {
				host := cf.host
				if host == "" {
					host = config.DefaultHostname
				}
				altHost, altPorts, err := rest.ParseAlternateFragment(altAddr)
				if err != nil {
					return err
				}
				specs = []models.NodeSpec{{
					Host:             host,
					Services:         defaultServices,
					RAMGiB:           ram,
					AlternateAddress: altHost,
					AlternatePorts:   altPorts,
				}}
			}

			if extAPI {
				hasAlt := false
				for _, s := range specs {
					if s.AlternateAddress != "" {
						hasAlt = true
						break
					}
				}
				if !hasAlt {
					return fmt.Errorf("--ext-api requires an alternate address on at least one node")
				}
			}

			primaryHost := specs[0].Host
			if extAPI && specs[0].AlternateAddress != "" {
				primaryHost = specs[0].AlternateAddress
			}
			cfg := cf.config()
			cfg.Hostname = primaryHost
			options := rest.BuildServerOptions(specs, extAPI)

			created, err := server.CreateCluster(cfg, options)
			if err != nil {
				logging.Error("Failed to create cluster: %v", err)
				exitErr()
			}
			if !created {
				logging.Info("Cluster already configured")
				return nil
			}
			hosts := make([]string, 0, len(specs))
			for _, s := range specs {
				hosts = append(hosts, s.Host)
			}
			logging.Info("Cluster created on %s", strings.Join(hosts, ", "))
			return nil
		},
	}
	cf.addTo(cmd)
	cmd.Flags().StringArrayVarP(&nodes, "node", "n", nil, "Node spec HOST[=SERVICES][@RAM][#ALTERNATE[;PORTMAP]]")
	cmd.Flags().StringVarP(&services, "services", "s", strings.Join(rest.DefaultServerServices, ","), "Default services when a node spec omits SERVICES")
	cmd.Flags().IntVar(&ram, "ram", config.DefaultRAMGiB, "Default RAM quota in GiB when a node spec omits @RAM")
	cmd.Flags().StringVarP(&altAddr, "alternate-address", "a", "", "External alternate address for a single-node cluster")
	cmd.Flags().BoolVar(&extAPI, "ext-api", false, "Use alternate address for management REST API calls")
	return cmd
}

func newClusterExistsCmd() *cobra.Command {
	var cf connFlags
	cmd := &cobra.Command{
		Use:   "exists",
		Short: "Print whether a Couchbase Server cluster is configured",
		RunE: func(cmd *cobra.Command, args []string) error {
			ok := server.ClusterExists(cf.config(), nil)
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

func newClusterMapCmd() *cobra.Command {
	var cf connFlags
	cmd := &cobra.Command{
		Use:   "map",
		Short: "Print the cluster host map from the management REST API",
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := server.ClusterMap(cf.config())
			if err != nil {
				logging.Error("Failed to read cluster map: %v", err)
				exitErr()
			}
			logging.Println("%s", text)
			return nil
		},
	}
	cf.addTo(cmd)
	return cmd
}

func newClusterTestCmd() *cobra.Command {
	var (
		cf       connFlags
		bucket   string
		external bool
		internal bool
		timeout  int
	)
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Connect and verify KV access via put/get",
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be a positive integer")
			}
			if external && internal {
				return fmt.Errorf("use either --external or --internal, not both")
			}
			cfg := cf.config()
			cfg.ConnectTimeout = timeout
			if bucket != "" {
				cfg.Bucket = bucket
			}
			if external {
				cfg.Network = "external"
			}
			if internal {
				cfg.Network = "default"
			}

			s := server.New()
			manageBucket := bucket == ""
			defer func() {
				if manageBucket && s.Cluster != nil {
					if ok, err := s.IsBucket("__test"); err == nil && ok {
						_ = s.DropBucket("__test")
					}
				}
				s.Disconnect()
			}()

			if err := s.Connect(cmd.Context(), cfg); err != nil {
				logging.Error("Cluster test failed: %v", err)
				exitErr()
			}
			logging.Info("Connected to %s", cf.host)
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
	cmd.Flags().BoolVar(&external, "external", false, "Force SDK network resolution to external alternate addresses")
	cmd.Flags().BoolVar(&internal, "internal", false, "Force SDK network resolution to internal (default) addresses")
	cmd.Flags().IntVar(&timeout, "timeout", 5, "SDK connect timeout in seconds")
	return cmd
}
