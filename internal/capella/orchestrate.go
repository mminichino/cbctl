package capella

import (
	"context"
	"fmt"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/mminichino/cbctl/internal/config"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/mminichino/cbctl/internal/rest"
)

// Backend orchestrates Capella Management API + SDK operations for the legacy (pre-port)
// commands. Every method takes the command context: cancellation (Ctrl-C) and deadlines reach
// each API request, cluster wait and connectivity poll.
type Backend struct {
	Cluster    *Cluster
	Database   string
	StreamHost string
	restClient *rest.Client

	// BaseURL, Clock and WaitTimeout override the API endpoint, the wait clock and the cluster
	// wait bound (tests). Zero values use the configured API host, engine.RealClock and
	// DefaultWaitTimeout.
	BaseURL     string
	Clock       engine.Clock
	WaitTimeout time.Duration
}

// NewBackend returns an empty Capella backend.
func NewBackend() *Backend {
	return &Backend{restClient: rest.NewClient()}
}

func (b *Backend) validate(cfg *config.Config) error {
	if cfg.Prop(config.PropCapellaToken) == "" {
		return fmt.Errorf("Capella connection requires %s", config.PropCapellaToken)
	}
	if cfg.Prop(config.PropCapellaProject) == "" && cfg.Prop(config.PropCapellaProjectID) == "" {
		cfg.SetProp(config.PropCapellaProject, config.DefaultCapellaProjectName)
	}
	if cfg.Prop(config.PropCapellaDatabase) == "" && cfg.Prop(config.PropCapellaDatabaseID) == "" {
		return fmt.Errorf("Capella connection requires %s or %s", config.PropCapellaDatabase, config.PropCapellaDatabaseID)
	}
	if cfg.Prop(config.PropCapellaUserEmail) == "" && cfg.Prop(config.PropCapellaUserID) == "" {
		return fmt.Errorf("Capella connection requires %s or %s", config.PropCapellaUserEmail, config.PropCapellaUserID)
	}
	return nil
}

func (b *Backend) resolveDatabaseName(cfg *config.Config) string {
	name := cfg.Prop(config.PropCapellaDatabase)
	if name == "" {
		name = cfg.Prop(config.PropCapellaDatabaseID)
	}
	if name != "" {
		cfg.SetProp(config.PropCapellaDatabase, name)
	}
	b.Database = name
	return name
}

func (b *Backend) resolveProject(ctx context.Context, cfg *config.Config) (*Project, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	client, err := NewClientFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	client.SetContext(ctx)
	client.Clock = b.Clock
	client.WaitTimeout = b.WaitTimeout
	if b.BaseURL != "" {
		client.BaseURL = b.BaseURL
	}
	org, err := NewOrganization(client)
	if err != nil {
		return nil, err
	}
	return org.DefaultProject()
}

// CreateCluster creates (or reuses) a Capella cluster, mirroring cloud.Capella.create_cluster_impl.
// created is true when a new cluster was posted; false when an existing healthy cluster was reused.
func (b *Backend) CreateCluster(ctx context.Context, cfg *config.Config) (created bool, err error) {
	if cfg == nil {
		return false, fmt.Errorf("config is required")
	}
	b.resolveDatabaseName(cfg)
	if err := b.validate(cfg); err != nil {
		return false, err
	}

	project, err := b.resolveProject(ctx, cfg)
	if err != nil {
		return false, err
	}

	props := cfg.MergedProperties(nil)
	nodes := rest.ParseCapellaNodes(props)
	clusterCfg := BuildClusterConfig(nodes)

	existing, err := NewCluster(project).FindByName(b.Database)
	if err != nil {
		return false, err
	}
	wasNew := existing == nil

	cluster, err := project.CreateCluster(b.Database, clusterCfg)
	if err != nil {
		return false, fmt.Errorf("failed to create Capella cluster: %w", err)
	}
	b.Cluster = cluster

	// No access is opened by default: a CIDR is only allowed when the caller named one, and a
	// database user is only made when the caller supplied both its name and password.
	allowCIDR := cfg.Prop(config.PropCapellaAllowCIDR)
	if allowCIDR != "" && cluster.AllowedCIDR != nil {
		if _, err := cluster.AllowedCIDR.CreateAllowedCIDR(allowCIDR); err != nil {
			return false, err
		}
	}
	hasUser := cfg.Username != "" && cfg.Password != ""
	if hasUser && cluster.Credentials != nil {
		if _, err := cluster.Credentials.CreateCredential(cfg.Username, cfg.Password, nil); err != nil {
			return false, err
		}
	}
	if allowCIDR == "" || !hasUser {
		// Without an allowed CIDR and a database user the cluster cannot be reached from here,
		// so the data-plane readiness checks are skipped.
		logging.Success("Capella cluster %s created; skipping reachability checks (no --allow-cidr or database user)", b.Database)
		return wasNew, nil
	}

	connectString, err := cluster.ConnectString()
	if err != nil {
		return false, err
	}
	if !NewConnectivity().CheckConnectivity(ctx, connectString, true, 300*time.Second) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return false, fmt.Errorf("Capella cluster connectivity check failed")
	}

	b.StreamHost = ExtractHost(connectString)
	endpoint := rest.ForCapella(b.StreamHost)
	rc := b.restClient
	if rc == nil {
		rc = rest.NewClient()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := rc.WaitForClusterServices(endpoint, cfg.Username, cfg.Password, 120); err != nil {
		return false, err
	}
	if err := rc.WaitForQueryReady(endpoint, cfg.Username, cfg.Password, 60); err != nil {
		return false, err
	}
	if err := rc.WaitForRebalanceComplete(endpoint, cfg.Username, cfg.Password, 120); err != nil {
		return false, err
	}
	logging.Success("Capella cluster %s created", b.Database)
	return wasNew, nil
}

