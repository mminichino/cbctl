package cli

import (
	"github.com/mminichino/cbctl/internal/config"
	"github.com/spf13/cobra"
)

// connFlags are shared Server connection flags.
type connFlags struct {
	host     string
	username string
	password string
	ssl      bool
}

func (f *connFlags) addTo(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.host, "host", config.DefaultHostname, "Cluster hostname or IP")
	cmd.Flags().StringVarP(&f.username, "username", "u", config.DefaultUser, "Administrator username")
	cmd.Flags().StringVarP(&f.password, "password", "p", config.DefaultPassword, "Administrator password")
	cmd.Flags().BoolVar(&f.ssl, "ssl", false, "Use TLS when connecting")
	// Support --no-ssl explicitly for parity with Python typer --ssl/--no-ssl.
	_ = cmd.Flags().Bool("no-ssl", false, "Disable TLS when connecting (default)")
	cmd.PreRunE = chainPreRun(cmd.PreRunE, func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("no-ssl") {
			noSSL, _ := cmd.Flags().GetBool("no-ssl")
			if noSSL {
				f.ssl = false
			}
		}
		if cmd.Flags().Changed("ssl") {
			ssl, _ := cmd.Flags().GetBool("ssl")
			f.ssl = ssl
		}
		return nil
	})
}

func (f *connFlags) config() *config.Config {
	cfg := config.New()
	cfg.Hostname = f.host
	cfg.Username = f.username
	cfg.Password = f.password
	cfg.SSL = f.ssl
	return cfg
}

type capellaFlags struct {
	token      string
	apiHost    string
	orgName    string
	orgID      string
	project    string
	projectID  string
	database   string
	databaseID string
	userEmail  string
	userID     string
	allowCIDR  string
	username   string
	password   string
}

func (f *capellaFlags) addTo(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.token, "token", "", "Capella API v4 token")
	cmd.Flags().StringVar(&f.apiHost, "api-host", config.DefaultCapellaAPIHost, "Capella API host")
	cmd.Flags().StringVar(&f.orgName, "org", "", "Capella organization name")
	cmd.Flags().StringVar(&f.orgID, "org-id", "", "Capella organization ID")
	cmd.Flags().StringVar(&f.project, "project", config.DefaultCapellaProjectName, "Capella project name")
	cmd.Flags().StringVar(&f.projectID, "project-id", "", "Capella project ID")
	cmd.Flags().StringVar(&f.database, "database", "", "Capella database/cluster name")
	cmd.Flags().StringVar(&f.databaseID, "database-id", "", "Capella database/cluster ID")
	cmd.Flags().StringVar(&f.userEmail, "user-email", "", "Capella account email")
	cmd.Flags().StringVar(&f.userID, "user-id", "", "Capella account user ID")
	cmd.Flags().StringVar(&f.allowCIDR, "allow-cidr", config.DefaultCapellaAllowCIDR, "Allowed CIDR on cluster create")
	cmd.Flags().StringVarP(&f.username, "username", "u", config.DefaultUser, "Database username")
	cmd.Flags().StringVarP(&f.password, "password", "p", config.DefaultPassword, "Database password")
}

func (f *capellaFlags) config() *config.Config {
	cfg := config.New()
	cfg.Username = f.username
	cfg.Password = f.password
	cfg.SSL = true
	cfg.SetProp(config.PropCapellaToken, f.token)
	cfg.SetProp(config.PropCapellaAPIHost, f.apiHost)
	cfg.SetProp(config.PropCapellaOrgName, f.orgName)
	cfg.SetProp(config.PropCapellaOrgID, f.orgID)
	cfg.SetProp(config.PropCapellaProject, f.project)
	cfg.SetProp(config.PropCapellaProjectID, f.projectID)
	cfg.SetProp(config.PropCapellaDatabase, f.database)
	cfg.SetProp(config.PropCapellaDatabaseID, f.databaseID)
	cfg.SetProp(config.PropCapellaUserEmail, f.userEmail)
	cfg.SetProp(config.PropCapellaUserID, f.userID)
	cfg.SetProp(config.PropCapellaAllowCIDR, f.allowCIDR)
	return cfg
}

func chainPreRun(existing, next func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	if existing == nil {
		return next
	}
	return func(cmd *cobra.Command, args []string) error {
		if err := existing(cmd, args); err != nil {
			return err
		}
		return next(cmd, args)
	}
}
