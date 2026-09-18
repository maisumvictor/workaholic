package argo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetApplicationStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/applications/api" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"api","namespace":"argocd"},"spec":{"source":{"repoURL":"https://github.com/acme/api"}},"status":{"sync":{"status":"OutOfSync","revision":"abc"},"health":{"status":"Degraded"}}}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.Client(), srv.URL, "test-token")
	view, err := c.GetApplication(context.Background(), "argocd", "api")
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != "api" || view.SyncStatus != "OutOfSync" || view.HealthStatus != "Degraded" || view.Revision != "abc" {
		t.Fatalf("app %+v", view)
	}
	if view.RepoURL != "https://github.com/acme/api" {
		t.Fatalf("repo %s", view.RepoURL)
	}
}

func TestClientGetApplicationRequiresName(t *testing.T) {
	c := New(http.DefaultClient, "http://example.invalid", "")
	if _, err := c.GetApplication(context.Background(), "argocd", ""); err == nil {
		t.Fatal("expected error")
	}
}
