package wsrelay_test

import (
	"net/http/httptest"
	"testing"

	"nexora.local/nli-v20/internal/wsrelay"
)

func TestHub_PublishNoClients(t *testing.T) {
	hub := wsrelay.NewHub()
	// Publishing with no clients should not panic or block
	hub.Publish([]byte(`{"test":true}`))
}

func TestHub_ServeHTTP_NonWebSocket(t *testing.T) {
	hub := wsrelay.NewHub()
	req := httptest.NewRequest("GET", "/api/v1/stream", nil)
	w := httptest.NewRecorder()
	hub.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400 for non-websocket request, got %d", w.Code)
	}
}
