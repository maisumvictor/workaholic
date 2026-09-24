package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	slackadapter "github.com/maisumvictor/Workaholic/internal/adapters/secondary/slack"
	"github.com/maisumvictor/Workaholic/internal/core/domain"
	"github.com/slack-go/slack"
)

const slackMaxAge = 5 * time.Minute

func (s *Server) handleSlackInteractive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := verifySlackSignature(s.cfg.SlackSigningSecret, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), body); err != nil {
		s.log.WarnContext(r.Context(), "slack signature rejected", "err", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	payloadRaw := string(body)
	if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		values, parseErr := url.ParseQuery(string(body))
		if parseErr != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if v := values.Get("payload"); v != "" {
			payloadRaw = v
		}
	}

	var ic slack.InteractionCallback
	if err := json.Unmarshal([]byte(payloadRaw), &ic); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	actor := ic.User.ID
	var actionID, incidentID string
	for _, act := range ic.ActionCallback.BlockActions {
		actionID = act.ActionID
		incidentID = act.Value
		break
	}
	if incidentID == "" {
		http.Error(w, "missing incident id", http.StatusBadRequest)
		return
	}

	var (
		inc *domain.Incident
	)
	switch actionID {
	case slackadapter.ActionApprove:
		inc, err = s.approvals.Approve(r.Context(), incidentID, actor)
	case slackadapter.ActionReject:
		inc, err = s.approvals.Reject(r.Context(), incidentID, actor, "rejected via slack")
	case slackadapter.ActionAskInvestigator:
		inc, err = s.approvals.FollowUp(r.Context(), incidentID, actor, followUpQuestion(ic))
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.log.ErrorContext(r.Context(), "slack interactive action failed", "err", err, "actor", actor, "incident", incidentID)
		writeJSON(w, statusFor(err), map[string]string{"error": err.Error()})
		return
	}

	text := fmt.Sprintf("Incident `%s` is now *%s* (by <@%s>).", inc.ID, inc.Status, actor)
	if actionID == slackadapter.ActionAskInvestigator {
		text = fmt.Sprintf("Follow-up on `%s` posted (investigator only, no remediator).", inc.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"replace_original": false,
		"text":             text,
	})
}

func followUpQuestion(ic slack.InteractionCallback) string {
	if ic.BlockActionState == nil {
		return ""
	}
	for _, byAction := range ic.BlockActionState.Values {
		if q, ok := byAction[slackadapter.ActionFollowUpQuestion]; ok {
			return strings.TrimSpace(q.Value)
		}
		for _, act := range byAction {
			if strings.TrimSpace(act.Value) != "" {
				return strings.TrimSpace(act.Value)
			}
		}
	}
	return ""
}

func verifySlackSignature(secret, timestampHeader, signatureHeader string, body []byte) error {
	if secret == "" {
		return domain.ErrSignatureInvalid
	}
	ts, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return domain.ErrSignatureInvalid
	}
	if time.Since(time.Unix(ts, 0)) > slackMaxAge || time.Unix(ts, 0).After(time.Now().Add(1*time.Minute)) {
		return domain.ErrSignatureInvalid
	}
	base := fmt.Sprintf("v0:%s:%s", timestampHeader, body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(base))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signatureHeader)) {
		return domain.ErrSignatureInvalid
	}
	return nil
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrEmptyFollowUp):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrApproverDenied), errors.Is(err, domain.ErrUnauthorized):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrInvalidStatus), errors.Is(err, domain.ErrAlreadyTerminal):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
