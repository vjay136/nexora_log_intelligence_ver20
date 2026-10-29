package routing

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"nexora.local/nli-v20/internal/output"
	"nexora.local/nli-v20/telemetry"
)

type V5Router struct {
	customerRouter  *output.Router
	telemetryRouter *output.Router
}

func NewV5Router(customerConfigs, telemetryConfigs []output.Config) (*V5Router, error) {
	customer, err := output.NewRouter(customerConfigs)
	if err != nil {
		return nil, err
	}
	metadata, err := output.NewRouter(telemetryConfigs)
	if err != nil {
		customer.Close()
		return nil, err
	}
	return &V5Router{customerRouter: customer, telemetryRouter: metadata}, nil
}

type V5ParseResult struct {
	EventPayload   *telemetry.EventPayload
	ParseTelemetry *telemetry.ParseTelemetry
	Success        bool
	Quarantined    bool
}

// RouteV5 never emits a customer record for quarantine. Telemetry carries the
// logical outcome and a bounded acknowledgement for every attempted route.
func (router *V5Router) RouteV5(result *V5ParseResult) error {
	return router.RouteV5ForFamily(result, "")
}

// RouteV5ForFamily preserves per-input output isolation for enterprise
// deployments while telemetry continues to use its independent channel.
func (router *V5Router) RouteV5ForFamily(result *V5ParseResult, family string) error {
	if result == nil || result.ParseTelemetry == nil {
		return fmt.Errorf("v5 routing requires ParseTelemetry")
	}
	return errors.Join(router.RouteV5CustomerForFamily(result, family), router.RouteV5Telemetry(result))
}

// RouteV5Customer sends only a parsed customer payload. Quarantine records are
// retained by the agent's local raw queue and never become customer events.
func (router *V5Router) RouteV5Customer(result *V5ParseResult) error {
	return router.RouteV5CustomerForFamily(result, "")
}

func (router *V5Router) RouteV5CustomerForFamily(result *V5ParseResult, family string) error {
	if result == nil || result.ParseTelemetry == nil {
		return fmt.Errorf("v5 routing requires ParseTelemetry")
	}
	metadata := result.ParseTelemetry
	var customerErr error
	if result.Quarantined {
		metadata.Route = "quarantine"
		metadata.Result = "quarantined"
		metadata.DeliveryAcks = append(metadata.DeliveryAcks, telemetry.DeliveryAck{
			Destination: "customer", Status: "skipped_quarantine", Timestamp: time.Now().UTC(),
		})
	} else if result.Success && result.EventPayload != nil && result.EventPayload.FieldCount() > 0 {
		payload, err := json.Marshal(result.EventPayload)
		if err != nil {
			return err
		}
		customerErr = router.customerRouter.WriteForFamily(payload, "normal", family)
		// WriteForFamily admits the record to bounded destination queues. The
		// subsequent Flush is the durable delivery barrier, so do not represent
		// queue admission as a destination acknowledgement.
		ack := telemetry.DeliveryAck{Destination: "customer", Status: "queued", Timestamp: time.Now().UTC()}
		if customerErr != nil {
			ack.Status, ack.ErrorCode = "failed", "customer_write_failed"
		}
		metadata.DeliveryAcks = append(metadata.DeliveryAcks, ack)
	} else {
		return fmt.Errorf("v5 routing requires a successful payload or quarantine outcome")
	}

	return customerErr
}

// RouteLegacy converts only normal/warning legacy results to customer payloads.
// Unknown/quarantine events are represented solely by bounded metadata.
func (router *V5Router) RouteLegacy(eventData map[string]interface{}, telemetryMetadata map[string]interface{}, route string) error {
	return router.RouteLegacyForFamily(eventData, telemetryMetadata, route, "")
}

func (router *V5Router) RouteLegacyForFamily(eventData map[string]interface{}, telemetryMetadata map[string]interface{}, route, family string) error {
	var customerErr error
	if (route == "normal" || route == "warning") && len(eventData) > 0 {
		payload, err := json.Marshal(telemetry.EventPayload{Fields: eventData})
		if err != nil {
			return err
		}
		customerErr = router.customerRouter.WriteForFamily(payload, route, family)
	}
	var telemetryErr error
	if len(telemetryMetadata) > 0 {
		metadata, err := json.Marshal(telemetryMetadata)
		if err != nil {
			return errors.Join(customerErr, err)
		}
		telemetryErr = router.telemetryRouter.Write(metadata, "telemetry")
	}
	return errors.Join(customerErr, telemetryErr)
}

// RouteV5Telemetry sends bounded parse metadata independently of customer
// delivery so operators can distinguish a resolved event from telemetry delay.
func (router *V5Router) RouteV5Telemetry(result *V5ParseResult) error {
	if result == nil || result.ParseTelemetry == nil {
		return fmt.Errorf("v5 routing requires ParseTelemetry")
	}
	metadata, err := json.Marshal(result.ParseTelemetry)
	if err != nil {
		return err
	}
	return router.telemetryRouter.Write(metadata, "telemetry")
}

func (router *V5Router) FlushCustomer() error  { return router.customerRouter.Flush() }
func (router *V5Router) FlushTelemetry() error { return router.telemetryRouter.Flush() }

// QueueStats aggregates the independent customer and metadata destination
// queues for bounded operational telemetry.
func (router *V5Router) QueueStats() (depth, capacity int) {
	customerDepth, customerCapacity := router.customerRouter.QueueStats()
	metadataDepth, metadataCapacity := router.telemetryRouter.QueueStats()
	return customerDepth + metadataDepth, customerCapacity + metadataCapacity
}

func (router *V5Router) Close() error {
	var customerErr, telemetryErr error
	if router.customerRouter != nil {
		customerErr = router.customerRouter.Close()
	}
	if router.telemetryRouter != nil {
		telemetryErr = router.telemetryRouter.Close()
	}
	return errors.Join(customerErr, telemetryErr)
}

func (router *V5Router) Flush() error {
	return errors.Join(router.customerRouter.Flush(), router.telemetryRouter.Flush())
}
