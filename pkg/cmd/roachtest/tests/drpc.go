// Copyright 2026 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package tests

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/cluster"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/option"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/registry"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/roachtestutil/mixedversion"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/spec"
	"github.com/cockroachdb/cockroach/pkg/cmd/roachtest/test"
	"github.com/cockroachdb/cockroach/pkg/roachprod/install"
	"github.com/cockroachdb/cockroach/pkg/roachprod/logger"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
)

func registerDRPC(r registry.Registry) {
	// Test 1: Toggle DRPC via cluster setting (no restarts). (For 26.1 only)
	r.Add(registry.TestSpec{
		Name:             "drpc/toggle-setting",
		Owner:            registry.OwnerServer,
		Cluster:          r.MakeClusterSpec(8, spec.CPU(16), spec.WorkloadNode()),
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Leases:           registry.MetamorphicLeases,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runDRPCToggleSetting(ctx, t, c)
		},
	})

	//// Test 2: Enable DRPC via environment variable with rolling restart. (For 26.1 only)
	r.Add(registry.TestSpec{
		Name:             "drpc/rolling-restart",
		Owner:            registry.OwnerServer,
		Cluster:          r.MakeClusterSpec(8, spec.CPU(16), spec.WorkloadNode()),
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.Nightly),
		Leases:           registry.MetamorphicLeases,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runDRPCRollingRestart(ctx, t, c)
		},
	})

	// Test 3: DRPC upgrade compatibility. Use MVT_UPGRADE_PATH to control
	// the upgrade path, e.g.:
	//   MVT_UPGRADE_PATH="25.4,current" roachtest run drpc/mixed-version
	//   MVT_UPGRADE_PATH="26.1,current" roachtest run drpc/mixed-version
	r.Add(registry.TestSpec{
		Name:             "drpc/mixed-version",
		Owner:            registry.OwnerServer,
		Cluster:          r.MakeClusterSpec(5, spec.WorkloadNode()),
		CompatibleClouds: registry.AllClouds,
		Suites:           registry.Suites(registry.MixedVersion),
		Monitor:          true,
		Randomized:       true,
		Run: func(ctx context.Context, t test.Test, c cluster.Cluster) {
			runDRPCMixedVersion(ctx, t, c)
		},
	})
}

// (For 26.1 only)
//
// runDRPCToggleSetting tests the ability to enable and disable DRPC at runtime
// using the cluster setting, running workloads in each state to verify
// functionality.
func runDRPCToggleSetting(ctx context.Context, t test.Test, c cluster.Cluster) {
	crdbNodes := c.CRDBNodes()

	t.Status("starting cluster without DRPC")
	settings := install.MakeClusterSettings()
	c.Start(ctx, t.L(), option.NewStartOpts(option.NoBackupSchedule), settings, crdbNodes)

	db := c.Conn(ctx, t.L(), 1)
	defer db.Close()

	// Wait for cluster to be fully up.
	err := roachtestutil.WaitFor3XReplication(ctx, t.L(), db)
	require.NoError(t, err)

	// Verify DRPC is initially disabled (default).
	var drpcEnabled bool
	err = db.QueryRowContext(ctx, "SHOW CLUSTER SETTING rpc.experimental_drpc.enabled").Scan(&drpcEnabled)
	require.NoError(t, err)
	t.L().Printf("Initial DRPC setting: %v", drpcEnabled)

	workloadDuration := roachtestutil.IfLocal(c, "1800s", "30m")

	// Phase 1: Run workload with gRPC (default).
	t.Status("running workload with gRPC (default)")
	runDRPCWorkload(ctx, t, c, workloadDuration, "grpc-baseline")

	// Phase 2: Enable DRPC via cluster setting.
	t.Status("enabling DRPC via cluster setting")
	_, err = db.ExecContext(ctx, "SET CLUSTER SETTING rpc.experimental_drpc.enabled = true")
	require.NoError(t, err)

	// Verify the setting took effect.
	err = db.QueryRowContext(ctx, "SHOW CLUSTER SETTING rpc.experimental_drpc.enabled").Scan(&drpcEnabled)
	require.NoError(t, err)
	require.True(t, drpcEnabled, "DRPC should be enabled after setting cluster setting")
	t.L().Printf("DRPC enabled: %v", drpcEnabled)

	// Give some time for new connections to use DRPC.
	time.Sleep(5 * time.Second)

	// Phase 3: Run workload with DRPC enabled.
	t.Status("running workload with DRPC enabled")
	runDRPCWorkload(ctx, t, c, workloadDuration, "drpc-enabled")

	// Phase 4: Disable DRPC via cluster setting.
	t.Status("disabling DRPC via cluster setting")
	_, err = db.ExecContext(ctx, "SET CLUSTER SETTING rpc.experimental_drpc.enabled = false")
	require.NoError(t, err)

	// Verify the setting took effect.
	err = db.QueryRowContext(ctx, "SHOW CLUSTER SETTING rpc.experimental_drpc.enabled").Scan(&drpcEnabled)
	require.NoError(t, err)
	require.False(t, drpcEnabled, "DRPC should be disabled after setting cluster setting")
	t.L().Printf("DRPC disabled: %v", drpcEnabled)

	// Give some time for new connections to use gRPC.
	time.Sleep(5 * time.Second)

	// Phase 5: Run workload with gRPC again.
	t.Status("running workload with gRPC (after disabling DRPC)")
	runDRPCWorkload(ctx, t, c, workloadDuration, "grpc-after-toggle")

	t.Status("DRPC toggle via cluster setting completed successfully")
}

