package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mminichino/cbctl/internal/capellaapi"
	"github.com/mminichino/cbctl/internal/engine"
	"github.com/mminichino/cbctl/internal/logging"
	"github.com/spf13/cobra"
)

// isPreconditionFailed reports whether err is Capella's 412 (stale ETag).
func isPreconditionFailed(err error) bool { return errors.Is(err, capellaapi.ErrPreconditionFailed) }

// powerOffNotes explains what turning a cluster off keeps.
const powerOffNotes = "Turning it off stops the compute; storage remains. All data, schema (buckets, scopes, collections)\n" +
	"and indexes remain, as well as the cluster configuration, including users and allow lists.\n" +
	"Linked App Services are turned off too. Charges for the Data API continue."

// powerResult is the JSON document of cluster power.
type powerResult struct {
	Cluster   string `json:"cluster"`
	ClusterID string `json:"clusterId"`
	State     string `json:"state"`  // on | off | turningOn | turningOff
	Status    string `json:"status"` // changed | unchanged | accepted | cancelled
}

func newCapellaClusterPowerCmd() *cobra.Command {
	var on, off, withApp bool
	var project string
	cmd := capellaLeaf("power CLUSTER", "Turn a cluster off (storage and data kept) or on again", cobra.ExactArgs(1),
		func([]string) error {
			switch {
			case on && off:
				return usageError("use either --on or --off", "--on and --off are mutually exclusive")
			case !on && !off:
				return usageError("add --on to turn the cluster on, or --off to turn it off", "nothing to do: neither --on nor --off was given")
			case withApp && !on:
				return usageError("add --on, or remove --with-app-service", "--with-app-service only applies with --on")
			}
			return nil
		},
		func(rt *runtime, args []string) error {
			p, cl, err := rt.resolveCluster(args[0], project)
			if err != nil {
				return err
			}
			return rt.runPower(p, cl, on, withApp, true)
		})
	cmd.Long = "Turn a cluster off to stop paying for compute (storage, data and configuration stay) or on again.\n\n" +
		"Examples:\n  cbctl capella cluster power dev-01 --off\n  cbctl capella cluster power dev-01 --on\n  cbctl capella cluster power dev-01 --on --with-app-service"
	addProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&on, "on", false, "Turn the cluster on")
	cmd.Flags().BoolVar(&off, "off", false, "Turn the cluster off")
	cmd.Flags().BoolVar(&withApp, "with-app-service", false, "With --on: also turn on the linked App Service")
	return cmd
}

