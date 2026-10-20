package proxmox_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"air-traffic-control/backend/plugins/vm/proxmox"
)

func TestClient_PasswordAuthFlow(t *testing.T) {
	var gotCookie string
	var gotCSRF string
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("bad form: %v", err)
		}
		if r.Form.Get("username") != "root@pam" || r.Form.Get("password") != "hunter2" {
			t.Errorf("unexpected credentials: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"ticket": "PVE-TICKET", "CSRFPreventionToken": "CSRF-XYZ",
		}})
	})
	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotCSRF = r.Header.Get("CSRFPreventionToken")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.2.4"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := proxmox.NewClient(proxmox.Config{Host: srv.URL, User: "root@pam", Password: "hunter2"})
	ver, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if ver["version"] != "8.2.4" {
		t.Errorf("unexpected version payload: %v", ver)
	}
	if gotCookie != "PVEAuthCookie=PVE-TICKET" {
		t.Errorf("expected ticket cookie, got %q", gotCookie)
	}
	if gotCSRF != "" {
		t.Errorf("GET must not send CSRF token, got %q", gotCSRF)
	}
}

func TestClient_NoCredentialsFails(t *testing.T) {
	c := proxmox.NewClient(proxmox.Config{Host: "127.0.0.1"})
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatal("expected auth error when no token or password is configured")
	}
}

func TestClient_APIErrorSurfaced(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":{"authorization":"permission check failed"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := proxmox.NewClient(proxmox.Config{Host: srv.URL, User: "root@pam", TokenID: "atc", TokenSecret: "s3cr3t"})
	_, err := c.Version(context.Background())
	if err == nil {
		t.Fatal("expected error from API")
	}
	msg := err.Error()
	if !strings.Contains(msg, "403") || !strings.Contains(msg, "permission check failed") {
		t.Errorf("error must surface status and body, got %q", msg)
	}
}

func TestClient_TokenHeaderComposition(t *testing.T) {
	var auth string
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.2"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Full user!tokenid form.
	c := proxmox.NewClient(proxmox.Config{Host: srv.URL, User: "svc@pve", TokenID: "ci", TokenSecret: "sekret"})
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	if auth != "PVEAPIToken=svc@pve!ci=sekret" {
		t.Errorf("unexpected auth header %q", auth)
	}
}
