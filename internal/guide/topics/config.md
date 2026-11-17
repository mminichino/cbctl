# The config file (capella.yaml)

One file describes your organization: every project (`projects:`), its clusters, and for each cluster
its scaling, hibernation schedule, allowed IPs, buckets, scopes, collections and credentials.
Field names are the same as in the Capella Management API request bodies. Files in the older
single-project form (`project:` + `clusters:`) still work and are converted on the next write.
cbctl writes and maintains this file (init, sync, project create, cluster create/update/scale).

**Where it is found** (first match wins): `--config PATH`, `CBCTL_CONFIG`, the profile's
`configFile`, `./capella.yaml`, `./capella.yml`. Having both `.yaml` and `.yml` is an error.

**Secrets and variables**
- `${VAR}` is replaced by an environment variable, `${file:./path}` by a file's content.
- Passwords must be `${VAR}` or `${file:...}`; a literal password is rejected.
- The API key never goes in this file.

**Defaults** applied before comparing: bucket `type: couchbase`, `replicas: 1`,
`durabilityLevel: none`, `timeToLiveInSeconds: 0`, `flushEnabled: false`,
`bucketConflictResolution: seqno`; cluster `configurationType: multiNode`.

**Checks** `cbctl capella validate` runs offline: schema and allowed values, node limits, disk type per
cloud provider, unique names, and that credentials only reference buckets, scopes and collections
defined in the file (or marked `external: true`).

Look up any field: `cbctl capella guide config clusters.buckets.storageBackend`.

**Editing by hand.** Files written by cbctl round-trip byte for byte. A hand-edited file keeps its comments and
key order when cbctl edits it, but may be re-indented on the first save; the previous text is always kept as
`capella.yaml.bak`. Files that use YAML anchors or aliases cannot be edited safely by cbctl and are refused.

**apiVersion** is `cbctl/v1`; `capellactl/v1` files are accepted unchanged.
