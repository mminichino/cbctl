package cli

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Allowed values of the bucket flags.
var (
	bucketTypes       = []string{"couchbase", "ephemeral"}
	storageBackends   = []string{"couchstore", "magma"}
	durabilityLevels  = []string{"none", "majority", "majorityAndPersistActive", "persistToMajority"}
	conflictModes     = []string{"seqno", "lww"}
	couchbaseEviction = []string{"fullEviction", "valueOnly"}
	ephemeralEviction = []string{"noEviction", "nruEviction"}
	ephemeralDurable  = []string{"none", "majority"}
	vbucketCounts     = []int{128, 1024}
)

// defaultBucketMemoryMB is the memory quota of a new bucket when --memory is not given.
const defaultBucketMemoryMB = 100

// freeTierUnsupported is the message for bucket flags a free-tier cluster cannot take.
const freeTierUnsupported = "not supported on free-tier clusters"

// bucketView is the output of bucket get: the bucket's settings and usage.
type bucketView struct {
	ID                           string                  `json:"id"`
	Name                         string                  `json:"name"`
	Type                         string                  `json:"type"`
	StorageBackend               string                  `json:"storageBackend,omitempty"`
	VBuckets                     int                     `json:"vbuckets,omitempty"`
	MemoryAllocationInMb         int                     `json:"memoryAllocationInMb"`
	BucketConflictResolution     string                  `json:"bucketConflictResolution,omitempty"`
	DurabilityLevel              string                  `json:"durabilityLevel,omitempty"`
	Replicas                     int                     `json:"replicas"`
	FlushEnabled                 bool                    `json:"flushEnabled"`
	TimeToLiveInSeconds          int                     `json:"timeToLiveInSeconds"`
	EvictionPolicy               string                  `json:"evictionPolicy,omitempty"`
	Priority                     int                     `json:"priority"`
	EnableCrossClusterVersioning *bool                   `json:"enableCrossClusterVersioning,omitempty"`
	Stats                        *capellaapi.BucketStats `json:"stats,omitempty"`
}

// flushEnabled reads the flush setting from either of the names Capella uses.
func flushEnabled(b *capellaapi.Bucket) bool {
	switch {
	case b.FlushEnabled != nil:
		return *b.FlushEnabled
	case b.Flush != nil:
		return *b.Flush
	}
	return false
}

func newBucketView(b *capellaapi.Bucket) bucketView {
	id := b.ID
	if id == "" {
		id = capellaapi.BucketID(b.Name)
	}
	return bucketView{
		ID: id, Name: b.Name, Type: b.Type, StorageBackend: b.StorageBackend, VBuckets: b.VBuckets,
		MemoryAllocationInMb: b.MemoryAllocationInMb, BucketConflictResolution: b.BucketConflictResolution,
		DurabilityLevel: b.DurabilityLevel, Replicas: b.Replicas, FlushEnabled: flushEnabled(b),
		TimeToLiveInSeconds: b.TimeToLiveInSeconds, EvictionPolicy: b.EvictionPolicy, Priority: b.Priority,
		EnableCrossClusterVersioning: b.EnableCrossClusterVersioning, Stats: b.Stats,
	}
}

