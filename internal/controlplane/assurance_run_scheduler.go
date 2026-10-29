package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"nexora.local/nli-v20/internal/middleware"
	metastore "nexora.local/nli-v20/internal/store"
)

var (
	errAssuranceRunNotFound = errors.New("Assurance run not found")
	errAssuranceRunTerminal = errors.New("Assurance run is already terminal")
)

// assuranceRunScheduler treats the metadata store as the durable queue. The
// in-memory wake channel is only a coalesced notification, so a full channel or
// controller restart cannot lose an accepted run. Claim/finalize/cancel state
// changes are serialized inside one active controller process.
type assuranceRunScheduler struct {
	server *Server
	wake   chan struct{}
	stop   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	wg     sync.WaitGroup
}

func newAssuranceRunScheduler(server *Server, workers int) (*assuranceRunScheduler, error) {
	if workers < 1 {
		workers = 1
	}
	if workers > 4 {
		workers = 4
	}
	q := &assuranceRunScheduler{server: server, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	pending, err := q.recoverInterrupted()
	if err != nil {
		return nil, fmt.Errorf("recover Assurance runs: %w", err)
	}
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	if pending {
		q.Wake()
	}
	return q, nil
}

func (q *assuranceRunScheduler) Wake() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *assuranceRunScheduler) Close() {
	q.once.Do(func() { close(q.stop) })
	q.wg.Wait()
}

func (q *assuranceRunScheduler) worker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.stop:
			return
		case <-q.wake:
		}
		for {
			record, ok := q.claimNext()
			if !ok {
				break
			}
			q.execute(record)
		}
	}
}

func (q *assuranceRunScheduler) recoverInterrupted() (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	running, err := q.server.metadata.List(ctx, "assurance-runs", metastore.Query{State: "running", Limit: 1000})
	if err != nil {
		return false, err
	}
	for _, doc := range running {
		var record assuranceRunRecord
		if json.Unmarshal(doc.Payload, &record) != nil || record.ID == "" {
			continue
		}
		record.State = "queued"
		record.StartedAt = nil
		record.FinishedAt = nil
		record.RecoveryCount++
		record.Error = "controller restarted before completion; run safely re-queued"
		if err := q.server.persistAssuranceRun(record, record.RequestedBy); err != nil {
			return false, err
		}
	}
	queued, err := q.server.metadata.List(ctx, "assurance-runs", metastore.Query{State: "queued", Limit: 1})
	if err != nil {
		return false, err
	}
	return len(running) > 0 || len(queued) > 0, nil
}

func (q *assuranceRunScheduler) claimNext() (assuranceRunRecord, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	docs, err := q.server.metadata.List(ctx, "assurance-runs", metastore.Query{State: "queued", Limit: 1000})
	if err != nil || len(docs) == 0 {
		return assuranceRunRecord{}, false
	}
	records := make([]assuranceRunRecord, 0, len(docs))
	for _, doc := range docs {
		var record assuranceRunRecord
		if json.Unmarshal(doc.Payload, &record) == nil && record.ID != "" && record.State == "queued" {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		return assuranceRunRecord{}, false
	}
	sort.Slice(records, func(i, j int) bool { return records[i].QueuedAt.Before(records[j].QueuedAt) })
	record := records[0]
	now := time.Now().UTC()
	record.State = "running"
	record.StartedAt = &now
	record.FinishedAt = nil
	record.Error = ""
	if err := q.server.persistAssuranceRun(record, record.RequestedBy); err != nil {
		return assuranceRunRecord{}, false
	}
	return record, true
}

func (q *assuranceRunScheduler) execute(record assuranceRunRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, secret, err := q.server.decryptAssuranceContext(ctx, record.CaseID)
	var evidence map[string]any
	if err == nil {
		request := assuranceRequest{Raw: secret.Raw, Desired: secret.Desired, Service: secret.Service, Source: secret.Source, FieldMapping: secret.FieldMapping}
		evidence = q.server.executeAssuranceRunScenario(record.Scenario, request)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	current, loadErr := q.load(record.ID)
	if loadErr != nil || current.State == "cancelled" {
		return
	}
	finished := time.Now().UTC()
	current.FinishedAt = &finished
	if err != nil {
		current.State = "failed"
		current.Error = "retained fixture could not be opened"
	} else if ctx.Err() != nil {
		current.State = "failed"
		current.Error = "Assurance run exceeded its bounded execution time"
	} else if evidence["passed"] == true {
		current.State = "passed"
		current.Evidence = evidence
		current.Error = ""
	} else {
		current.State = "failed"
		current.Evidence = evidence
		current.Error = "one or more scenario checks failed"
	}
	if saveErr := q.server.persistAssuranceRun(current, current.RequestedBy); saveErr != nil {
		return
	}
	outcome := "failure"
	if current.State == "passed" {
		outcome = "success"
	}
	q.server.auditAction(&middleware.SecurityContext{Username: current.RequestedBy, Role: current.RequestedRole}, "assurance.run", "assurance-runs/"+current.ID, outcome, map[string]string{"state": current.State, "plan_revision": fmt.Sprint(current.PlanRevision)})
}

func (q *assuranceRunScheduler) load(id string) (assuranceRunRecord, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	doc, err := q.server.metadata.Get(ctx, "assurance-runs", id, 0)
	if err != nil {
		return assuranceRunRecord{}, fmt.Errorf("%w: %v", errAssuranceRunNotFound, err)
	}
	var record assuranceRunRecord
	if json.Unmarshal(doc.Payload, &record) != nil || record.ID == "" {
		return assuranceRunRecord{}, errors.New("Assurance run metadata is invalid")
	}
	return record, nil
}

func (q *assuranceRunScheduler) Cancel(id, actor string) (assuranceRunRecord, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	record, err := q.load(id)
	if err != nil {
		return assuranceRunRecord{}, err
	}
	switch record.State {
	case "queued", "running":
		now := time.Now().UTC()
		record.State = "cancelled"
		record.FinishedAt = &now
		record.Error = "cancelled by authorized operator"
		if err := q.server.persistAssuranceRun(record, actor); err != nil {
			return assuranceRunRecord{}, err
		}
		return record, nil
	default:
		return assuranceRunRecord{}, fmt.Errorf("%w: state %s", errAssuranceRunTerminal, record.State)
	}
}