func powerWord(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// runPower brings a cluster to on (healthy) or off (turnedOff), waiting unless --no-wait. It is
// shared with schedule pause --power-on (port of power_cmds.power).
func (rt *runtime) runPower(p *capellaapi.Project, cl *capellaapi.Cluster, on, withApp, ask bool) error {
	free := isFreeCluster(cl)
	target := "turnedOff"
	if on {
		target = "healthy"
	}
	res := powerResult{Cluster: cl.Name, ClusterID: cl.ID, State: powerWord(on), Status: "unchanged"}
	current := cl.CurrentState
	for attempt := 1; attempt <= 2; attempt++ {
		if current == target {
			rt.report(res, "cluster %s is already %s; nothing to do", cl.Name, powerWord(on))
			return nil
		}
		if current == "turningOn" || current == "turningOff" {
			if rt.NoWait {
				return blockedError("wait until it is healthy or turned off (see 'cbctl capella cluster get "+cl.Name+"'), then run the command again",
					"cluster %s is %s", cl.Name, current)
			}
			op, going := engine.OpPowerOff, "turnedOff"
			if current == "turningOn" {
				op, going = engine.OpPowerOn, "healthy"
			}
			if _, err := rt.waitCluster(p.ID, cl.ID, cl.Name, op, free); err != nil {
				return err
			}
			if going == target {
				res.Status = "changed"
				rt.report(res, "cluster %s is %s", cl.Name, powerWord(on))
				return nil
			}
			current = going
		}
		if current != "healthy" && current != "turnedOff" {
			need := "healthy"
			if on {
				need = "turned off"
			}
			return blockedError(fmt.Sprintf("wait until it is %s (see 'cbctl capella cluster get %s'), then run the command again", need, cl.Name),
				"cluster %s is %s; it can be turned %s only when it is %s", cl.Name, current, powerWord(on), need)
		}
		if !on && ask && attempt == 1 {
			fmt.Fprintln(rt.errOut(), powerOffNotes)
			ok, err := confirm(rt, fmt.Sprintf("Turn off cluster %s?", cl.Name))
			if err != nil {
				return err
			}
			if !ok {
				res.Status, res.State = "cancelled", cl.CurrentState
				rt.report(res, "cancelled; the cluster is unchanged")
				return nil
			}
		}
		err := rt.sendPower(p.ID, cl.ID, on, withApp, free)
		if err == nil {
			break
		}
		if !errors.Is(err, capellaapi.ErrConflict) {
			return err
		}
		if attempt == 2 {
			return &CLIError{Code: ExitError, Message: fmt.Sprintf("Capella refused: %v", err),
				Action: "someone else is changing the cluster: wait until that finishes, then run the command again", Err: err}
		}
		again, rerr := rt.readCluster(p.ID, cl.ID, free)
		if rerr != nil {
			return rerr
		}
		current = again.CurrentState
	}
	if rt.NoWait {
		res.Status, res.State = "accepted", "turningOff"
		if on {
			res.State = "turningOn"
		}
		rt.report(res, "cluster %s is turning %s (--no-wait)", cl.Name, powerWord(on))
		return nil
	}
	op := engine.OpPowerOff
	if on {
		op = engine.OpPowerOn
	}
	logging.Info("power %s accepted for cluster %s", powerWord(on), cl.Name)
	if _, err := rt.waitCluster(p.ID, cl.ID, cl.Name, op, free); err != nil {
		return err
	}
	res.Status = "changed"
	if on {
		rt.report(res, "cluster %s is on", cl.Name)
		return nil
	}
	rt.report(res, "cluster %s is off (storage, data and configuration retained)", cl.Name)
	if note := rt.scheduleNote(p.ID, cl.ID); note != "" && !rt.JSON {
		rt.Out.Line("%s", note)
	}
	return nil
}

func (rt *runtime) sendPower(projectID, clusterID string, on, withApp, free bool) error {
	switch {
	case free && on:
		return rt.Client.TurnFreeClusterOn(rt.Ctx, projectID, clusterID)
	case free:
		return rt.Client.TurnFreeClusterOff(rt.Ctx, projectID, clusterID)
	case on:
		return rt.Client.TurnClusterOn(rt.Ctx, projectID, clusterID, withApp)
	}
	return rt.Client.TurnClusterOff(rt.Ctx, projectID, clusterID)
}

// scheduleNote returns "note: the on/off schedule will turn this cluster on at Mon 08:00 PT",
// or "" when there is no active schedule or no next on-window.
func (rt *runtime) scheduleNote(projectID, clusterID string) string {
	body, _, err := rt.Client.GetSchedule(rt.Ctx, projectID, clusterID)
	if err != nil || body["activationStatus"] == "paused" {
		return ""
	}
	if when := nextOnText(body, rt.Clock.Now()); when != "" {
		return "note: the on/off schedule will turn this cluster on at " + when
	}
	return ""
}

// ---- protect ---------------------------------------------------------------------------------

type protectResult struct {
	Cluster            string `json:"cluster"`
	ClusterID          string `json:"clusterId"`
	DeletionProtection bool   `json:"deletionProtection"`
	Status             string `json:"status"` // changed | unchanged | cancelled
}

func newCapellaClusterProtectCmd() *cobra.Command {
	var on, off bool
	var project string
	cmd := capellaLeaf("protect CLUSTER", "Turn deletion protection on or off", cobra.ExactArgs(1),
		func([]string) error {
			switch {
			case on && off:
				return usageError("use either --on or --off", "--on and --off are mutually exclusive")
			case !on && !off:
				return usageError("add --on to turn protection on, or --off to turn it off", "nothing to do: neither --on nor --off was given")
			}
			return nil
		},
		func(rt *runtime, args []string) error {
			t, err := rt.locateCluster(args[0], project)
			if err != nil {
				return err
			}
			if t.freeTier() {
				return blockedError("deletion protection is for provisioned clusters", "free-tier clusters have no deletion protection")
			}
			res := protectResult{Cluster: t.Cluster.Name, ClusterID: t.Cluster.ID, DeletionProtection: on, Status: "unchanged"}
			if t.protected() == on {
				rt.report(res, "deletion protection is already %s for %s; nothing to do", powerWord(on), t.Cluster.Name)
				return nil
			}
			if !on {
				ok, err := confirm(rt, fmt.Sprintf("Turn off deletion protection for %s? The cluster and its buckets can then be deleted.", t.Cluster.Name))
				if err != nil {
					return err
				}
				if !ok {
					res.Status, res.DeletionProtection = "cancelled", true
					rt.report(res, "cancelled; deletion protection is unchanged")
					return nil
				}
			}
			if err := rt.Client.SetDeletionProtection(rt.Ctx, t.Project.ID, t.Cluster.ID, on); err != nil {
				return conflictAsBlocked(err, t.Cluster.Name)
			}
			res.Status = "changed"
			rt.report(res, "deletion protection %s for %s", powerWord(on), t.Cluster.Name)
			return nil
		})
	addProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&on, "on", false, "Turn deletion protection on")
	cmd.Flags().BoolVar(&off, "off", false, "Turn deletion protection off (needed before delete)")
	return cmd
}

