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
	cmd.AddCommand(newClusterJoinCmd())
	cmd.AddCommand(newClusterAddCmd())
	cmd.AddCommand(newClusterRebalanceCmd())
	cmd.AddCommand(newClusterExistsCmd())
	cmd.AddCommand(newClusterMapCmd())
	cmd.AddCommand(newClusterTestCmd())
	return cmd
}

type provisionFlags struct {
	ipAddress         string
	externalIPAddress string
	rallyIPAddress    string
	name              string
	serverGroup       string
	dataPath          string
}

func (f *provisionFlags) addCreateFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.ipAddress, "ip-address", "", "Node management IP/hostname (provisioner create/join)")
	cmd.Flags().StringVar(&f.externalIPAddress, "external-ip-address", "", "External alternate address (alias of --alternate-address)")
	cmd.Flags().StringVar(&f.name, "name", "", "Cluster display name")
	cmd.Flags().StringVar(&f.serverGroup, "server-group", "", "Server group / availability zone name")
	cmd.Flags().StringVar(&f.dataPath, "data-path", "", "Data/index/analytics/eventing path on the node")
}

func (f *provisionFlags) addJoinFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.ipAddress, "ip-address", "", "Node management IP/hostname (provisioner create/join)")
	cmd.Flags().StringVar(&f.externalIPAddress, "external-ip-address", "", "External alternate address (alias of --alternate-address)")
	cmd.Flags().StringVar(&f.rallyIPAddress, "rally-ip-address", "", "Primary/rally node management IP")
	cmd.Flags().StringVar(&f.serverGroup, "server-group", "", "Server group / availability zone name")
	cmd.Flags().StringVar(&f.dataPath, "data-path", "", "Data/index/analytics/eventing path on the node")
}

func (f *provisionFlags) addRebalanceFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.rallyIPAddress, "rally-ip-address", "", "Primary/rally node management IP")
	cmd.Flags().StringVar(&f.ipAddress, "ip-address", "", "Alias for --rally-ip-address when rebalancing")
}

func resolveNodeHost(ipAddress, host string) string {
	if strings.TrimSpace(ipAddress) != "" {
		return strings.TrimSpace(ipAddress)
	}
	return strings.TrimSpace(host)
}

func resolveExternal(externalIP, alternate string) string {
	if strings.TrimSpace(externalIP) != "" {
		return strings.TrimSpace(externalIP)
	}
	// alternate may include ";PORTMAP" — take host only for provisioner external flag path
	host, _, err := rest.ParseAlternateFragment(alternate)
	if err != nil {
		return strings.TrimSpace(alternate)
	}
	return host
}

