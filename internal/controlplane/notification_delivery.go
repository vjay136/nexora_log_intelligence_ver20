package controlplane

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"nexora.local/nli-v20/internal/auditlog"
	metastore "nexora.local/nli-v20/internal/store"
)

const notificationAttemptLimit = 3

type notificationDeliveryAttempt struct {
	ID        string    `json:"id"`
	Sequence  uint64    `json:"sequence"`
	Channel   string    `json:"channel"`
	Attempt   int       `json:"attempt"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
}

type notificationDeliveryJob struct {
	notificationDeliveryAttempt
	Event auditlog.Event `json:"event"`
}

var notificationHTTPClient = &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// dispatchAuditNotification persists a bounded-queue job after an audit append.
// Provider I/O happens on the single worker, never on the business request path.
func (s *Server) dispatchAuditNotification(event auditlog.Event) {
	_, _, actionable := classifyNotificationEvent(event)
	if !actionable || event.Action == "notification.policy.update" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	id := fmt.Sprintf("%020d-dispatch-00", event.Sequence)
	job := notificationDeliveryJob{notificationDeliveryAttempt: notificationDeliveryAttempt{ID: id, Sequence: event.Sequence, Channel: "dispatch", Status: "pending", Timestamp: time.Now().UTC()}, Event: event}
	if err := s.saveNotificationJob(ctx, job, "pending", ""); err != nil {
		return
	}
	select {
	case s.notificationQueue <- job:
	default:
		_ = s.saveNotificationJob(ctx, job, "failed", "delivery queue is full")
	}
}

func (s *Server) startNotificationWorker() {
	if s.notificationQueue == nil {
		s.notificationQueue = make(chan notificationDeliveryJob, 128)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	s.notificationWorkerCancel = cancel
	if s.notificationWorkerDone == nil {
		s.notificationWorkerDone = make(chan struct{})
	}
	go func() {
		defer close(s.notificationWorkerDone)
		for {
			select {
			case <-workerCtx.Done():
				return
			case job := <-s.notificationQueue:
				s.processNotificationJob(workerCtx, job)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	docs, err := s.metadata.List(ctx, notificationDeliveryCollection, metastore.Query{Limit: 500})
	if err != nil {
		return
	}
	for _, doc := range docs {
		if doc.State != "pending" {
			continue
		}
		var job notificationDeliveryJob
		if json.Unmarshal(doc.Payload, &job) != nil || job.Channel != "dispatch" || job.Event.Sequence == 0 {
			continue
		}
		select {
		case s.notificationQueue <- job:
		default:
			_ = s.saveNotificationJob(ctx, job, "failed", "delivery queue is full during recovery")
		}
	}
}

func (s *Server) processNotificationJob(parent context.Context, job notificationDeliveryJob) {
	ctx, cancel := context.WithTimeout(parent, 18*time.Second)
	defer cancel()
	if err := s.saveNotificationJob(ctx, job, "dispatching", ""); err != nil {
		return
	}
	category, severity, actionable := classifyNotificationEvent(job.Event)
	if !actionable {
		_ = s.saveNotificationJob(ctx, job, "skipped", "audit event is no longer actionable")
		return
	}
	policy := s.readNotificationPolicyFromContext(ctx)
	if !policyWatches(policy, category) || (!policy.Channels["email"].Enabled && !policy.Channels["pagerduty"].Enabled) {
		_ = s.saveNotificationJob(ctx, job, "skipped", "event category is no longer watched")
		return
	}
	for _, name := range []string{"email", "pagerduty"} {
		channel := policy.Channels[name]
		if !channel.Enabled {
			continue
		}
		if name == "email" && !channelConfiguredEmail(channel) {
			if err := s.saveNotificationAttempt(ctx, job.Sequence, name, 0, "not_configured", ""); err != nil {
				_ = s.saveNotificationJob(ctx, job, "failed", "could not persist delivery attempt")
				return
			}
			continue
		}
		if name == "pagerduty" && !envConfigured(channel.SecretEnv) {
			if err := s.saveNotificationAttempt(ctx, job.Sequence, name, 0, "not_configured", ""); err != nil {
				_ = s.saveNotificationJob(ctx, job, "failed", "could not persist delivery attempt")
				return
			}
			continue
		}
		var lastErr error
		for attempt := 1; attempt <= notificationAttemptLimit; attempt++ {
			attemptCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			if name == "email" {
				lastErr = sendNotificationEmail(attemptCtx, channel, job.Event, category, severity)
			} else {
				lastErr = s.sendPagerDuty(attemptCtx, channel, job.Event, category, severity)
			}
			stop()
			status, message := "delivered", ""
			if lastErr != nil {
				status, message = "failed", safeDeliveryError(lastErr)
			}
			if err := s.saveNotificationAttempt(ctx, job.Sequence, name, attempt, status, message); err != nil {
				_ = s.saveNotificationJob(ctx, job, "failed", "could not persist delivery attempt")
				return
			}
			if lastErr == nil {
				break
			}
			if attempt < notificationAttemptLimit {
				select {
				case <-ctx.Done():
					_ = s.saveNotificationJob(ctx, job, "failed", "delivery timeout")
					return
				case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
				}
			}
		}
	}
	_ = s.saveNotificationJob(ctx, job, "complete", "")
}

func (s *Server) readNotificationPolicyFromContext(ctx context.Context) notificationPolicy {
	doc, err := s.metadata.Get(ctx, notificationConfigCollection, "default", 0)
	if err != nil {
		return defaultNotificationPolicy()
	}
	var policy notificationPolicy
	if json.Unmarshal(doc.Payload, &policy) != nil || policy.Channels == nil {
		return defaultNotificationPolicy()
	}
	return policy
}

func channelConfiguredEmail(channel notificationChannelConfig) bool {
	return envConfigured(channel.ServerEnv) && envConfigured(channel.SenderEnv) && envConfigured(channel.RecipientEnv) && ((channel.UsernameEnv == "" && channel.PasswordEnv == "") || (envConfigured(channel.UsernameEnv) && envConfigured(channel.PasswordEnv)))
}

func sendNotificationEmail(ctx context.Context, channel notificationChannelConfig, event auditlog.Event, category, severity string) error {
	server := strings.TrimSpace(os.Getenv(channel.ServerEnv))
	host, port, err := net.SplitHostPort(server)
	if err != nil || host == "" || port != "465" {
		return errors.New("SMTP server must be a host on implicit-TLS port 465")
	}
	from := strings.TrimSpace(os.Getenv(channel.SenderEnv))
	to := strings.FieldsFunc(os.Getenv(channel.RecipientEnv), func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	if !validMailAddress(from) || len(to) == 0 || len(to) > 100 {
		return errors.New("SMTP sender or recipients are invalid")
	}
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
		if !validMailAddress(to[i]) {
			return errors.New("SMTP recipient address is invalid")
		}
	}
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", server, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return errors.New("SMTP TLS connection failed")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return errors.New("SMTP greeting failed")
	}
	defer client.Close()
	if channel.UsernameEnv != "" {
		auth := smtp.PlainAuth("", os.Getenv(channel.UsernameEnv), os.Getenv(channel.PasswordEnv), host)
		if err := client.Auth(auth); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if err := client.Mail(from); err != nil {
		return errors.New("SMTP sender rejected")
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return errors.New("SMTP recipient rejected")
		}
	}
	w, err := client.Data()
	if err != nil {
		return errors.New("SMTP DATA rejected")
	}
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: NLI %s notification: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nNLI observed an audited %s event.\r\nAction: %s\r\nResource: %s\r\nOutcome: %s\r\nAudit sequence: %d\r\n", from, strings.Join(to, ", "), severity, category, category, safeHeader(event.Action), safeHeader(event.Resource), safeHeader(event.Outcome), event.Sequence)
	if _, err = w.Write([]byte(body)); err != nil {
		_ = w.Close()
		return errors.New("SMTP message write failed")
	}
	if err = w.Close(); err != nil {
		return errors.New("SMTP server did not accept message")
	}
	return nil
}

func validMailAddress(value string) bool {
	if value == "" || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	return err == nil && parsed.Address == value
}
func safeHeader(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r", " "), "\n", " ")
}

func (s *Server) sendPagerDuty(ctx context.Context, channel notificationChannelConfig, event auditlog.Event, category, severity string) error {
	endpoint := channel.Endpoint
	if endpoint == "" {
		endpoint = "https://events.pagerduty.com/v2/enqueue"
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "events.pagerduty.com" || u.Path != "/v2/enqueue" {
		return errors.New("PagerDuty endpoint is invalid")
	}
	key := os.Getenv(channel.SecretEnv)
	if strings.TrimSpace(key) == "" {
		return errors.New("PagerDuty routing key is not configured")
	}
	pdSeverity := "warning"
	if severity == "critical" {
		pdSeverity = "critical"
	}
	payload := map[string]any{"routing_key": key, "event_action": "trigger", "dedup_key": fmt.Sprintf("nli-audit-%d", event.Sequence), "payload": map[string]any{"summary": notificationTitle(event, category), "source": "Nexora Log Intelligence", "severity": pdSeverity, "custom_details": map[string]any{"audit_sequence": strconv.FormatUint(event.Sequence, 10), "action": event.Action, "resource": event.Resource, "outcome": event.Outcome}}}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return errors.New("PagerDuty request could not be created")
	}
	req.Header.Set("Content-Type", "application/json")
	client := notificationHTTPClient
	if s.notificationHTTPClient != nil {
		client = s.notificationHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("PagerDuty request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("PagerDuty rejected event with HTTP %d", resp.StatusCode)
	}
	var ack struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&ack) != nil || ack.Status != "success" {
		return errors.New("PagerDuty response did not confirm acceptance")
	}
	return nil
}

func (s *Server) saveNotificationAttempt(ctx context.Context, sequence uint64, channel string, attempt int, status, message string) error {
	id := fmt.Sprintf("%020d-%s-%02d", sequence, channel, attempt)
	record := notificationDeliveryAttempt{ID: id, Sequence: sequence, Channel: channel, Attempt: attempt, Status: status, Timestamp: time.Now().UTC(), Error: message}
	data, _ := json.Marshal(record)
	_, err := s.metadata.Put(ctx, metastore.Document{Collection: notificationDeliveryCollection, ID: id, State: status, Payload: data, CreatedBy: "notification-dispatcher"})
	return err
}

func (s *Server) saveNotificationJob(ctx context.Context, job notificationDeliveryJob, status, message string) error {
	job.Status, job.Error, job.Timestamp = status, message, time.Now().UTC()
	data, _ := json.Marshal(job)
	_, err := s.metadata.Put(ctx, metastore.Document{Collection: notificationDeliveryCollection, ID: job.ID, State: status, Payload: data, CreatedBy: "notification-dispatcher"})
	return err
}

func safeDeliveryError(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "delivery failed"
	}
	if len(message) > 240 {
		message = message[:240]
	}
	return message
}