// ---- delete ----------------------------------------------------------------------------------

type clusterDeleteResult struct {
	Cluster           string `json:"cluster"`
	ClusterID         string `json:"clusterId,omitempty"`
	Status            string `json:"status"` // deleted | accepted | absent | cancelled
	SnapshotsRetained bool   `json:"snapshotsRetained"`
}

func newCapellaClusterDeleteCmd() *cobra.Command {
	var project string
	var discard, cascade bool
	cmd := capellaLeaf("delete CLUSTER", "Delete a cluster and all its data and wait until it is gone", cobra.ExactArgs(1), nil,
		func(rt *runtime, args []string) error {
			return rt.runClusterDelete(args[0], project, discard)
		})
	cmd.Long = "Delete a cluster with its buckets, data, schedule and allowed CIDRs. Snapshot backups are kept unless\n" +
		"--discard-snapshots is given. You confirm by typing the cluster name (or pass --yes).\n\n" +
		"Examples:\n  cbctl capella cluster delete dev-01\n  cbctl capella cluster delete dev-01 --discard-snapshots --yes --no-wait"
	addProjectFlag(cmd, &project)
	cmd.Flags().BoolVar(&discard, "discard-snapshots", false, "Also delete the cluster's snapshot backups (kept by default)")
	cmd.Flags().BoolVar(&cascade, "cascade", false, "Accepted for symmetry with 'project delete --cascade': a cluster's buckets, schedule and allowed CIDRs are always deleted with it")
	return cmd
}

func isNotFoundCLI(err error) bool {
	var ce *CLIError
	return errors.As(err, &ce) && ce.Code == ExitConfigInvalid && strings.HasSuffix(ce.Message, " not found")
}

