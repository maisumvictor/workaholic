package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "workaholic",
		Short: "CLI for the Workaholic incident response bot",
	}
	var (
		apiURL string
		token  string
		actor  string
	)
	root.PersistentFlags().StringVar(&apiURL, "api-url", env("WORKAHOLIC_API_URL", "http://127.0.0.1:8080"), "Workaholic server URL")
	root.PersistentFlags().StringVar(&token, "token", os.Getenv("WORKAHOLIC_API_TOKEN"), "Bearer token for the CLI API")
	root.PersistentFlags().StringVar(&actor, "actor", env("WORKAHOLIC_ACTOR", os.Getenv("USER")), "Approver identity (must be on the whitelist)")

	client := func() *apiClient {
		return &apiClient{base: strings.TrimRight(apiURL, "/"), token: token, actor: actor, http: &http.Client{Timeout: 15 * time.Second}}
	}

	incidents := &cobra.Command{Use: "incidents", Short: "Query and approve incidents"}
	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "List incidents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := "/api/v1/incidents"
			if status != "" {
				path += "?status=" + status
			}
			return client().print("GET", path, nil)
		},
	}
	list.Flags().StringVar(&status, "status", "", "Filter by status")

	get := &cobra.Command{
		Use:   "get [id]",
		Short: "Show an incident and its action plan (diff)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client().print("GET", "/api/v1/incidents/"+args[0], nil)
		},
	}
	audit := &cobra.Command{
		Use:   "audit [id]",
		Short: "Show the audit trail for an incident",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return client().print("GET", "/api/v1/incidents/"+args[0]+"/audit", nil)
		},
	}
	approve := &cobra.Command{
		Use:   "approve [id]",
		Short: "Approve and execute a pending action plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]string{"actor": actor}
			return client().print("POST", "/api/v1/incidents/"+args[0]+"/approve", body)
		},
	}
	var reason string
	reject := &cobra.Command{
		Use:   "reject [id]",
		Short: "Reject a pending action plan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]string{"actor": actor, "reason": reason}
			return client().print("POST", "/api/v1/incidents/"+args[0]+"/reject", body)
		},
	}
	reject.Flags().StringVar(&reason, "reason", "rejected via cli", "Rejection reason")

	incidents.AddCommand(list, get, audit, approve, reject)
	root.AddCommand(incidents)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

type apiClient struct {
	base  string
	token string
	actor string
	http  *http.Client
}

func (c *apiClient) print(method, path string, body any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.actor != "" {
		req.Header.Set("X-Actor", c.actor)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	pretty := out
	var buf bytes.Buffer
	if json.Indent(&buf, out, "", "  ") == nil {
		pretty = buf.Bytes()
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, pretty)
	}
	fmt.Println(string(pretty))
	return nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
