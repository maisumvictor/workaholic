package runbooks

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/maisumvictor/Workaholic/internal/core/domain"
)

// LocalRepo loads Markdown runbooks from a directory. Front matter is a
// simple YAML-like block delimited by --- lines:
//
//	---
//	id: hpa-maxed
//	title: HPA at max replicas
//	risk: auto_remediate
//	match.alertname: KubeHPAReplicasAtMax
//	---
type LocalRepo struct {
	dir string
}

func NewLocalRepo(dir string) *LocalRepo {
	return &LocalRepo{dir: dir}
}

func (r *LocalRepo) List(ctx context.Context) ([]domain.Runbook, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, domain.Wrap(err, "read runbooks dir")
	}
	out := make([]domain.Runbook, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		rb, err := r.loadFile(filepath.Join(r.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, *rb)
	}
	return out, nil
}

func (r *LocalRepo) Get(ctx context.Context, id string) (*domain.Runbook, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, domain.ErrNotFound
}

func (r *LocalRepo) Match(ctx context.Context, labels map[string]string) ([]domain.Runbook, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	matched := make([]domain.Runbook, 0)
	for _, rb := range all {
		if rb.Matches(labels) {
			matched = append(matched, rb)
		}
	}
	return matched, nil
}

func (r *LocalRepo) loadFile(path string) (*domain.Runbook, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, domain.Wrap(err, "open runbook")
	}
	defer f.Close()

	rb := domain.Runbook{
		Path:        path,
		MatchLabels: map[string]string{},
		RiskLevel:   domain.RiskRequiresApproval,
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		inFront bool
		started bool
		body    strings.Builder
	)
	for sc.Scan() {
		line := sc.Text()
		if !started && strings.TrimSpace(line) == "---" {
			started = true
			inFront = true
			continue
		}
		if inFront {
			if strings.TrimSpace(line) == "---" {
				inFront = false
				continue
			}
			parseFrontMatterLine(&rb, line)
			continue
		}
		started = true
		body.WriteString(line)
		body.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return nil, domain.Wrap(err, "scan runbook")
	}
	rb.Body = body.String()
	if rb.ID == "" {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		rb.ID = base
	}
	if rb.Title == "" {
		rb.Title = rb.ID
	}
	return &rb, nil
}

func parseFrontMatterLine(rb *domain.Runbook, line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	k, v, ok := strings.Cut(line, ":")
	if !ok {
		return
	}
	k = strings.TrimSpace(k)
	v = strings.Trim(strings.TrimSpace(v), `"'`)
	switch {
	case k == "id":
		rb.ID = v
	case k == "title":
		rb.Title = v
	case k == "risk" || k == "risk_level":
		if rl, ok := domain.ParseRiskLevel(v); ok {
			rb.RiskLevel = rl
		}
	case strings.HasPrefix(k, "match."):
		rb.MatchLabels[strings.TrimPrefix(k, "match.")] = v
	}
}