func (rt *runtime) runClusterDelete(name, project string, discard bool) error {
	res := clusterDeleteResult{Cluster: name, SnapshotsRetained: !discard}
	t, err := rt.locateCluster(name, project)
	switch {
	case err != nil && isNotFoundCLI(err):
		res.Status = "absent"
		rt.report(res, "cluster %s not found; nothing to do", name)
		return nil
	case err != nil:
		return err
	}
	cl, free := t.Cluster, t.freeTier()
	res.ClusterID = cl.ID
	if cl.CurrentState == "destroying" {
		logging.Info("cluster %s is already being deleted", cl.Name)
		return rt.finishDelete(t, &res, discard)
	}
	if err := t.refuseIfProtected(); err != nil {
		return err
	}
	for _, line := range rt.deleteDetails(t, discard) {
		rt.Out.Line("%s", line)
	}
	ok, err := confirmTyped(rt, "Type the cluster name to confirm deletion", cl.Name)
	if err != nil {
		return err
	}
	if !ok {
		res.Status = "cancelled"
		rt.report(res, "cancelled; nothing was deleted")
		return nil
	}
	if free {
		err = rt.Client.DeleteFreeCluster(rt.Ctx, t.Project.ID, cl.ID)
	} else {
		err = rt.Client.DeleteCluster(rt.Ctx, t.Project.ID, cl.ID, !discard)
	}
	switch {
	case errors.Is(err, capellaapi.ErrNotFound):
		res.Status = "deleted"
		rt.report(res, "deleted cluster %s", cl.Name)
		return nil
	case err != nil:
		return conflictAsBlocked(err, cl.Name)
	}
	logging.Info("delete accepted for cluster %s", cl.Name)
	return rt.finishDelete(t, &res, discard)
}

// finishDelete waits for the cluster to disappear (unless --no-wait) and reports.
func (rt *runtime) finishDelete(t *clusterTarget, res *clusterDeleteResult, discard bool) error {
	cl := t.Cluster
	if rt.NoWait {
		res.Status = "accepted"
		rt.report(*res, "cluster %s is being deleted (--no-wait); run this command again to wait until it is gone", cl.Name)
		return nil
	}
	if _, err := rt.waitCluster(t.Project.ID, cl.ID, cl.Name, engine.OpDelete, t.freeTier()); err != nil {
		return err
	}
	res.Status = "deleted"
	if t.freeTier() {
		rt.report(*res, "deleted cluster %s", cl.Name)
		return nil
	}
	kept := "retained"
	if discard {
		kept = "deleted"
	}
	rt.report(*res, "deleted cluster %s (snapshot backups %s)", cl.Name, kept)
	return nil
}

// deleteDetails lists what a delete takes with it.
func (rt *runtime) deleteDetails(t *clusterTarget, discard bool) []string {
	cl := t.Cluster
	nodes := totalNodes(cl.ServiceGroups)
	var buckets []capellaapi.Bucket
	var err error
	if t.freeTier() {
		buckets, _, err = rt.Client.ListFreeBuckets(rt.Ctx, t.Project.ID, cl.ID)
	} else {
		buckets, err = rt.Client.ListBuckets(rt.Ctx, t.Project.ID, cl.ID)
	}
	if err != nil {
		logging.Debug("listing buckets for the delete summary: %v", err)
	}
	plural := func(n int, word string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, word)
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	lines := []string{fmt.Sprintf("  - cluster %s  %s %s · %s · %s", cl.Name, clusterProvider(cl), clusterRegion(cl),
		plural(nodes, "node"), plural(len(buckets), "bucket"))}
	if len(buckets) > 0 {
		names := make([]string, len(buckets))
		for i, b := range buckets {
			names[i] = b.Name
		}
		sort.Strings(names)
		lines = append(lines, "    buckets deleted with the cluster: "+strings.Join(names, ", "))
	}
	if t.freeTier() {
		return lines
	}
	if sched, _, err := rt.Client.GetSchedule(rt.Ctx, t.Project.ID, cl.ID); err == nil && sched != nil {
		lines = append(lines, fmt.Sprintf("    on/off schedule (%v) is deleted with the cluster", sched["timezone"]))
	}
	if discard {
		lines = append(lines, "    WARNING: snapshot backups will be deleted")
	} else {
		lines = append(lines, "    snapshot backups are kept (--discard-snapshots deletes them)")
	}
	return lines
}
