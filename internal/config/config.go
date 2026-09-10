// Package config holds Couchbase Server and Capella connection settings.
package config

const (
	DefaultUser     = "Administrator"
	DefaultPassword = "password"
	DefaultHostname = "127.0.0.1"

	DefaultKVTimeout      = 5
	DefaultConnectTimeout = 15
	DefaultQueryTimeout   = 75

	DefaultBucketQuotaMiB = 128
	DefaultRAMGiB         = 4

	PropHost           = "couchbase.hostname"
	PropUser           = "couchbase.username"
	PropPassword       = "couchbase.password"
	PropBucket         = "couchbase.bucket"
	PropScope          = "couchbase.scope"
	PropCollection     = "couchbase.collection"
	PropSSLMode        = "couchbase.sslMode"
	PropNetwork        = "couchbase.network"
	PropConnectTimeout = "couchbase.connectTimeout"
	PropKVTimeout      = "couchbase.kvTimeout"
	PropQueryTimeout   = "couchbase.queryTimeout"
	PropServerExtAPI   = "couchbase.server.extApi"
	PropCACert         = "couchbase.ca.cert"

	PropCapellaToken      = "capella.token"
	PropCapellaAPIHost    = "capella.api.host"
	PropCapellaOrgName    = "capella.organization.name"
	PropCapellaOrgID      = "capella.organization.id"
	PropCapellaProject    = "capella.project.name"
	PropCapellaProjectID  = "capella.project.id"
	PropCapellaDatabase   = "capella.database.name"
	PropCapellaDatabaseID = "capella.database.id"
	PropCapellaUserEmail  = "capella.user.email"
	PropCapellaUserID     = "capella.user.id"
	PropCapellaAllowCIDR  = "capella.cluster.allow"

	DefaultCapellaAPIHost     = "cloudapi.cloud.couchbase.com"
	DefaultCapellaProjectName = "default"
	DefaultCapellaAllowCIDR   = "0.0.0.0/0"
)

// Config is a mutable connection configuration.
type Config struct {
	Hostname       string
	Username       string
	Password       string
	Bucket         string
	Scope          string
	Collection     string
	SSL            bool
	Network        string
	ConnectTimeout int
	KVTimeout      int
	QueryTimeout   int
	CACertPath     string
	Properties     map[string]string
}

// New returns a Config with Server CLI defaults (SSL off).
func New() *Config {
	return &Config{
		Hostname:       DefaultHostname,
		Username:       DefaultUser,
		Password:       DefaultPassword,
		SSL:            false,
		ConnectTimeout: DefaultConnectTimeout,
		KVTimeout:      DefaultKVTimeout,
		QueryTimeout:   DefaultQueryTimeout,
		Properties:     map[string]string{},
	}
}

// Clone returns a shallow copy with a copied properties map.
func (c *Config) Clone() *Config {
	if c == nil {
		return New()
	}
	out := *c
	out.Properties = map[string]string{}
	for k, v := range c.Properties {
		out.Properties[k] = v
	}
	return &out
}

// SetProp sets a property key.
func (c *Config) SetProp(key, value string) *Config {
	if c.Properties == nil {
		c.Properties = map[string]string{}
	}
	if value != "" {
		c.Properties[key] = value
	}
	return c
}

// Prop returns a property value.
func (c *Config) Prop(key string) string {
	if c.Properties == nil {
		return ""
	}
	return c.Properties[key]
}

// IsCapella reports whether Capella token auth is configured.
func (c *Config) IsCapella() bool {
	return c.Prop(PropCapellaToken) != ""
}

// MergedProperties returns properties plus core connection fields.
func (c *Config) MergedProperties(extra map[string]string) map[string]string {
	merged := map[string]string{}
	for k, v := range c.Properties {
		merged[k] = v
	}
	merged[PropUser] = c.Username
	merged[PropPassword] = c.Password
	if c.Hostname != "" {
		merged[PropHost] = c.Hostname
	}
	if c.SSL {
		merged[PropSSLMode] = "true"
	} else {
		merged[PropSSLMode] = "false"
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

// ServerQuotaKey returns couchbase.server.<service>.quota.
func ServerQuotaKey(service string) string {
	return "couchbase.server." + service + ".quota"
}
