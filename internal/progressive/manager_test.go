package progressive

import (
	"strings"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/protocol"
)

func testNodes(count int) []Node {
	nodes := make([]Node, 0, count)
	for index := 0; index < count; index++ {
		nodes = append(nodes, Node{ID: strings.Repeat("0", 3-len(string(rune('0'+index%10)))) + string(rune('a'+index)), Tags: map[string]string{"env": "prod"}})
	}
	return nodes
}

func state(char string) protocol.HashState {
	return protocol.HashState{PackHash: strings.Repeat(char, 64), ConfigHash: strings.Repeat(char, 64), ReleaseHash: strings.Repeat(char, 64)}
}

func TestCanaryWavesApprovalPauseResumeAndMetrics(t *testing.T) {
	nodes := []Node{{ID: "n01", Tags: map[string]string{"env": "prod"}}, {ID: "n02", Tags: map[string]string{"env": "prod"}}, {ID: "n03", Tags: map[string]string{"env": "prod"}}, {ID: "n04", Tags: map[string]string{"env": "prod"}}, {ID: "dev", Tags: map[string]string{"env": "dev"}}}
	manager := NewProgressiveManager(WithNodes(nodes))
	_, err := manager.Create("rollout-1", "parser rollout", CanaryStrategy(), Selector{Tags: map[string]string{"env": "prod"}}, state("a"), state("b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Start("rollout-1"); err != nil {
		t.Fatal(err)
	}
	rollout, _ := manager.Get("rollout-1")
	if rollout.Status != RolloutRunning || rollout.Waves[0].Wave.Name != "canary" || len(rollout.Waves[0].TargetNodes) != 1 {
		t.Fatalf("canary start: %+v", rollout)
	}
	canary := rollout.Waves[0].TargetNodes[0]
	if err := manager.RecordNodeResult("rollout-1", canary, NodeSucceeded, 10*time.Millisecond, "ok"); err != nil {
		t.Fatal(err)
	}
	rollout, _ = manager.Get("rollout-1")
	if rollout.Status != RolloutAwaitingApproval || rollout.CurrentWave != 1 {
		t.Fatalf("approval gate not reached: %+v", rollout)
	}
	if err := manager.Pause("rollout-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Resume("rollout-1"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Approve("rollout-1", "ops@example.test"); err != nil {
		t.Fatal(err)
	}
	rollout, _ = manager.Get("rollout-1")
	if rollout.Status != RolloutRunning || rollout.Waves[1].ApprovedBy == "" {
		t.Fatalf("approval did not resume wave: %+v", rollout)
	}
	for rollout.Status == RolloutRunning {
		wave := rollout.Waves[rollout.CurrentWave]
		for _, nodeID := range wave.TargetNodes {
			if err := manager.RecordNodeResult("rollout-1", nodeID, NodeSucceeded, 20*time.Millisecond, "ok"); err != nil {
				t.Fatal(err)
			}
		}
		rollout, _ = manager.Get("rollout-1")
	}
	if rollout.Status != RolloutCompleted || !rollout.Active.Equal(state("a")) || rollout.Metrics.SucceededNodes != 4 || rollout.Metrics.LatencyP95 != 20*time.Millisecond {
		t.Fatalf("final rollout: %+v", rollout)
	}
}

func TestFailureAutoRollsBackFromReportedResults(t *testing.T) {
	strategy := Strategy{Name: "strict", MaximumFailureRate: 0, AutoRollback: true, Waves: []Wave{{Name: "canary", Percent: 100}}}
	manager := NewProgressiveManager(WithNodes([]Node{{ID: "n1"}}))
	_, _ = manager.Create("fail", "failure", strategy, Selector{}, state("a"), state("b"))
	_ = manager.Start("fail")
	if err := manager.RecordNodeResult("fail", "n1", NodeFailed, 30*time.Millisecond, "validation failed"); err != nil {
		t.Fatal(err)
	}
	rollout, _ := manager.Get("fail")
	if rollout.Status != RolloutRolledBack || !rollout.Active.Equal(state("b")) || rollout.Metrics.FailedNodes != 1 {
		t.Fatalf("rollback state: %+v", rollout)
	}
}

func TestBlueGreenRequiresTrafficSwitchApproval(t *testing.T) {
	manager := NewProgressiveManager(WithNodes([]Node{{ID: "green-1"}, {ID: "green-2"}}))
	_, _ = manager.Create("bg", "blue-green", BlueGreenStrategy(), Selector{}, state("c"), state("d"))
	_ = manager.Start("bg")
	for _, nodeID := range []string{"green-1", "green-2"} {
		if err := manager.RecordNodeResult("bg", nodeID, NodeSucceeded, time.Millisecond, "healthy"); err != nil {
			t.Fatal(err)
		}
	}
	rollout, _ := manager.Get("bg")
	if rollout.Status != RolloutAwaitingApproval || rollout.Waves[rollout.CurrentWave].Wave.Name != "traffic_switch" {
		t.Fatalf("traffic switch gate missing: %+v", rollout)
	}
	if err := manager.Approve("bg", "release-manager"); err != nil {
		t.Fatal(err)
	}
	rollout, _ = manager.Get("bg")
	if rollout.Status != RolloutCompleted || !rollout.Active.Equal(state("c")) {
		t.Fatalf("blue-green completion: %+v", rollout)
	}
}

func TestProgressiveStatePersistsAndExplicitRollback(t *testing.T) {
	path := t.TempDir() + "/rollouts.json"
	manager := NewProgressiveManager(WithStateFile(path), WithNodes([]Node{{ID: "n1"}}))
	_, _ = manager.Create("persist", "persistent", CanaryStrategy(), Selector{}, state("e"), state("f"))
	if err := manager.Start("persist"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewProgressiveManager(WithStateFile(path), WithNodes([]Node{{ID: "n1"}}))
	rollout, found := reloaded.Get("persist")
	if !found || rollout.Status != RolloutRunning {
		t.Fatalf("persisted rollout missing: %+v", rollout)
	}
	if err := reloaded.Rollback("persist", "operator rollback"); err != nil {
		t.Fatal(err)
	}
	rollout, _ = reloaded.Get("persist")
	if rollout.Status != RolloutRolledBack || !rollout.Active.Equal(state("f")) {
		t.Fatalf("explicit rollback: %+v", rollout)
	}
}

func TestProgressiveCancellation(t *testing.T) {
	manager := NewProgressiveManager(WithNodes([]Node{{ID: "n1"}}))
	_, _ = manager.Create("cancel", "cancel", CanaryStrategy(), Selector{}, state("1"), state("2"))
	if err := manager.Start("cancel"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Cancel("cancel"); err != nil {
		t.Fatal(err)
	}
	rollout, _ := manager.Get("cancel")
	if rollout.Status != RolloutCancelled || rollout.CompletedAt == nil {
		t.Fatalf("cancel state: %+v", rollout)
	}
	if err := manager.Resume("cancel"); err == nil {
		t.Fatal("cancelled rollout resumed")
	}
}
