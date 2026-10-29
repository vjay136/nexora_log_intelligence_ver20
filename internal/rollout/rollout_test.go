package rollout_test

import (
	"os"
	"testing"

	"nexora.local/nli-v20/internal/rollout"
)

func tmpRollout(t *testing.T) *rollout.Manager {
	t.Helper()
	f, _ := os.CreateTemp("", "nli-rollout-*.json")
	f.Close()
	os.Remove(f.Name())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return rollout.NewManager(f.Name())
}

func TestCreateRollout(t *testing.T) {
	m := tmpRollout(t)
	r, err := m.Create(rollout.TypeConfigPush, "Push new config", "admin",
		[]string{"node-001", "node-002"}, nil,
		rollout.ConfigPayload{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != rollout.StatusPending {
		t.Errorf("expected pending, got %s", r.Status)
	}
}

func TestStartAndComplete(t *testing.T) {
	m := tmpRollout(t)
	r, _ := m.Create(rollout.TypeRestart, "Restart all", "ops", nil, nil, nil)
	nodes := []string{"n1", "n2"}
	m.Start(r.ID, nodes)

	m.RecordNodeResult(r.ID, "n1", rollout.StatusCompleted, "")
	m.RecordNodeResult(r.ID, "n2", rollout.StatusCompleted, "")

	got, _ := m.Get(r.ID)
	if got.Status != rollout.StatusCompleted {
		t.Errorf("expected completed, got %s", got.Status)
	}
	if got.DoneNodes != 2 {
		t.Errorf("expected 2 done nodes, got %d", got.DoneNodes)
	}
}

func TestFailedRollout(t *testing.T) {
	m := tmpRollout(t)
	r, _ := m.Create(rollout.TypeParserSync, "Sync parsers", "ops", nil, nil, nil)
	m.Start(r.ID, []string{"n1"})
	m.RecordNodeResult(r.ID, "n1", rollout.StatusFailed, "RPM install failed")

	got, _ := m.Get(r.ID)
	if got.Status != rollout.StatusFailed {
		t.Errorf("expected failed, got %s", got.Status)
	}
}

func TestCancelRollout(t *testing.T) {
	m := tmpRollout(t)
	r, _ := m.Create(rollout.TypeConfigPush, "Test cancel", "admin", nil, nil, nil)
	if err := m.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(r.ID)
	if got.Status != rollout.StatusCancelled {
		t.Errorf("expected cancelled, got %s", got.Status)
	}
}
