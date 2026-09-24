package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCompareUsesReposAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/repos/acme/api/compare/old...new" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ahead","ahead_by":2,"behind_by":0,"html_url":"https://github.com/acme/api/compare/old...new","commits":[{"commit":{"message":"fix hpa"}},{"commit":{"message":"bump image"}}]}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.Client(), srv.URL, "test-token")
	view, err := c.Compare(context.Background(), "acme", "api", "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "ahead" || view.AheadBy != 2 || len(view.Commits) != 2 {
		t.Fatalf("compare %+v", view)
	}
	if view.HTMLURL == "" {
		t.Fatal("missing html url")
	}
}

func TestClientCompareRequiresRepo(t *testing.T) {
	c := New(http.DefaultClient, "http://example.invalid", "")
	if _, err := c.Compare(context.Background(), "", "api", "a", "b"); err == nil {
		t.Fatal("expected error")
	}
}
