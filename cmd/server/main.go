package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	primaryhttp "github.com/maisumvictor/Workaholic/internal/adapters/primary/http"
	awsadapter "github.com/maisumvictor/Workaholic/internal/adapters/secondary/aws"
	runbooksfs "github.com/maisumvictor/Workaholic/internal/adapters/secondary/fs/runbooks"
	k8sadapter "github.com/maisumvictor/Workaholic/internal/adapters/secondary/k8s"
	langchain "github.com/maisumvictor/Workaholic/internal/adapters/secondary/llm/langchain"
	slackadapter "github.com/maisumvictor/Workaholic/internal/adapters/secondary/slack"
	"github.com/maisumvictor/Workaholic/internal/adapters/secondary/storage/sqlite"
	"github.com/maisumvictor/Workaholic/internal/core/ports"
	"github.com/maisumvictor/Workaholic/internal/core/services"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	cfg := loadConfig()
	log.Info("starting workaholic", "mode", cfg.Mode, "addr", cfg.ListenAddr)

	db, err := sqlite.Open(ctx, cfg.SQLitePath)
	if err != nil {
		return err
	}
	defer db.Close()
	repo := sqlite.NewIncidentRepo(db)
	runbooks := runbooksfs.NewLocalRepo(cfg.RunbooksDir)

	k8sClients, err := k8sadapter.Build(k8sadapter.NewFactoryConfigFromEnv())
	if err != nil {
		return err
	}

	var awsClient *awsadapter.Client
	awsClient, err = awsadapter.New(ctx, cfg.AWSRegion)
	if err != nil {
		log.Warn("aws client unavailable; AWS tools disabled", "err", err)
		awsClient = nil
	}

	var awsInv ports.AWSInvestigator
	var awsRem ports.AWSRemediator
	if awsClient != nil {
		awsInv = awsClient
		awsRem = awsClient
	}

	llm, err := langchain.New(langchain.Config{
		Provider: cfg.LLMProvider,
		Model:    cfg.LLMModel,
		APIKey:   cfg.LLMAPIKey,
	}, k8sClients.Investigator, awsInv)
	if err != nil {
		return err
	}

	var msg ports.Messaging = slackadapter.Noop{}
	if cfg.SlackBotToken != "" && cfg.SlackChannel != "" {
		msg = slackadapter.New(cfg.SlackBotToken, cfg.SlackChannel)
	} else {
		log.Warn("slack not configured; approvals will only be available via CLI")
	}

	incidents := services.NewIncidentService(
		repo, runbooks, llm, msg, nil,
		services.PlanExecutor{K8s: k8sClients.Remediator, AWS: awsRem},
		log,
	)
	approvals := services.NewApprovalService(incidents, services.NewStaticApprovers(cfg.ApproverIDs), log)

	srv := primaryhttp.NewServer(primaryhttp.Config{
		Addr:               cfg.ListenAddr,
		APIToken:           cfg.APIToken,
		SlackSigningSecret: cfg.SlackSigningSecret,
	}, incidents, approvals, log)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		log.Info("shutting down")
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

type config struct {
	Mode               string
	ListenAddr         string
	SQLitePath         string
	RunbooksDir        string
	APIToken           string
	SlackBotToken      string
	SlackSigningSecret string
	SlackChannel       string
	ApproverIDs        []string
	LLMProvider        string
	LLMModel           string
	LLMAPIKey          string
	AWSRegion          string
}

func loadConfig() config {
	approvers := splitCSV(env("SLACK_APPROVER_IDS", ""))
	provider := env("LLM_PROVIDER", "openai")
	apiKey := os.Getenv("OPENAI_API_KEY")
	if strings.EqualFold(provider, "anthropic") {
		apiKey = firstNonEmpty(os.Getenv("ANTHROPIC_API_KEY"), apiKey)
	}
	return config{
		Mode:               env("MODE", "vm"),
		ListenAddr:         env("LISTEN_ADDR", ":8080"),
		SQLitePath:         env("SQLITE_PATH", "workaholic.db"),
		RunbooksDir:        env("RUNBOOKS_DIR", "runbooks"),
		APIToken:           os.Getenv("WORKAHOLIC_API_TOKEN"),
		SlackBotToken:      os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret: os.Getenv("SLACK_SIGNING_SECRET"),
		SlackChannel:       os.Getenv("SLACK_CHANNEL"),
		ApproverIDs:        approvers,
		LLMProvider:        provider,
		LLMModel:           os.Getenv("LLM_MODEL"),
		LLMAPIKey:          apiKey,
		AWSRegion:          os.Getenv("AWS_REGION"),
	}
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
