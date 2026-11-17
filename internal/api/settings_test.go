package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Sending GET's {"settings": ...} wrapper back to PUT must be refused before
// anything is saved (the server here has no store: saving would panic).
func TestPutSettingsRefusesWrapper(t *testing.T) {
	s := &Server{}
	body := `{"settings":{"salary_floor_lpa":10,"resume_text":"x"},"defaults":{},"llm_configured":false}`
	w := httptest.NewRecorder()
	s.putSettings(w, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "settings object itself") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
