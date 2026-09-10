package capella

import (
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/couchbase/gocb/v2"
)

var tempCertFiles []string

// ConnectCluster connects to Capella via gocb with TLS and optional PEM CA.
// cleanup removes any temporary certificate file written for the connection.
func ConnectCluster(
	connectString, username, password, certificatePEM string,
	kvTimeout, connectTimeout, queryTimeout int,
) (*gocb.Cluster, func(), error) {
	connectString = NormalizeConnectString(connectString)
	if connectString == "" {
		return nil, nil, fmt.Errorf("Capella connection string is required")
	}
	if kvTimeout <= 0 {
		kvTimeout = 5
	}
	if connectTimeout <= 0 {
		connectTimeout = 15
	}
	if queryTimeout <= 0 {
		queryTimeout = 75
	}

	opts := gocb.ClusterOptions{
		Authenticator: gocb.PasswordAuthenticator{
			Username: username,
			Password: password,
		},
		TimeoutsConfig: gocb.TimeoutsConfig{
			KVTimeout:         time.Duration(maxInt(kvTimeout, 20)) * time.Second,
			ConnectTimeout:    time.Duration(maxInt(connectTimeout, 20)) * time.Second,
			QueryTimeout:      time.Duration(maxInt(queryTimeout, 120)) * time.Second,
			ManagementTimeout: 120 * time.Second,
		},
		SecurityConfig: gocb.SecurityConfig{},
	}

	cleanup := func() { cleanupTempCerts() }

	if certificatePEM != "" {
		path, err := writeCertTempfile(certificatePEM)
		if err != nil {
			return nil, nil, err
		}
		pemBytes, err := os.ReadFile(path)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			cleanup()
			return nil, nil, fmt.Errorf("failed to parse Capella certificate PEM")
		}
		opts.SecurityConfig.TLSRootCAs = pool
	}

	_ = opts.ApplyProfile(gocb.ClusterConfigProfileWanDevelopment)

	cluster, err := gocb.Connect(connectString, opts)
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	waitTO := time.Duration(maxInt(connectTimeout, 20)) * time.Second
	if err := cluster.WaitUntilReady(waitTO, nil); err != nil {
		_, _ = cluster.Ping(nil)
	}
	return cluster, cleanup, nil
}

// DisconnectCluster closes a gocb cluster and cleans temp certs.
func DisconnectCluster(cluster *gocb.Cluster) {
	if cluster != nil {
		_ = cluster.Close(nil)
	}
	cleanupTempCerts()
}

func writeCertTempfile(certificatePEM string) (string, error) {
	f, err := os.CreateTemp("", "capella-cert-*.pem")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.WriteString(certificatePEM); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if !endsWithNewline(certificatePEM) {
		_, _ = f.WriteString("\n")
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	tempCertFiles = append(tempCertFiles, path)
	return path, nil
}

func endsWithNewline(s string) bool {
	return len(s) > 0 && s[len(s)-1] == '\n'
}

func cleanupTempCerts() {
	for len(tempCertFiles) > 0 {
		n := len(tempCertFiles) - 1
		path := tempCertFiles[n]
		tempCertFiles = tempCertFiles[:n]
		_ = os.Remove(path)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
