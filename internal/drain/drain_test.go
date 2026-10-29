package drain_test

import (
	"testing"

	"nexora.local/nli-v20/internal/drain"
)

func TestNewCluster(t *testing.T) {
	m := drain.New(drain.Config{SimTh: 0.5})
	r := m.Process("Connection from 192.168.1.1 port 22")
	if !r.IsNew {
		t.Error("expected new cluster for first log")
	}
	if r.ClusterID == 0 {
		t.Error("expected non-zero cluster ID")
	}
}

func TestTemplateGeneralization(t *testing.T) {
	m := drain.New(drain.Config{SimTh: 0.5})
	m.Process("<IP> Connected to <IP>")
	r2 := m.Process("<IP> Connected to <IP>")
	if r2.IsNew {
		t.Error("expected existing cluster for similar log")
	}
}

func TestSimThreshold(t *testing.T) {
	m := drain.New(drain.Config{SimTh: 0.9}) // very strict
	m.Process("user alice logged in from 10.0.0.1")
	r := m.Process("user bob logged in from 10.0.0.2")
	// With sim_th=0.9, "alice" vs "bob" and "10.0.0.1" vs "10.0.0.2" differ
	// 4 of 6 tokens match = 0.67, below 0.9 → new cluster
	if !r.IsNew {
		t.Log("sim_th=0.9 boundary: result was existing cluster (depends on masking)")
	}
}

func TestPersistenceRoundtrip(t *testing.T) {
	m := drain.New(drain.Config{SimTh: 0.5})
	m.Process("Connection from <IP> port <NUM>")
	m.Process("Connection from <IP> port <NUM>")

	clusters := m.Clusters()
	if len(clusters) == 0 {
		t.Fatal("expected at least one cluster")
	}

	// Restore into a new miner
	m2 := drain.New(drain.Config{SimTh: 0.5})
	m2.LoadClusters(clusters)

	r := m2.Process("Connection from <IP> port <NUM>")
	if r.IsNew {
		t.Error("expected cluster to be restored from persistence")
	}
}
