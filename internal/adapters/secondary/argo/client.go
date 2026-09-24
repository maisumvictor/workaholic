package argo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
)

// Client implements ports.ArgoInvestigator over the Argo CD HTTP API.
type Client struct {
	http  *http.Client
	base  string
	token string
}

func New(h *http.Client, baseURL, token string) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{http: h, base: strings.TrimRight(baseURL, "/"), token: token}
}

func (c *Client) GetApplication(ctx context.Context, namespace, name string) (*ports.ArgoApplicationView, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: application name is required", domain.ErrInvalidToolArgs)
	}
	u, err := url.Parse(c.base + "/api/v1/applications/" + url.PathEscape(name))
	if err != nil {
		return nil, err
	}
	if ns := strings.TrimSpace(namespace); ns != "" {
		q := u.Query()
		q.Set("appNamespace", ns)
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, domain.Wrap(err, "argo get application")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, domain.Wrap(err, "argo get application read")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("argo get application: status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Source struct {
				RepoURL string `json:"repoURL"`
			} `json:"source"`
		} `json:"spec"`
		Status struct {
			Sync struct {
				Status   string `json:"status"`
				Revision string `json:"revision"`
			} `json:"sync"`
			Health struct {
				Status string `json:"status"`
			} `json:"health"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, domain.Wrap(err, "argo application json")
	}
	ns := raw.Metadata.Namespace
	if ns == "" {
		ns = namespace
	}
	return &ports.ArgoApplicationView{
		Name:         firstNonEmpty(raw.Metadata.Name, name),
		Namespace:    ns,
		SyncStatus:   raw.Status.Sync.Status,
		HealthStatus: raw.Status.Health.Status,
		Revision:     raw.Status.Sync.Revision,
		RepoURL:      raw.Spec.Source.RepoURL,
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
