package capella

import "fmt"

// Certificate manages Capella cluster CA certificates.
type Certificate struct {
	Cluster        *Cluster
	CertificatePEM string
}

// NewCertificate constructs a certificate helper.
func NewCertificate(cluster *Cluster) *Certificate {
	return &Certificate{Cluster: cluster}
}

func (c *Certificate) endpoint() string {
	return fmt.Sprintf("%s/%s/certificates", c.Cluster.Endpoint(), c.Cluster.Data.ID)
}

// SetCertificate stores the PEM text.
func (c *Certificate) SetCertificate(pem string) {
	c.CertificatePEM = pem
}

// GetClusterCertificate fetches the cluster CA PEM.
func (c *Certificate) GetClusterCertificate() (string, error) {
	var resp CertificateResponse
	if err := c.Cluster.client().GetJSON(c.endpoint(), &resp); err != nil {
		return "", apiError(c.Cluster.client().lastStatus, c.Cluster.client().lastBody, "Cluster Certificate Error", err)
	}
	c.CertificatePEM = resp.Certificate
	return resp.Certificate, nil
}
