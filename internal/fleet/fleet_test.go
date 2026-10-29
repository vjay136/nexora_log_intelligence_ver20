package fleet_test

import (
	"os"
	"testing"
	"time"

	"nexora.local/nli-v20/internal/fleet"
)

func tmpFleet(t *testing.T) *fleet.Fleet {
	t.Helper()
	f, _ := os.CreateTemp("", "nli-fleet-*.json")
	f.Close()
	os.Remove(f.Name())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return fleet.New(f.Name())
}

func TestRegisterNode(t *testing.T) {
	f := tmpFleet(t)
	f.Register(&fleet.Node{
		ID:       "node-001",
		Hostname: "server01",
		IP:       "10.0.0.1",
		Version:  "0.4.0",
	})
	all := f.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 node, got %d", len(all))
	}
	if all[0].Status != fleet.StatusOnline {
		t.Errorf("expected online, got %s", all[0].Status)
	}
}

func TestHeartbeat(t *testing.T) {
	f := tmpFleet(t)
	f.Register(&fleet.Node{ID: "n1", Hostname: "h1"})
	err := f.Heartbeat("n1", &fleet.NodeMetrics{Received: 1000, Parsed: 950})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := f.Get("n1")
	if n.Metrics == nil {
		t.Fatal("expected metrics")
	}
	if n.Metrics.ParseRate < 90 {
		t.Errorf("parse rate = %.1f, want >= 90", n.Metrics.ParseRate)
	}
}

func TestMarkStale(t *testing.T) {
	f := tmpFleet(t)
	f.Register(&fleet.Node{ID: "stale", Hostname: "old"})
	// Force last_seen to be old
	time.Sleep(2 * time.Millisecond)
	f.MarkStale(1 * time.Millisecond)
	n, _ := f.Get("stale")
	if n.Status != fleet.StatusOffline {
		t.Errorf("expected offline, got %s", n.Status)
	}
}

func TestFleetStats(t *testing.T) {
	f := tmpFleet(t)
	f.Register(&fleet.Node{ID: "n1", Hostname: "h1"})
	f.Register(&fleet.Node{ID: "n2", Hostname: "h2"})
	stats := f.Stats()
	if stats["total"].(int) != 2 {
		t.Errorf("expected 2 nodes, got %v", stats["total"])
	}
}
