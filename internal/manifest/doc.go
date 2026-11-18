// Package manifest is the offline model of "cbctl apply -f" manifests: the apiVersion/kind
// envelope, the CouchbaseServer and CouchbaseCapella specs, strict decoding with line-numbered
// errors, validation, defaults and secret references.
//
// Nothing here prints or contacts a network. Load reads and validates every document of a file
// and returns a *ValidationError (exit code 3) listing all problems at once. Document.Resolve
// turns the secret references into values just before apply. The execution packages
// (serverapply, capellaapply) build on the types here.
package manifest
