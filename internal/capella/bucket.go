package capella

import (
	"fmt"
	"strings"
)

// Buckets manages Capella bucket resources.
type Buckets struct {
	Cluster *Cluster
	Bucket  *CapellaBucketData
}

// NewBuckets constructs a bucket helper for a resolved cluster.
func NewBuckets(cluster *Cluster) *Buckets {
	return &Buckets{Cluster: cluster}
}

func (b *Buckets) endpoint() string {
	return fmt.Sprintf("%s/%s/buckets", b.Cluster.Endpoint(), b.Cluster.Data.ID)
}

func (b *Buckets) client() *Client { return b.Cluster.client() }

// List buckets.
func (b *Buckets) List() ([]CapellaBucketData, error) {
	var wrap struct {
		Data []CapellaBucketData `json:"data"`
	}
	if err := b.client().GetJSON(b.endpoint(), &wrap); err != nil {
		return nil, apiError(b.client().lastStatus, b.client().lastBody, "Bucket List Error", err)
	}
	return wrap.Data, nil
}

// FindByName returns a bucket or nil.
func (b *Buckets) FindByName(name string) (*CapellaBucketData, error) {
	list, err := b.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}
	return nil, nil
}

// CreateBucketSettings holds Capella bucket create options.
type CreateBucketSettings struct {
	Name                     string
	Type                     string
	StorageBackend           string
	MemoryAllocationInMb     int
	BucketConflictResolution string
	DurabilityLevel          string
	Replicas                 int
	Flush                    bool
	TimeToLiveInSeconds      int
}

// DefaultCreateBucketSettings returns Capella defaults.
func DefaultCreateBucketSettings(name string) CreateBucketSettings {
	return CreateBucketSettings{
		Name:                     name,
		Type:                     "couchbase",
		StorageBackend:           "couchstore",
		MemoryAllocationInMb:     128,
		BucketConflictResolution: "seqno",
		DurabilityLevel:          "none",
		Replicas:                 1,
		Flush:                    false,
		TimeToLiveInSeconds:      0,
	}
}

// Create creates a bucket if missing.
func (b *Buckets) Create(settings CreateBucketSettings) (*CapellaBucketData, error) {
	if settings.Name == "" {
		return nil, fmt.Errorf("settings must include name")
	}
	if existing, err := b.FindByName(settings.Name); err != nil {
		return nil, err
	} else if existing != nil {
		b.Bucket = existing
		return existing, nil
	}
	if settings.Type == "" {
		settings.Type = "couchbase"
	}
	if settings.StorageBackend == "" {
		settings.StorageBackend = "couchstore"
	}
	if settings.MemoryAllocationInMb <= 0 {
		settings.MemoryAllocationInMb = 128
	}
	if settings.BucketConflictResolution == "" {
		settings.BucketConflictResolution = "seqno"
	}
	if settings.DurabilityLevel == "" {
		settings.DurabilityLevel = "none"
	}
	req := CreateBucketRequest{
		Name:                     settings.Name,
		Type:                     settings.Type,
		StorageBackend:           settings.StorageBackend,
		MemoryAllocationInMb:     settings.MemoryAllocationInMb,
		BucketConflictResolution: settings.BucketConflictResolution,
		DurabilityLevel:          strings.ToLower(settings.DurabilityLevel),
		Replicas:                 settings.Replicas,
		Flush:                    settings.Flush,
		TimeToLiveInSeconds:      settings.TimeToLiveInSeconds,
	}
	var idResp IDResponse
	if err := b.client().PostJSON(b.endpoint(), req, &idResp); err != nil {
		return nil, apiError(b.client().lastStatus, b.client().lastBody, "Bucket Create Error", err)
	}
	data, err := b.GetByID(idResp.ID)
	if err != nil {
		return nil, fmt.Errorf("Bucket creation failed: %w", err)
	}
	b.Bucket = data
	return data, nil
}

// Delete deletes the current bucket.
func (b *Buckets) Delete() error {
	if b.Bucket == nil || b.Bucket.ID == "" {
		return nil
	}
	if err := b.client().Delete(b.endpoint() + "/" + b.Bucket.ID); err != nil {
		return apiError(b.client().lastStatus, b.client().lastBody, "Bucket Delete Error", err)
	}
	b.Bucket = nil
	return nil
}

// GetByName finds a bucket by name.
func (b *Buckets) GetByName(name string) (*CapellaBucketData, error) {
	if b.Bucket != nil && b.Bucket.Name == name {
		return b.Bucket, nil
	}
	found, err := b.FindByName(name)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, notFound(fmt.Sprintf("Can not find bucket %s", name))
	}
	return found, nil
}

// GetByID GETs a bucket by ID.
func (b *Buckets) GetByID(id string) (*CapellaBucketData, error) {
	if b.Bucket != nil && b.Bucket.ID == id {
		return b.Bucket, nil
	}
	var data CapellaBucketData
	if err := b.client().GetJSON(b.endpoint()+"/"+id, &data); err != nil {
		if IsNotFound(err) {
			return nil, notFound("Bucket not found")
		}
		return nil, apiError(b.client().lastStatus, b.client().lastBody, "Bucket Get Error", err)
	}
	return &data, nil
}
