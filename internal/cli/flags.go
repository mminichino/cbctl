package cli

import (
	"net"
	"os"
	"strings"

	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/settings"
	"github.com/spf13/cobra"
)

// flagIfChanged returns the string flag value only when it was explicitly set on the command line.
func flagIfChanged(cmd *cobra.Command, name string) string {
	if cmd == nil || !cmd.Flags().Changed(name) {
		return ""
	}
	v, _ := cmd.Flags().GetString(name)
	return v
}

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
	allowAny   bool
	username   string
	password   string
	passEnv    string
	passFile   string
}

// prepare validates the legacy access flags and returns the connection config. needUser says
// the command connects with the SDK, so a database user and password are required.
func (f *capellaFlags) prepare(cmd *cobra.Command, needUser bool) (*config.Config, error) {
	if err := f.loadPassword(cmd, needUser); err != nil {
		return nil, err
	}
	cidr, err := validateAllowCIDR(f.allowCIDR, f.allowAny)
	if err != nil {
		return nil, err
	}
	f.allowCIDR = cidr
	return f.configFrom(cmd), nil
}

// loadPassword resolves the database password from --password-env or --password-file. A
// literal -p/--password is refused: it would land in shell history and process listings.
func (f *capellaFlags) loadPassword(cmd *cobra.Command, needUser bool) error {
	fs := cmd.Flags()
	if fs.Changed("password") {
		return usageError("pass the password with --password-env VAR or --password-file PATH",
			"-p/--password is not accepted on Capella commands")
	}
	if f.passEnv != "" && f.passFile != "" {
		return usageError("use either --password-env or --password-file", "--password-env and --password-file are mutually exclusive")
	}
	switch {
	case f.passEnv != "":
		v := envFrom(cmd)(f.passEnv)
		if v == "" {
			return usageError("export "+f.passEnv+" first", "environment variable %s is not set or empty", f.passEnv)
		}
		f.password = v
	case f.passFile != "":
		b, err := os.ReadFile(f.passFile)
		if err != nil {
			return usageError("check the --password-file path", "reading password file: %v", err)
		}
		f.password = strings.TrimRight(string(b), "\r\n")
		if f.password == "" {
			return usageError("write the password into "+f.passFile, "password file %s is empty", f.passFile)
		}
	}
	switch {
	case f.password != "" && f.username == "":
		return usageError("add --username NAME", "a database password needs --username")
	case f.username != "" && f.password == "":
		return usageError("add --password-env VAR or --password-file PATH", "--username %s needs a password", f.username)
	case needUser && f.username == "":
		return usageError("add --username NAME and --password-env VAR (or --password-file PATH)",
			"this command connects to the cluster and needs a database user")
	}
	return nil
}

// validateAllowCIDR checks an --allow-cidr value. Opening the cluster to every address needs
// --allow-any-ip, so a copy-pasted 0.0.0.0/0 never exposes a cluster by accident.
func validateAllowCIDR(cidr string, allowAny bool) (string, error) {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		if allowAny {
			return "0.0.0.0/0", nil
		}
		return "", nil
	}
	if !strings.Contains(cidr, "/") {
		if ip := net.ParseIP(cidr); ip != nil {
			if ip.To4() != nil {
				cidr += "/32"
			} else {
				cidr += "/128"
			}
		}
	}
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", usageError("give a CIDR such as 203.0.113.0/24 or an IP address", "invalid --allow-cidr %q", cidr)
	}
	if ones, _ := n.Mask.Size(); ones == 0 && !allowAny {
		return "", usageError("add --allow-any-ip to open the cluster to every address", "--allow-cidr %s allows any IP address", cidr)
	}
	return cidr, nil
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
	cmd.Flags().StringVar(&f.allowCIDR, "allow-cidr", "", "Allowed CIDR on cluster create (none by default)")
	cmd.Flags().BoolVar(&f.allowAny, "allow-any-ip", false, "Allow 0.0.0.0/0 with --allow-cidr (opens the cluster to every address)")
	cmd.Flags().StringVarP(&f.username, "username", "u", "", "Database username")
	cmd.Flags().StringVarP(&f.password, "password", "p", "", "Not accepted; use --password-env or --password-file")
	_ = cmd.Flags().MarkHidden("password")
	cmd.Flags().StringVar(&f.passEnv, "password-env", "", "Environment variable holding the database password")
	cmd.Flags().StringVar(&f.passFile, "password-file", "", "File holding the database password")
}

func (f *capellaFlags) configFrom(cmd *cobra.Command) *config.Config {
	cfg := config.New()
	cfg.Username = f.username
	cfg.Password = f.password
	cfg.SSL = true

	apiHostInput := ""
	if cmd != nil {
		apiHostInput = flagIfChanged(cmd, "api-host")
	} else if f.apiHost != "" && f.apiHost != config.DefaultCapellaAPIHost {
		// Without a command, treat only a non-default host as explicit.
		apiHostInput = f.apiHost
	}

	resolved, err := resolveSettings(cmd, settings.Inputs{
		Token:   f.token,
		OrgID:   f.orgID,
		APIHost: apiHostInput,
	})
	if err != nil {
		logging.Warning("%v", err)
	} else {
		for _, w := range resolved.Warnings {
			logging.Warning("%s", w)
		}
		if f.token == "" && resolved.APIKey.IsSet() {
			f.token = resolved.APIKey.Value
		}
		if f.orgID == "" && resolved.OrgID.IsSet() {
			f.orgID = resolved.OrgID.Value
		}
		if resolved.BaseURL.IsSet() {
			host := settings.HostFromBaseURL(resolved.BaseURL.Value)
			if f.apiHost == "" || f.apiHost == config.DefaultCapellaAPIHost {
				if cmd == nil || !cmd.Flags().Changed("api-host") {
					f.apiHost = host
				}
			}
			if cmd != nil && cmd.Flags().Changed("api-host") {
				f.apiHost = host
			}
		}
	}

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
