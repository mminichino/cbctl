package capellaapi

// Endpoint is one Capella Management API operation as a method and path template.
type Endpoint struct {
	Method string
	Path   string
}

const (
	invOrg = "/v4/organizations/{organizationId}"
	invPrj = invOrg + "/projects/{projectId}"
	invClu = invPrj + "/clusters/{clusterId}"
	invBkt = invClu + "/buckets/{bucketId}"
)

// Inventory lists every API operation the client wraps. It is ported from capellactl's INVENTORY
// and lets tests prove that each operation is exercised by a contract test.
var Inventory = []Endpoint{
	{"GET", "/v4/organizations"}, {"GET", invOrg},
	{"GET", invOrg + "/projects"}, {"POST", invOrg + "/projects"}, {"GET", invPrj}, {"PUT", invPrj}, {"DELETE", invPrj},
	{"GET", invPrj + "/clusters"}, {"POST", invPrj + "/clusters"}, {"GET", invClu}, {"PUT", invClu}, {"DELETE", invClu},
	{"PUT", invClu + "/deletionProtection"}, {"POST", invClu + "/activationState"}, {"DELETE", invClu + "/activationState"},
	{"GET", invClu + "/stats"},
	{"POST", invPrj + "/clusters/freeTier"}, {"GET", invPrj + "/clusters/freeTier/{clusterId}"},
	{"PUT", invPrj + "/clusters/freeTier/{clusterId}"}, {"DELETE", invPrj + "/clusters/freeTier/{clusterId}"},
	{"POST", invPrj + "/clusters/freeTier/{clusterId}/activationState"},
	{"DELETE", invPrj + "/clusters/freeTier/{clusterId}/activationState"},
	{"GET", invClu + "/onOffSchedule"}, {"POST", invClu + "/onOffSchedule"}, {"PUT", invClu + "/onOffSchedule"},
	{"DELETE", invClu + "/onOffSchedule"}, {"POST", invClu + "/onOffSchedule/activationState"},
	{"DELETE", invClu + "/onOffSchedule/activationState"},
	{"GET", invClu + "/allowedcidrs"}, {"POST", invClu + "/allowedcidrs"}, {"GET", invClu + "/allowedcidrs/{allowedCidrId}"},
	{"DELETE", invClu + "/allowedcidrs/{allowedCidrId}"},
	{"GET", invClu + "/buckets"}, {"POST", invClu + "/buckets"}, {"GET", invBkt}, {"PUT", invBkt}, {"DELETE", invBkt},
	{"GET", invClu + "/buckets/freeTier"}, {"POST", invClu + "/buckets/freeTier"},
	{"GET", invClu + "/buckets/freeTier/{bucketId}"}, {"PUT", invClu + "/buckets/freeTier/{bucketId}"},
	{"DELETE", invClu + "/buckets/freeTier/{bucketId}"},
	{"GET", invBkt + "/scopes"}, {"POST", invBkt + "/scopes"}, {"GET", invBkt + "/scopes/{scopeName}"},
	{"DELETE", invBkt + "/scopes/{scopeName}"},
	{"GET", invBkt + "/scopes/{scopeName}/collections"}, {"POST", invBkt + "/scopes/{scopeName}/collections"},
	{"GET", invBkt + "/scopes/{scopeName}/collections/{collectionName}"},
	{"PUT", invBkt + "/scopes/{scopeName}/collections/{collectionName}"},
	{"DELETE", invBkt + "/scopes/{scopeName}/collections/{collectionName}"},
	{"GET", invClu + "/users"}, {"POST", invClu + "/users"}, {"GET", invClu + "/users/{userId}"}, {"PUT", invClu + "/users/{userId}"},
	{"DELETE", invClu + "/users/{userId}"},
	{"GET", invClu + "/cloudsnapshotbackupschedule"}, {"PUT", invClu + "/cloudsnapshotbackupschedule"},
	{"DELETE", invClu + "/cloudsnapshotbackupschedule"}, {"GET", invClu + "/cloudsnapshotbackupschedule/options"},
	{"GET", invClu + "/cloudsnapshotbackups/regions"}, {"GET", invClu + "/cloudsnapshotbackups"},
	{"GET", invPrj + "/cloudsnapshotbackups"}, {"POST", invClu + "/cloudsnapshotbackups"},
	{"PUT", invClu + "/cloudsnapshotbackups/{backupId}"}, {"DELETE", invClu + "/cloudsnapshotbackups/{backupId}"},
	{"POST", invClu + "/cloudsnapshotbackups/{backupId}/restore"}, {"GET", invClu + "/cloudsnapshotbackups/restores"},
	{"GET", invBkt + "/backup/schedules"}, {"POST", invBkt + "/backup/schedules"}, {"PUT", invBkt + "/backup/schedules"},
	{"DELETE", invBkt + "/backup/schedules"}, {"GET", invBkt + "/backup/cycles"}, {"GET", invBkt + "/backup/cycles/{cycleId}"},
	{"POST", invBkt + "/backups"}, {"GET", invClu + "/backups"}, {"GET", invClu + "/backups/{backupId}"},
	{"DELETE", invClu + "/backups/{backupId}"}, {"POST", invClu + "/backups/{backupId}/restore"},
	{"POST", invBkt + "/backups/{backupId}/export"}, {"GET", invBkt + "/backups/{backupId}/exports/{exportId}"},
}