func newClusterCreateCmd() *cobra.Command {
	var (
		cf       connFlags
		pf       provisionFlags
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
			provisioner := len(nodes) == 0 && (cmd.Flags().Changed("ip-address") ||
				cmd.Flags().Changed("name") || cmd.Flags().Changed("server-group") ||
				cmd.Flags().Changed("data-path"))
			if len(nodes) > 0 && (cmd.Flags().Changed("ip-address") || cmd.Flags().Changed("name") ||
				cmd.Flags().Changed("server-group") || cmd.Flags().Changed("data-path")) {
				return fmt.Errorf("provisioner flags (--ip-address/--name/--server-group/--data-path) cannot be combined with --node")
			}

			if provisioner {
				ip := resolveNodeHost(pf.ipAddress, cf.host)
				if ip == "" {
					ip = config.DefaultHostname
				}
				parsedServices := rest.ParseServices(services, rest.DefaultServerServices)
				if len(parsedServices) == 0 {
					return fmt.Errorf("at least one service is required")
				}
				ext := resolveExternal(pf.externalIPAddress, altAddr)
				opts := rest.ProvisionOptions{
					IPAddress:         ip,
					ExternalIPAddress: ext,
					Services:          parsedServices,
					ServerGroup:       pf.serverGroup,
					DataPath:          pf.dataPath,
					ClusterName:       pf.name,
					RAMGiB:            ram,
					Username:          cf.username,
					Password:          cf.password,
					SSL:               cf.ssl,
				}
				created, err := server.BootstrapPrimary(opts)
				if err != nil {
					logging.Error("Failed to create cluster: %v", err)
					exitErr()
				}
				if !created {
					logging.Info("Cluster already configured")
					return nil
				}
				logging.Info("Cluster created on %s", ip)
				return nil
			}

			defaultServices := rest.ParseServices(services, rest.DefaultServerServices)
			if len(defaultServices) == 0 {
				return fmt.Errorf("at least one service is required")
			}

			var specs []models.NodeSpec
			if len(nodes) > 0 {
				if altAddr != "" || pf.externalIPAddress != "" {
					return fmt.Errorf("use #ALTERNATE in --node instead of --alternate-address/--external-ip-address when specifying multiple nodes")
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
				if pf.externalIPAddress != "" && altHost == "" {
					altHost = strings.TrimSpace(pf.externalIPAddress)
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
	pf.addCreateFlags(cmd)
	cmd.Flags().StringArrayVarP(&nodes, "node", "n", nil, "Node spec HOST[=SERVICES][@RAM][#ALTERNATE[;PORTMAP]]")
	cmd.Flags().StringVarP(&services, "services", "s", strings.Join(rest.DefaultServerServices, ","), "Default services when a node spec omits SERVICES")
	cmd.Flags().IntVar(&ram, "ram", config.DefaultRAMGiB, "Default RAM quota in GiB when a node spec omits @RAM")
	cmd.Flags().StringVarP(&altAddr, "alternate-address", "a", "", "External alternate address for a single-node cluster")
	cmd.Flags().BoolVar(&extAPI, "ext-api", false, "Use alternate address for management REST API calls")
	return cmd
}

func runClusterJoin(cf connFlags, pf provisionFlags, services string, altAddr string) error {
	ip := resolveNodeHost(pf.ipAddress, cf.host)
	if strings.TrimSpace(ip) == "" {
		return fmt.Errorf("--ip-address is required")
	}
	rally := strings.TrimSpace(pf.rallyIPAddress)
	if rally == "" {
		return fmt.Errorf("--rally-ip-address is required")
	}
	parsedServices := rest.ParseServices(services, rest.DefaultServerServices)
	if len(parsedServices) == 0 {
		return fmt.Errorf("at least one service is required")
	}
	ext := resolveExternal(pf.externalIPAddress, altAddr)
	opts := rest.ProvisionOptions{
		IPAddress:         ip,
		ExternalIPAddress: ext,
		RallyIPAddress:    rally,
		Services:          parsedServices,
		ServerGroup:       pf.serverGroup,
		DataPath:          pf.dataPath,
		Username:          cf.username,
		Password:          cf.password,
		SSL:               cf.ssl,
	}
	joined, err := server.JoinNode(opts)
	if err != nil {
		logging.Error("Failed to join node: %v", err)
		exitErr()
	}
	if !joined {
		logging.Info("Node already configured")
		return nil
	}
	logging.Info("Node %s added to cluster at %s", ip, rally)
	return nil
}

func newClusterJoinCmd() *cobra.Command {
	var (
		cf       connFlags
		pf       provisionFlags
		services string
		altAddr  string
	)
	cmd := &cobra.Command{
		Use:   "join",
		Short: "Join a node to an existing Couchbase Server cluster (no rebalance)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterJoin(cf, pf, services, altAddr)
		},
	}
	cf.addTo(cmd)
	pf.addJoinFlags(cmd)
	cmd.Flags().StringVarP(&services, "services", "s", strings.Join(rest.DefaultServerServices, ","), "Services for the joining node")
	cmd.Flags().StringVarP(&altAddr, "alternate-address", "a", "", "External alternate address")
	return cmd
}

func newClusterAddCmd() *cobra.Command {
	var (
		cf       connFlags
		pf       provisionFlags
		services string
		altAddr  string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Alias for cluster join",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterJoin(cf, pf, services, altAddr)
		},
	}
	cf.addTo(cmd)
	pf.addJoinFlags(cmd)
	cmd.Flags().StringVarP(&services, "services", "s", strings.Join(rest.DefaultServerServices, ","), "Services for the joining node")
	cmd.Flags().StringVarP(&altAddr, "alternate-address", "a", "", "External alternate address")
	return cmd
}

func newClusterRebalanceCmd() *cobra.Command {
	var (
		cf connFlags
		pf provisionFlags
	)
	cmd := &cobra.Command{
		Use:   "rebalance",
		Short: "Rebalance all nodes in the cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			rally := strings.TrimSpace(pf.rallyIPAddress)
			if rally == "" {
				rally = resolveNodeHost(pf.ipAddress, cf.host)
			}
			if rally == "" {
				return fmt.Errorf("--rally-ip-address is required")
			}
			opts := rest.ProvisionOptions{
				RallyIPAddress: rally,
				IPAddress:      rally,
				Username:       cf.username,
				Password:       cf.password,
				SSL:            cf.ssl,
			}
			if err := server.RebalanceCluster(opts); err != nil {
				logging.Error("Failed to rebalance cluster: %v", err)
				exitErr()
			}
			logging.Info("Cluster rebalanced")
			return nil
		},
	}
	cf.addTo(cmd)
	pf.addRebalanceFlags(cmd)
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