// (For 26.1 only)
//
// runDRPCRollingRestart tests enabling DRPC via environment variable using
// a rolling restart approach, then disabling it the same way.
func runDRPCRollingRestart(ctx context.Context, t test.Test, c cluster.Cluster) {
	crdbNodes := c.CRDBNodes()

	// Phase 1: Start cluster without DRPC.
	t.Status("starting cluster without DRPC")
	settings := install.MakeClusterSettings()
	startOpts := option.NewStartOpts(option.NoBackupSchedule)
	c.Start(ctx, t.L(), startOpts, settings, crdbNodes)

	db := c.Conn(ctx, t.L(), 1)
	defer db.Close()

	// Wait for cluster to be fully up.
	err := roachtestutil.WaitFor3XReplication(ctx, t.L(), db)
	require.NoError(t, err)

	workloadDuration := roachtestutil.IfLocal(c, "1800s", "30m")

	// Phase 2: Run workload with gRPC (baseline).
	t.Status("running workload with gRPC (baseline)")
	runDRPCWorkload(ctx, t, c, workloadDuration, "grpc-baseline")

	// Phase 3: Rolling restart with DRPC enabled via environment variable.
	t.Status("performing rolling restart to enable DRPC via environment variable")
	drpcSettings := install.MakeClusterSettings()
	drpcSettings.Env = append(drpcSettings.Env, "COCKROACH_EXPERIMENTAL_DRPC_ENABLED=true")

	for _, node := range crdbNodes {
		t.Status(fmt.Sprintf("restarting node %d with DRPC enabled", node))

		// Stop the node gracefully.
		stopOpts := option.NewStopOpts()
		stopOpts.RoachprodOpts.Sig = 15 // SIGTERM for graceful shutdown
		stopOpts.RoachprodOpts.Wait = true
		c.Stop(ctx, t.L(), stopOpts, c.Node(node))

		// Start the node with DRPC environment variable.
		c.Start(ctx, t.L(), startOpts, drpcSettings, c.Node(node))

		// Wait for the node to be healthy.
		t.Status(fmt.Sprintf("waiting for node %d to be healthy", node))
		err := waitForNodeHealth(ctx, t, c, node)
		require.NoError(t, err, "node %d failed to become healthy after restart", node)

		t.L().Printf("Node %d restarted successfully with DRPC enabled", node)
	}

	// Give some time for connections to stabilize.
	time.Sleep(10 * time.Second)

	// Phase 4: Run workload with DRPC enabled (via env var).
	t.Status("running workload with DRPC enabled (via env var)")
	runDRPCWorkload(ctx, t, c, workloadDuration, "drpc-via-env")

	// Phase 5: Rolling restart to disable DRPC (remove environment variable).
	t.Status("performing rolling restart to disable DRPC")
	grpcSettings := install.MakeClusterSettings() // No DRPC env var

	for _, node := range crdbNodes {
		t.Status(fmt.Sprintf("restarting node %d without DRPC", node))

		// Stop the node gracefully.
		stopOpts := option.NewStopOpts()
		stopOpts.RoachprodOpts.Sig = 15
		stopOpts.RoachprodOpts.Wait = true
		c.Stop(ctx, t.L(), stopOpts, c.Node(node))

		// Start the node without DRPC environment variable.
		c.Start(ctx, t.L(), startOpts, grpcSettings, c.Node(node))

		// Wait for the node to be healthy.
		t.Status(fmt.Sprintf("waiting for node %d to be healthy", node))
		err := waitForNodeHealth(ctx, t, c, node)
		require.NoError(t, err, "node %d failed to become healthy after restart", node)

		t.L().Printf("Node %d restarted successfully without DRPC", node)
	}

	// Give some time for connections to stabilize.
	time.Sleep(10 * time.Second)

	// Phase 6: Run workload with gRPC again.
	t.Status("running workload with gRPC (after rolling restart)")
	runDRPCWorkload(ctx, t, c, workloadDuration, "grpc-after-rolling-restart")

	t.Status("DRPC rolling restart test completed successfully")
}