// bucketDoc is the JSON document of bucket add, update and delete.
type bucketDoc struct {
	Cluster string `json:"cluster"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Status  string `json:"status"`
}

// listBuckets lists the buckets of a cluster through the endpoint that matches its tier.
func (r *runtime) listBuckets(t *clusterTarget) ([]capellaapi.Bucket, error) {
	pid, cid := t.ids()
	var (
		buckets []capellaapi.Bucket
		err     error
	)
	if t.freeTier() {
		buckets, _, err = r.Client.ListFreeBuckets(r.Ctx, pid, cid)
	} else {
		buckets, err = r.Client.ListBuckets(r.Ctx, pid, cid)
	}
	if err != nil {
		return nil, fmt.Errorf("listing buckets of %s: %w", t.Cluster.Name, err)
	}
	return buckets, nil
}

// readBucket reads one bucket with its ETag through the endpoint that matches the cluster's tier.
func (r *runtime) readBucket(t *clusterTarget, name string) (*capellaapi.Bucket, string, error) {
	pid, cid := t.ids()
	id := capellaapi.BucketID(name)
	var (
		b    *capellaapi.Bucket
		etag string
		err  error
	)
	if t.freeTier() {
		b, etag, err = r.Client.GetFreeBucket(r.Ctx, pid, cid, id)
	} else {
		b, etag, err = r.Client.GetBucket(r.Ctx, pid, cid, id)
	}
	if err != nil {
		return nil, "", notFoundOr(err, "bucket", name,
			fmt.Sprintf("check the name with 'cbctl capella bucket list %s'", t.Cluster.Name), "reading bucket "+name)
	}
	return b, etag, nil
}

func newCapellaBucketListCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "list CLUSTER",
		Short: "List the buckets of a cluster",
		Args:  cobra.ExactArgs(1),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		buckets, err := rt.listBuckets(t)
		if err != nil {
			return err
		}
		if rt.JSON {
			views := make([]bucketView, 0, len(buckets))
			for i := range buckets {
				views = append(views, newBucketView(&buckets[i]))
			}
			rt.Out.List(views)
			return nil
		}
		rows := make([][]string, 0, len(buckets))
		for i := range buckets {
			b := &buckets[i]
			storage := b.StorageBackend
			if storage == "" {
				storage = "-"
			}
			rows = append(rows, []string{b.Name, b.Type, storage, strconv.Itoa(b.MemoryAllocationInMb),
				strconv.Itoa(b.Replicas), b.DurabilityLevel, strconv.Itoa(b.TimeToLiveInSeconds),
				strconv.FormatBool(flushEnabled(b))})
		}
		rt.Out.Table([]string{"NAME", "TYPE", "STORAGE", "MEMORY_MB", "REPLICAS", "DURABILITY", "TTL", "FLUSH"}, rows)
		return nil
	}
	return cmd
}

func newCapellaBucketGetCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "get CLUSTER NAME",
		Short: "Show one bucket: settings and usage",
		Args:  cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		b, _, err := rt.readBucket(t, args[1])
		if err != nil {
			return err
		}
		rt.Out.Object(newBucketView(b))
		return nil
	}
	return cmd
}

// bucketAddFlags are the settings of bucket add.
type bucketAddFlags struct {
	typ, backend, durability, conflict, eviction string
	vbuckets, memory, replicas, ttl, priority    int
	flush                                        bool
}

// freeTierRefused lists the flags that were set although a free-tier cluster cannot take them.
func freeTierRefused(cmd *cobra.Command, allowed ...string) error {
	var given []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Name == "token" || f.Name == "org-id" || f.Name == "api-host" || f.Name == "project" || slices.Contains(allowed, f.Name) {
			return
		}
		if cmd.InheritedFlags().Lookup(f.Name) != nil {
			return
		}
		given = append(given, "--"+f.Name)
	})
	if len(given) == 0 {
		return nil
	}
	return usageError("only "+strings.Join(prefixed(allowed), " and ")+" can be set on a free-tier cluster",
		"%s %s", strings.Join(given, ", "), freeTierUnsupported)
}

func prefixed(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "--" + n
	}
	return out
}

func newCapellaBucketAddCmd() *cobra.Command {
	var (
		project string
		f       bucketAddFlags
	)
	cmd := &cobra.Command{
		Use:   "add CLUSTER NAME",
		Short: "Add a bucket to a cluster (no-op when it exists)",
		Long: `Add a bucket to a cluster. A bucket that already exists is left alone.

Couchbase buckets use the couchstore or magma storage backend (--vbuckets needs magma);
ephemeral buckets keep data in memory only, take no --storage-backend and allow the noEviction
and nruEviction policies and the none and majority durability levels. Free-tier clusters only
honour --memory.

Examples:
  cbctl capella bucket add my-cluster orders --memory 1024 --storage-backend magma
  cbctl capella bucket add my-cluster sessions --type ephemeral --memory 256 --eviction-policy nruEviction`,
		Args: cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	fl := cmd.Flags()
	fl.StringVar(&f.typ, "type", "couchbase", "Bucket type: couchbase|ephemeral")
	fl.StringVar(&f.backend, "storage-backend", "", "Storage backend of a couchbase bucket: couchstore|magma (default couchstore)")
	fl.IntVar(&f.vbuckets, "vbuckets", 0, "Number of vBuckets of a magma bucket: 128|1024")
	fl.IntVar(&f.memory, "memory", defaultBucketMemoryMB, "Memory quota in MB")
	fl.IntVar(&f.replicas, "replicas", 1, "Number of replicas, 0-3 (default 1, 0 on a single-node cluster)")
	fl.StringVar(&f.durability, "durability", "none", "Durability level: none|majority|majorityAndPersistActive|persistToMajority")
	fl.IntVar(&f.ttl, "ttl", 0, "Bucket time to live in seconds (0 means none)")
	fl.BoolVar(&f.flush, "flush", false, "Allow flushing the bucket")
	fl.StringVar(&f.conflict, "conflict-resolution", "seqno", "Conflict resolution: seqno|lww")
	fl.StringVar(&f.eviction, "eviction-policy", "", "Eviction policy (couchbase: fullEviction|valueOnly, ephemeral: noEviction|nruEviction)")
	fl.IntVar(&f.priority, "priority", 0, "Recovery priority (Couchbase Server 7.6 or later)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if err := f.validate(changed); err != nil {
			return err
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		name := args[1]
		pid, cid := t.ids()
		free := t.freeTier()
		if free {
			if err := freeTierRefused(cmd, "memory"); err != nil {
				return err
			}
		}
		doc := bucketDoc{Cluster: t.Cluster.Name, Name: name, ID: capellaapi.BucketID(name)}

		exists := func() (bool, error) {
			buckets, err := rt.listBuckets(t)
			if err != nil {
				return false, err
			}
			for _, b := range buckets {
				if b.Name == name {
					return true, nil
				}
			}
			return false, nil
		}
		if ok, err := exists(); err != nil {
			return err
		} else if ok {
			doc.Status = "already exists"
			rt.report(doc, "bucket %s already exists in %s", name, t.Cluster.Name)
			return nil
		}

		if free {
			_, err = rt.Client.CreateFreeBucket(rt.Ctx, pid, cid, capellaapi.Object{"name": name, "memoryAllocationInMb": f.memory})
		} else {
			if !changed("replicas") && t.Cluster.ConfigurationType == "singleNode" {
				f.replicas = 0
			}
			_, err = rt.Client.CreateBucket(rt.Ctx, pid, cid, f.request(name, changed))
		}
		switch {
		case err == nil:
			doc.Status = "created"
		case errors.Is(err, capellaapi.ErrConflict):
			doc.Status = "already exists"
		case isAmbiguous(err):
			ok, lerr := exists()
			if lerr != nil || !ok {
				return err
			}
			doc.Status = "created"
		default:
			return fmt.Errorf("creating bucket %s: %w", name, err)
		}
		if doc.Status == "created" {
			rt.report(doc, "created bucket %s in %s (%s)", name, t.Cluster.Name, doc.ID)
		} else {
			rt.report(doc, "bucket %s already exists in %s", name, t.Cluster.Name)
		}
		return nil
	}
	return cmd
}

// validate checks the flag values and the combinations a bucket type allows, before any request.
func (f *bucketAddFlags) validate(changed func(string) bool) error {
	if !slices.Contains(bucketTypes, f.typ) {
		return usageError("use "+strings.Join(bucketTypes, " or "), "invalid --type %q", f.typ)
	}
	if changed("storage-backend") && !slices.Contains(storageBackends, f.backend) {
		return usageError("use "+strings.Join(storageBackends, " or "), "invalid --storage-backend %q", f.backend)
	}
	if !slices.Contains(durabilityLevels, f.durability) {
		return usageError("use one of "+strings.Join(durabilityLevels, ", "), "invalid --durability %q", f.durability)
	}
	if !slices.Contains(conflictModes, f.conflict) {
		return usageError("use "+strings.Join(conflictModes, " or "), "invalid --conflict-resolution %q", f.conflict)
	}
	if err := checkBucketNumbers(f.memory, f.replicas, f.ttl, f.priority, changed); err != nil {
		return err
	}
	if changed("vbuckets") && !slices.Contains(vbucketCounts, f.vbuckets) {
		return usageError("use 128 or 1024", "invalid --vbuckets %d", f.vbuckets)
	}
	if f.typ == "ephemeral" {
		if changed("storage-backend") {
			return usageError("ephemeral buckets keep data in memory only", "--storage-backend is not allowed for ephemeral buckets")
		}
		if changed("vbuckets") {
			return usageError("vBuckets apply to magma buckets only", "--vbuckets is not allowed for ephemeral buckets")
		}
		if changed("eviction-policy") && !slices.Contains(ephemeralEviction, f.eviction) {
			return usageError("use noEviction or nruEviction", "%s is not valid for ephemeral buckets", f.eviction)
		}
		if !slices.Contains(ephemeralDurable, f.durability) {
			return usageError("ephemeral buckets allow none or majority", "%s needs disk persistence", f.durability)
		}
		return nil
	}
	if changed("eviction-policy") && !slices.Contains(couchbaseEviction, f.eviction) {
		return usageError("use fullEviction or valueOnly; noEviction and nruEviction are for ephemeral buckets",
			"%s is not valid for couchbase buckets", f.eviction)
	}
	if changed("vbuckets") && f.backend != "magma" {
		return usageError("add --storage-backend magma", "--vbuckets is only allowed with --storage-backend magma")
	}
	return nil
}

// checkBucketNumbers validates the numeric bucket flags that were given.
func checkBucketNumbers(memory, replicas, ttl, priority int, changed func(string) bool) error {
	if changed("memory") && memory < 1 {
		return usageError("pass a memory quota in MB", "invalid --memory %d", memory)
	}
	if changed("replicas") && (replicas < 0 || replicas > 3) {
		return usageError("use 0 to 3", "invalid --replicas %d", replicas)
	}
	if changed("ttl") && ttl < 0 {
		return usageError("use 0 or a number of seconds", "invalid --ttl %d", ttl)
	}
	if changed("priority") && priority < 0 {
		return usageError("use 0 or a positive number", "invalid --priority %d", priority)
	}
	return nil
}

// request builds the create body. Settings the user left alone keep Capella's defaults, except
// the ones the API requires.
func (f *bucketAddFlags) request(name string, changed func(string) bool) capellaapi.CreateBucketRequest {
	req := capellaapi.CreateBucketRequest{
		Name: name, Type: f.typ, MemoryAllocationInMb: f.memory, BucketConflictResolution: f.conflict,
		DurabilityLevel: f.durability, Replicas: f.replicas, Flush: f.flush, TimeToLiveInSeconds: f.ttl,
		EvictionPolicy: f.eviction,
	}
	if f.typ == "couchbase" {
		req.StorageBackend = f.backend
		if req.StorageBackend == "" {
			req.StorageBackend = "couchstore"
		}
	}
	if changed("vbuckets") {
		v := f.vbuckets
		req.VBuckets = &v
	}
	if changed("flush") {
		v := f.flush
		req.FlushEnabled = &v
	}
	if changed("priority") {
		v := f.priority
		req.Priority = &v
	}
	return req
}

// bucketUpdateFlags are the settings of bucket update. The immutable ones (type, storage backend,
// vBuckets, conflict resolution, eviction policy) are deliberately not flags.
type bucketUpdateFlags struct {
	memory, replicas, ttl, priority int
	durability                      string
	flush, noFlush                  bool
	versioning, noVersioning        bool
}

func newCapellaBucketUpdateCmd() *cobra.Command {
	var (
		project string
		f       bucketUpdateFlags
	)
	cmd := &cobra.Command{
		Use:   "update CLUSTER NAME",
		Short: "Change a bucket's memory, replicas, durability, TTL, flush or priority",
		Long: `Change a bucket's settings. Only the settings you pass change. The bucket's type, storage
backend, vBuckets, conflict resolution and eviction policy cannot be changed. Free-tier clusters
only honour --memory and --cross-cluster-versioning.

Examples:
  cbctl capella bucket update my-cluster orders --memory 2048
  cbctl capella bucket update my-cluster orders --replicas 2 --durability majority`,
		Args: cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	fl := cmd.Flags()
	fl.IntVar(&f.memory, "memory", 0, "Memory quota in MB")
	fl.IntVar(&f.replicas, "replicas", 0, "Number of replicas, 0-3")
	fl.StringVar(&f.durability, "durability", "", "Durability level: none|majority|majorityAndPersistActive|persistToMajority")
	fl.IntVar(&f.ttl, "ttl", 0, "Bucket time to live in seconds")
	fl.BoolVar(&f.flush, "flush", false, "Allow flushing the bucket")
	fl.BoolVar(&f.noFlush, "no-flush", false, "Do not allow flushing the bucket")
	fl.IntVar(&f.priority, "priority", 0, "Recovery priority (Couchbase Server 7.6 or later)")
	fl.BoolVar(&f.versioning, "cross-cluster-versioning", false, "Enable cross-cluster versioning")
	fl.BoolVar(&f.noVersioning, "no-cross-cluster-versioning", false, "Disable cross-cluster versioning")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		changed := cmd.Flags().Changed
		if err := f.validate(changed); err != nil {
			return err
		}
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		if t.freeTier() {
			if err := freeTierRefused(cmd, "memory", "cross-cluster-versioning", "no-cross-cluster-versioning"); err != nil {
				return err
			}
		}
		name := args[1]
		pid, cid := t.ids()
		id := capellaapi.BucketID(name)
		for attempt := 0; ; attempt++ {
			cur, etag, err := rt.readBucket(t, name)
			if err != nil {
				return err
			}
			var body capellaapi.Object
			if t.freeTier() {
				body = f.freeBody(cur, changed)
				err = rt.Client.UpdateFreeBucket(rt.Ctx, pid, cid, id, body)
			} else {
				body = f.body(cur, changed)
				err = rt.Client.UpdateBucket(rt.Ctx, pid, cid, id, body, etag)
			}
			if errors.Is(err, capellaapi.ErrPreconditionFailed) && attempt == 0 {
				logging.Debug("bucket %s changed while updating; retrying", name)
				continue
			}
			if err != nil {
				return notFoundOr(err, "bucket", name,
					fmt.Sprintf("check the name with 'cbctl capella bucket list %s'", t.Cluster.Name), "updating bucket "+name)
			}
			break
		}
		rt.report(bucketDoc{Cluster: t.Cluster.Name, ID: id, Name: name, Status: "updated"},
			"updated bucket %s in %s", name, t.Cluster.Name)
		return nil
	}
	return cmd
}

// validate checks the flag values and rejects an update that changes nothing.
func (f *bucketUpdateFlags) validate(changed func(string) bool) error {
	if f.flush && f.noFlush {
		return usageError("keep one of them", "both --flush and --no-flush were given")
	}
	if f.versioning && f.noVersioning {
		return usageError("keep one of them", "both --cross-cluster-versioning and --no-cross-cluster-versioning were given")
	}
	if changed("durability") && !slices.Contains(durabilityLevels, f.durability) {
		return usageError("use one of "+strings.Join(durabilityLevels, ", "), "invalid --durability %q", f.durability)
	}
	if err := checkBucketNumbers(f.memory, f.replicas, f.ttl, f.priority, changed); err != nil {
		return err
	}
	if !f.any(changed) {
		return usageError("pass what to change, for example --memory, --replicas, --durability or --ttl", "nothing to update")
	}
	return nil
}

// any reports whether at least one setting was given.
func (f *bucketUpdateFlags) any(changed func(string) bool) bool {
	for _, n := range []string{"memory", "replicas", "durability", "ttl", "flush", "no-flush", "priority",
		"cross-cluster-versioning", "no-cross-cluster-versioning"} {
		if changed(n) {
			return true
		}
	}
	return false
}

// body is the full PUT body: the bucket as it is now with the given settings on top. Capella
// replaces the mutable settings as a whole, so unchanged ones are sent back.
func (f *bucketUpdateFlags) body(cur *capellaapi.Bucket, changed func(string) bool) capellaapi.Object {
	body := capellaapi.Object{
		"memoryAllocationInMb": cur.MemoryAllocationInMb,
		"durabilityLevel":      cur.DurabilityLevel,
		"replicas":             cur.Replicas,
		"timeToLiveInSeconds":  cur.TimeToLiveInSeconds,
	}
	if cur.FlushEnabled != nil || cur.Flush != nil {
		body["flushEnabled"] = flushEnabled(cur)
	}
	if cur.EnableCrossClusterVersioning != nil {
		body["enableCrossClusterVersioning"] = *cur.EnableCrossClusterVersioning
	}
	if cur.Priority > 0 {
		body["priority"] = cur.Priority
	}
	if changed("memory") {
		body["memoryAllocationInMb"] = f.memory
	}
	if changed("replicas") {
		body["replicas"] = f.replicas
	}
	if changed("durability") {
		body["durabilityLevel"] = f.durability
	}
	if changed("ttl") {
		body["timeToLiveInSeconds"] = f.ttl
	}
	if changed("flush") || changed("no-flush") {
		body["flushEnabled"] = f.flush
	}
	if changed("priority") {
		body["priority"] = f.priority
	}
	f.setVersioning(body, changed)
	return body
}

// freeBody is the PUT body of a free-tier bucket: memory and cross-cluster versioning only.
func (f *bucketUpdateFlags) freeBody(cur *capellaapi.Bucket, changed func(string) bool) capellaapi.Object {
	body := capellaapi.Object{"memoryAllocationInMb": cur.MemoryAllocationInMb}
	if cur.EnableCrossClusterVersioning != nil {
		body["enableCrossClusterVersioning"] = *cur.EnableCrossClusterVersioning
	}
	if changed("memory") {
		body["memoryAllocationInMb"] = f.memory
	}
	f.setVersioning(body, changed)
	return body
}

func (f *bucketUpdateFlags) setVersioning(body capellaapi.Object, changed func(string) bool) {
	if changed("cross-cluster-versioning") || changed("no-cross-cluster-versioning") {
		body["enableCrossClusterVersioning"] = f.versioning
	}
}

func newCapellaBucketDeleteCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "delete CLUSTER NAME",
		Short: "Delete a bucket and all its data",
		Args:  cobra.ExactArgs(2),
	}
	af := addAPIFlags(cmd)
	addProjectFlag(cmd, &project)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := newRuntime(cmd, af)
		if err != nil {
			return err
		}
		t, err := rt.locateCluster(args[0], project)
		if err != nil {
			return err
		}
		name := args[1]
		if _, _, err := rt.readBucket(t, name); err != nil {
			return err
		}
		if err := t.refuseIfProtected(); err != nil {
			return err
		}
		question := fmt.Sprintf("Delete bucket %s and all its data from %s?", name, t.Cluster.Name)
		if ok, err := rt.confirmDelete(question); err != nil || !ok {
			return err
		}
		pid, cid := t.ids()
		id := capellaapi.BucketID(name)
		if t.freeTier() {
			err = rt.Client.DeleteFreeBucket(rt.Ctx, pid, cid, id)
		} else {
			err = rt.Client.DeleteBucket(rt.Ctx, pid, cid, id)
		}
		if err != nil {
			return notFoundOr(err, "bucket", name,
				fmt.Sprintf("check the name with 'cbctl capella bucket list %s'", t.Cluster.Name), "deleting bucket "+name)
		}
		rt.report(bucketDoc{Cluster: t.Cluster.Name, ID: id, Name: name, Status: "deleted"},
			"deleted bucket %s from %s", name, t.Cluster.Name)
		return nil
	}
	return cmd
}
