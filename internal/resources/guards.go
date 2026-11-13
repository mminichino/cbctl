package resources

import "fmt"

// DeletionProtected reports whether the cluster, as Capella returned it, has deletion protection on.
// Every delete path (cluster delete, bucket delete or prune, destroy, project delete --cascade)
// asks this before any write.
func DeletionProtected(cluster Obj) bool {
	return truthy(cluster["deletionProtection"])
}

// ProtectionMessage is the refusal shown when a delete path meets a protected cluster.
func ProtectionMessage(cluster string) string {
	return fmt.Sprintf("deletion protection is on for %s; run 'cbctl capella cluster protect %s --off' first", cluster, cluster)
}