// waitForNodeHealth waits for a node to become healthy after restart.
func waitForNodeHealth(ctx context.Context, t test.Test, c cluster.Cluster, node int) error {
	db := c.Conn(ctx, t.L(), node)
	defer db.Close()

	// Simple health check - try to run a query.
	for i := 0; i < 30; i++ {
		var result int
		err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result)
		if err == nil && result == 1 {
			return nil
		}
		t.L().Printf("Node %d health check attempt %d failed: %v", node, i+1, err)
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("node %d did not become healthy within timeout", node)
}

// runDRPCWorkload runs a KV workload for the specified duration.
func runDRPCWorkload(
	ctx context.Context, t test.Test, c cluster.Cluster, duration string, phase string,
) {
	t.L().Printf("Starting workload phase: %s (duration: %s)", phase, duration)

	nodes := c.CRDBNodes()
	cmd := fmt.Sprintf(
		"./cockroach workload run tpcc --tolerate-errors --init --user=%s --password=%s "+
			"--concurrency=64 --read-percent=50 --duration=%s {pgurl:1-%d}",
		install.DefaultUser, install.DefaultPassword, duration, len(nodes),
	)

	err := c.RunE(ctx, option.WithNodes(c.WorkloadNode()), cmd)
	require.NoError(t, err, "workload failed during phase: %s", phase)

	t.L().Printf("Completed workload phase: %s", phase)
}

