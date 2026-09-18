package github

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

const maxCommits = 10

// Client implements ports.GitHubInvestigator over the Compare API.
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

func (c *Client) Compare(ctx context.Context, owner, repo, base, head string) (*ports.GitHubCompareView, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	base = strings.TrimSpace(base)
	head = strings.TrimSpace(head)
	if owner == "" || repo == "" || base == "" || head == "" {
		return nil, fmt.Errorf("%w: owner, repo, base, and head are required", domain.ErrInvalidToolArgs)
	}
	u := c.base + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/compare/" + url.PathEscape(base) + "..." + url.PathEscape(head)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, domain.Wrap(err, "github compare")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, domain.Wrap(err, "github compare read")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("github compare: status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var raw struct {
		Status   string `json:"status"`
		AheadBy  int    `json:"ahead_by"`
		BehindBy int    `json:"behind_by"`
		HTMLURL  string `json:"html_url"`
		Commits  []struct {
			Commit struct {
				Message string `json:"message"`
			} `json:"commit"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, domain.Wrap(err, "github compare json")
	}
	msgs := make([]string, 0, len(raw.Commits))
	for _, cmt := range raw.Commits {
		msg := strings.TrimSpace(cmt.Commit.Message)
		if msg == "" {
			continue
		}
		if i := strings.IndexByte(msg, '\n'); i > 0 {
			msg = msg[:i]
		}
		msgs = append(msgs, msg)
		if len(msgs) >= maxCommits {
			break
		}
	}
	return &ports.GitHubCompareView{
		Owner:    owner,
		Repo:     repo,
		Base:     base,
		Head:     head,
		Status:   raw.Status,
		AheadBy:  raw.AheadBy,
		BehindBy: raw.BehindBy,
		Commits:  msgs,
		HTMLURL:  raw.HTMLURL,
	}, nil
}
