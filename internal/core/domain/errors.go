package domain

import "fmt"

// Sentinel errors used across the domain and adapters.
var (
	ErrNotFound             = fmt.Errorf("not found")
	ErrInvalidStatus        = fmt.Errorf("invalid incident status transition")
	ErrUnauthorized         = fmt.Errorf("unauthorized")
	ErrProtectedNamespace   = fmt.Errorf("namespace is protected and cannot be mutated")
	ErrReplicaCapExceeded   = fmt.Errorf("requested replica count exceeds safety cap")
	ErrUnknownTool          = fmt.Errorf("unknown remediator tool")
	ErrInvalidToolArgs      = fmt.Errorf("invalid tool arguments")
	ErrPlanRequiresApproval = fmt.Errorf("action plan requires human approval")
	ErrAlreadyTerminal      = fmt.Errorf("incident is already in a terminal state")
	ErrEmptyActionPlan      = fmt.Errorf("action plan has no executable steps")
	ErrSignatureInvalid     = fmt.Errorf("request signature is invalid")
	ErrApproverDenied       = fmt.Errorf("caller is not an authorized approver")
	ErrVerificationFailed   = fmt.Errorf("post-remediation verification failed")
)

// ValidationError captures a field-level domain validation failure.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validation error on %s: %s", e.Field, e.Message)
}

// Wrap annotates err with context without losing the original.
func Wrap(err error, msg string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}