// DestroyCluster deletes the Capella cluster.
func (b *Backend) DestroyCluster(ctx context.Context, cfg *config.Config) error {
	if b.Cluster == nil {
		if cfg != nil {
			b.resolveDatabaseName(cfg)
			if err := b.validate(cfg); err != nil {
				return err
			}
			project, err := b.resolveProject(ctx, cfg)
			if err != nil {
				return err
			}
			cluster, err := project.AddCluster(b.Database)
			if err != nil {
				var nf *CapellaNotFoundError
				if AsCapellaNotFound(err, &nf) {
					return nil
				}
				return err
			}
			b.Cluster = cluster
		} else {
			return nil
		}
	} else {
		b.Cluster.client().SetContext(ctx)
	}
	if err := b.Cluster.Delete(); err != nil {
		return fmt.Errorf("failed to destroy Capella cluster: %w", err)
	}
	b.Cluster = nil
	b.StreamHost = ""
	return nil
}

// ClusterExists performs a real Capella API lookup by database name/id. A missing cluster is
// (false, nil); any other failure (auth, transport, 5xx, cancellation) is returned as an error
// so callers never mistake an outage for "does not exist".
func (b *Backend) ClusterExists(ctx context.Context, cfg *config.Config) (bool, error) {
	if cfg == nil {
		return false, fmt.Errorf("config is required")
	}
	b.resolveDatabaseName(cfg)
	if cfg.Prop(config.PropCapellaToken) == "" {
		return false, fmt.Errorf("Capella connection requires %s", config.PropCapellaToken)
	}
	project, err := b.resolveProject(ctx, cfg)
	if err != nil {
		return false, err
	}
	c := NewCluster(project)
	client := project.Org.Client
	switch {
	case client.hasDatabaseID():
		_, err = c.GetByID(client.DatabaseID)
	case client.hasDatabaseName():
		_, err = c.GetByName(client.DatabaseName)
	default:
		return false, fmt.Errorf("Capella connection requires %s or %s", config.PropCapellaDatabase, config.PropCapellaDatabaseID)
	}
	if err == nil {
		return true, nil
	}
	if IsNotFound(err) {
		return false, nil
	}
	return false, err
}

// Connect resolves Capella resources and returns a gocb cluster.
// cleanup closes the SDK connection and removes temp cert files.
func (b *Backend) Connect(ctx context.Context, cfg *config.Config) (*gocb.Cluster, func(), string, string, error) {
	noop := func() {}
	if cfg == nil {
		return nil, noop, "", "", fmt.Errorf("config is required")
	}
	b.resolveDatabaseName(cfg)
	if err := b.validate(cfg); err != nil {
		return nil, noop, "", "", err
	}

	project, err := b.resolveProject(ctx, cfg)
	if err != nil {
		return nil, noop, "", "", err
	}
	cluster, err := project.AddCluster(b.Database)
	if err != nil {
		return nil, noop, "", "", err
	}
	b.Cluster = cluster

	if cluster.Credentials != nil {
		_, _ = cluster.Credentials.AddCredentials(cfg.Username, cfg.Password)
	}

	connectString, err := cluster.ConnectString()
	if err != nil {
		return nil, noop, "", "", err
	}
	if !NewConnectivity().CheckConnectivity(ctx, connectString, true, 120*time.Second) {
		if err := ctx.Err(); err != nil {
			return nil, noop, "", "", err
		}
		return nil, noop, "", "", fmt.Errorf("Capella cluster is not reachable at %s", connectString)
	}

	var certPEM string
	if cluster.Certificate != nil {
		certPEM = cluster.Certificate.CertificatePEM
		if certPEM == "" {
			if pem, err := cluster.Certificate.GetClusterCertificate(); err == nil {
				certPEM = pem
			}
		}
	}

	gocbCluster, cleanup, err := ConnectCluster(
		connectString,
		cfg.Username,
		cfg.Password,
		certPEM,
		cfg.KVTimeout,
		cfg.ConnectTimeout,
		cfg.QueryTimeout,
	)
	if err != nil {
		return nil, noop, "", "", err
	}
	b.StreamHost = ExtractHost(connectString)
	return gocbCluster, cleanup, certPEM, b.StreamHost, nil
}

// CreateBucket creates a bucket via Capella REST.
func (b *Backend) CreateBucket(name string, quotaMiB, replicas int) error {
	if b.Cluster == nil || b.Cluster.Buckets == nil {
		return fmt.Errorf("Capella cluster is not connected")
	}
	settings := DefaultCreateBucketSettings(name)
	if quotaMiB > 0 {
		settings.MemoryAllocationInMb = quotaMiB
	}
	if replicas >= 0 {
		settings.Replicas = replicas
	}
	_, err := b.Cluster.Buckets.Create(settings)
	if err != nil {
		return fmt.Errorf("bucketCreate: Capella API error: %w", err)
	}
	return nil
}

// DropBucket deletes a bucket via Capella REST.
func (b *Backend) DropBucket(name string) error {
	if b.Cluster == nil || b.Cluster.Buckets == nil {
		return fmt.Errorf("Capella cluster is not connected")
	}
	data, err := b.Cluster.Buckets.GetByName(name)
	if err != nil {
		var nf *CapellaNotFoundError
		if AsCapellaNotFound(err, &nf) {
			return nil
		}
		return err
	}
	b.Cluster.Buckets.Bucket = data
	return b.Cluster.Buckets.Delete()
}