// runDRPCMixedVersion tests DRPC upgrade compatibility by performing a
// standard mixed-version upgrade, then enabling DRPC via --use-new-rpc on
// the upgraded cluster. It verifies that:
//  1. The standard upgrade completes successfully (all nodes on gRPC).
//  2. A mixed-RPC state works (some nodes DRPC, some gRPC).
//  3. A full-DRPC state works (all nodes with --use-new-rpc).
//
// Use MVT_UPGRADE_PATH to control the upgrade path (e.g. "25.4,current"
// or "26.1,current").
func runDRPCMixedVersion(ctx context.Context, t test.Test, c cluster.Cluster) {
	crdbNodes := c.CRDBNodes()

	mvt := mixedversion.NewTest(ctx, t, t.L(), c, crdbNodes,
		mixedversion.NumUpgrades(1),
		mixedversion.EnabledDeploymentModes(mixedversion.SystemOnlyDeployment),
		mixedversion.AlwaysUseLatestPredecessors,
		mixedversion.WithSkipVersionProbability(1),
		mixedversion.WithWorkloadNodes(c.WorkloadNode()),
	)
	// Run a kv workload in the background throughout the entire upgrade.
	initCmd := roachtestutil.NewCommand("./cockroach workload init kv").
		Flag("splits", 100).
		Arg("{pgurl%s}", crdbNodes)
	runCmd := roachtestutil.NewCommand("./cockroach workload run kv").
		Flag("concurrency", 50).
		Flag("read-percent", 50).
		Option("tolerate-errors").
		Arg("{pgurl%s}", crdbNodes)
	stopWorkload := mvt.Workload("kv", c.WorkloadNode(), initCmd, runCmd)
	defer stopWorkload()

	mvt.InMixedVersion(
		"validate cluster health during upgrade",
		func(ctx context.Context, l *logger.Logger, rng *rand.Rand, h *mixedversion.Helper) error {
			l.Printf("checking cluster health in mixed version state")
			var count int
			if err := h.QueryRow(rng, "SELECT count(*) FROM kv.kv").Scan(&count); err != nil {
				return errors.Wrap(err, "cluster health check failed")
			}
			l.Printf("cluster health OK, kv row count: %d", count)
			return nil
		},
	)

	mvt.AfterUpgradeFinalized(
		"enable DRPC on upgraded nodes",
		func(ctx context.Context, l *logger.Logger, rng *rand.Rand, h *mixedversion.Helper) error {
			if !h.Context().ToVersion.IsCurrent() {
				l.Printf("skipping DRPC enablement for intermediate upgrade")
				return nil
			}

			// Split nodes into two groups for progressive DRPC enablement.
			// The background kv workload is already running and will
			// exercise the cluster throughout these restarts.
			half := len(crdbNodes) / 2
			firstGroup := crdbNodes[:half]
			secondGroup := crdbNodes[half:]

			// Restart first group with --use-new-rpc to create a
			// mixed-RPC state (some DRPC, some gRPC).
			l.Printf("restarting nodes %v with --use-new-rpc (mixed-RPC state)", firstGroup)
			for _, node := range firstGroup {
				if err := restartNodeWithDRPC(ctx, t, l, c, node); err != nil {
					return errors.Wrapf(err, "restarting node %d with DRPC", node)
				}
			}

			time.Sleep(600 * time.Second)

			// Restart remaining nodes with --use-new-rpc so the
			// entire cluster runs DRPC.
			l.Printf("restarting nodes %v with --use-new-rpc (full-DRPC state)", secondGroup)
			for _, node := range secondGroup {
				if err := restartNodeWithDRPC(ctx, t, l, c, node); err != nil {
					return errors.Wrapf(err, "restarting node %d with DRPC", node)
				}
			}

			l.Printf("DRPC upgrade compatibility test completed successfully")
			return nil
		},
	)

	mvt.Run()
}

// restartNodeWithDRPC gracefully stops a node and restarts it with the
// --use-new-rpc flag to enable DRPC. It notifies the test monitor
// before stopping and after starting to prevent false failure alerts.
func restartNodeWithDRPC(
	ctx context.Context, t test.Test, l *logger.Logger, c cluster.Cluster, node int,
) error {
	nodeOpt := c.Node(node)
	m := t.Monitor()

	m.ExpectProcessDead(nodeOpt)

	stopOpts := option.DefaultStopOpts()
	stopOpts.RoachprodOpts.Sig = 15 // SIGTERM for graceful shutdown
	stopOpts.RoachprodOpts.Wait = true
	if err := c.StopE(ctx, l, stopOpts, nodeOpt); err != nil {
		return errors.Wrapf(err, "stopping node %d", node)
	}

	startOpts := option.NewStartOpts(option.NoBackupSchedule)
	startOpts.RoachprodOpts.SkipInit = true
	startOpts.RoachprodOpts.ExtraArgs = append(
		startOpts.RoachprodOpts.ExtraArgs, "--use-new-rpc=true",
	)
	settings := install.MakeClusterSettings()
	if err := c.StartE(ctx, l, startOpts, settings, nodeOpt); err != nil {
		return errors.Wrapf(err, "starting node %d with DRPC", node)
	}

	m.ExpectProcessAlive(nodeOpt)

	l.Printf("waiting for node %d to be healthy after DRPC restart", node)
	return waitForNodeHealth(ctx, t, c, node)
}
