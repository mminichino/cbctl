package cli

import (
	"strings"

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
	cmd.AddCommand(newCapellaHelpAlias())
	installManagedModes(cmd)
	installLegacy(cmd)
	for _, kind := range []string{"bucket", "scope", "collection"} {
		c, _, err := cmd.Find([]string{kind, "create"})
		if err != nil {
			panic(err)
		}
		shareAddFlags(c)
	}
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
	cmd.AddCommand(newHiddenApplyAlias("cluster"))
	registerCapellaClusterCommands(cmd)
	return cmd
}

func newCapellaClusterCreateCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "create [NAME]",
		Short: "Create a Capella cluster",
		Long: "Create a cluster. With a capella.yaml present the cluster is added to the file and applied; without one\n" +
			"(or with --direct) it is created in Capella directly. On a terminal, a missing name starts a short wizard.\n\n" +
			"Examples:\n  cbctl capella cluster create dev-01 --size dev\n  cbctl capella cluster create prod-01 --group data:3x8/32 --group index,query:2x8/32 --region us-east-2",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if usesLegacyFlags(cmd) {
				return runLegacyClusterCreate(cmd, args, &cf)
			}
			return runDirectClusterCreate(cmd, args)
		},
	}
	cf.addTo(cmd)
	return cmd
}

// managedCreateFlags are the cluster create flags of the managed and direct builder; none of
// them can be combined with the legacy flags.
var managedCreateFlags = []string{"name", "provider", "region", "cidr", "size", "nodes", "cpu", "ram", "group",
	"disk-type", "disk-size", "disk-iops", "disk-auto-expansion", "services", "availability", "support-plan",
	"support-timezone", "description", "free-tier", "single-node", "server-version", "zone", "cmek-id", "private-dns"}

// resolveClusterName returns the cluster name from the positional argument or the named flag.
// Both given and different is a usage error.
func resolveClusterName(cmd *cobra.Command, args []string, flag string) (string, error) {
	fromFlag := flagString(cmd, flag)
	if len(args) == 0 {
		return fromFlag, nil
	}
	if fromFlag != "" && fromFlag != args[0] {
		return "", usageError("give the name once, as NAME or --"+flag,
			"cluster name %q and --%s %q differ", args[0], flag, fromFlag)
	}
	return args[0], nil
}

// runLegacyClusterCreate is the pre-port direct create, kept for scripts that pass the legacy
// flags. It warns, and opens no network access or database user the caller did not ask for.
func runLegacyClusterCreate(cmd *cobra.Command, args []string, cf *capellaFlags) error {
	warnDeprecated(cmd.ErrOrStderr(), "capella cluster create --database ...", replacementFor("cluster create"))
	var mixed []string
	for _, name := range managedCreateFlags {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			mixed = append(mixed, "--"+name)
		}
	}
	if len(mixed) > 0 {
		return usageError("use either the cluster flags (--size, --group, ...) or the legacy flags, not both",
			"cannot combine %s with legacy --database/-u/--password-env", strings.Join(mixed, ", "))
	}
	if len(args) == 1 {
		if cf.database != "" && cf.database != args[0] {
			return usageError("give the name once", "cluster name %q and --database %q differ", args[0], cf.database)
		}
		cf.database = args[0]
	}
	cfg, err := cf.prepare(cmd, false)
	if err != nil {
		return err
	}
	created, err := newLegacyBackend().CreateCluster(cmd.Context(), cfg)
	if err != nil {
		return failure("Failed to create cluster", err)
	}
	name := cf.database
	if name == "" {
		name = cf.databaseID
	}
	if !created {
		logging.Info("Cluster already configured")
		return nil
	}
	logging.Info("Cluster created on %s", name)
	return nil
}

func newCapellaClusterExistsCmd() *cobra.Command {
	var cf capellaFlags
	cmd := &cobra.Command{
		Use:   "exists",
		Short: "Print whether a Capella cluster exists",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := cf.prepare(cmd, false)
			if err != nil {
				return err
			}
			ok, err := newLegacyBackend().ClusterExists(cmd.Context(), cfg)
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

// newCapellaClusterDestroyCmd is the legacy destroy. It runs the same code as "cluster delete",
// so it asks for confirmation (exit 4 without a terminal and --yes) and honours deletion
// protection.
func newCapellaClusterDestroyCmd() *cobra.Command {
	var (
		cf      capellaFlags
		discard bool
	)
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Destroy a Capella cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cf.loadPassword(cmd, false); err != nil {
				return err
			}
			name := cf.database
			if name == "" {
				name = cf.databaseID
			}
			if name == "" {
				return usageError("pass --database NAME, or use 'cbctl capella cluster delete CLUSTER'", "the cluster name is required")
			}
			af := readAPIFlags(cmd)
			af.apiHost = flagIfChanged(cmd, "api-host")
			rt, err := newRuntime(cmd, af)
			if err != nil {
				return err
			}
			project := ""
			if cmd.Flags().Changed("project") {
				project = cf.project
			}
			return rt.runClusterDelete(name, project, discard)
		},
	}
	cf.addTo(cmd)
	cmd.Flags().BoolVar(&discard, "discard-snapshots", false, "Also delete the cluster's snapshot backups (kept by default)")
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
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
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
		Use:   "create CLUSTER NAME",
		Short: "Add a bucket (capellactl spelling of 'bucket add')",
		Long: "'bucket create CLUSTER NAME' is capellactl's spelling of 'bucket add CLUSTER NAME' and takes the same flags.\n" +
			"The pre-port form 'bucket create NAME --database CLUSTER' still works but is deprecated.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if capellactlArity("bucket", args) {
				return forwardToAdd(cmd, args)
			}
			if !usesLegacyFlags(cmd) {
				return usageError("run 'cbctl capella bucket add CLUSTER NAME'",
					"'bucket create' takes CLUSTER NAME (or the deprecated NAME --database CLUSTER)")
			}
			warnDeprecated(cmd.ErrOrStderr(), "capella bucket create NAME --database ...", replacementFor("bucket create"))
			name := args[0]
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
			backend := newLegacyBackend()
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
	cmd.Flags().IntVar(&quota, "quota", 128, "RAM quota in MiB (deprecated form only)")
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
			cfg, err := cf.prepare(cmd, true)
			if err != nil {
				return err
			}
			backend := newLegacyBackend()
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
