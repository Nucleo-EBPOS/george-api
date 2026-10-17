package openapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAPIDocumentIsValidAndComplete(t *testing.T) {
	raw := JSON("test", "https://georgedesk.test")
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Fatalf("versão inesperada: %v", doc["openapi"])
	}
	paths := doc["paths"].(map[string]interface{})
	must := []string{"/auth/signup", "/auth/login", "/auth/refresh", "/auth/logout", "/auth/forgot", "/auth/reset",
		"/auth/2fa/setup", "/auth/2fa/verify", "/auth/2fa/disable", "/me", "/devices", "/devices/{id}", "/devices/{id}/share",
		"/devices/{id}/share/{shareId}", "/devices/heartbeat", "/devices/search", "/invites", "/invites/{code}", "/invites/{code}/consume",
		"/groups", "/groups/{id}", "/groups/{id}/devices", "/sessions", "/sessions/{id}/summary", "/org", "/org/users", "/org/users/{id}",
		"/org/audit", "/org/branding", "/org/sso", "/org/billing", "/org/billing/portal", "/webhooks/stripe", "/ops/orgs", "/ops/health",
		"/healthz", "/releases/latest"}
	for _, p := range must {
		if _, ok := paths[p]; !ok {
			t.Errorf("rota ausente no OpenAPI: %s", p)
		}
	}
	// nenhum par de templates equivalentes (ex.: /x/{id} e /x/{code}) — proibido pela spec OpenAPI
	seen := map[string]string{}
	for p := range paths {
		norm := p
		for strings.Contains(norm, "{") {
			i, j := strings.Index(norm, "{"), strings.Index(norm, "}")
			norm = norm[:i] + "{}" + norm[j+1:]
		}
		if prev, dup := seen[norm]; dup {
			t.Errorf("templates equivalentes: %s e %s", prev, p)
		}
		seen[norm] = p
	}
	// toda operação tem responses e operationId único
	ids := map[string]bool{}
	for p, item := range paths {
		for m, op := range item.(map[string]interface{}) {
			o := op.(map[string]interface{})
			if _, ok := o["responses"]; !ok {
				t.Errorf("%s %s sem responses", m, p)
			}
			id, _ := o["operationId"].(string)
			if id == "" || ids[id] {
				t.Errorf("%s %s operationId vazio/duplicado: %q", m, p, id)
			}
			ids[id] = true
		}
	}
}
