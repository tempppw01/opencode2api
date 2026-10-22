package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"opencode2api/internal/identity"
	wire "opencode2api/internal/protocol"
)

func TestProxyProbeUsesOpenCodeSessionIdentity(t *testing.T) {
	sessionPattern := regexp.MustCompile(`^ses_[0-9a-f]{9}f[c-f][0-9a-f][0-9A-Za-z]{14}$`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := r.Header.Get("x-opencode-session")
		if !sessionPattern.MatchString(session) {
			t.Errorf("x-opencode-session = %q, want canonical OpenCode session ID", session)
		}
		if got := r.Header.Get("x-opencode-session-id"); got != session {
			t.Errorf("x-opencode-session-id = %q, want session identity %q", got, session)
		}
		if r.Header.Get("x-session-affinity") == "" || r.Header.Get("User-Agent") == "" || r.Header.Get("x-opencode-client") != "cli" {
			t.Errorf("missing OpenCode client identity headers: %+v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "ok"}}}})
	}))
	defer server.Close()

	ids := identity.RequestIDs{
		Session: identity.CanonicalSessionID("proxy-health:0:beam-free"),
		Project: "opencode2api:default-project",
	}
	payload, err := probePayload("beam-free", wire.Chat, "", false)
	if err != nil {
		t.Fatal(err)
	}
	req, err := upstreamRequest(context.Background(), server.URL, wire.Chat, payload, ids, "public")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
