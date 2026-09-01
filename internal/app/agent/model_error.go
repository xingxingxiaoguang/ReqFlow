package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	domain "reqflow/internal/domain/workflow"
)

type ModelErrorKind string

const (
	ModelTransportError  ModelErrorKind = "transport_error"
	ModelRateLimited     ModelErrorKind = "rate_limited"
	ModelAuthentication  ModelErrorKind = "authentication_failed"
	ModelUnavailable     ModelErrorKind = "provider_unavailable"
	ModelContextOverflow ModelErrorKind = "context_overflow"
	ModelInvalidOutput   ModelErrorKind = "invalid_output"
	ModelPolicyBlocked   ModelErrorKind = "policy_blocked"
)

type ModelError struct {
	Kind     ModelErrorKind
	Provider string
	Status   int
	Err      error
}

func (e *ModelError) Error() string {
	if e == nil {
		return ""
	}
	provider := strings.TrimSpace(e.Provider)
	if provider == "" {
		provider = "model"
	}
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", provider, e.Kind)
	}
	return fmt.Sprintf("%s: %v", provider, e.Err)
}

func (e *ModelError) Unwrap() error { return e.Err }

func (e *ModelError) Retryable() bool {
	if e == nil {
		return false
	}
	switch e.Kind {
	case ModelTransportError, ModelRateLimited, ModelAuthentication, ModelUnavailable, ModelInvalidOutput:
		return true
	default:
		return false
	}
}

func (e *ModelError) FailureKind() domain.ModelFailureKind {
	if e == nil {
		return domain.ModelUnavailable
	}
	switch e.Kind {
	case ModelRateLimited:
		return domain.ModelRateLimited
	case ModelAuthentication:
		return domain.ModelAuthentication
	case ModelContextOverflow:
		return domain.ModelContextOverflow
	case ModelInvalidOutput:
		return domain.ModelInvalidOutput
	case ModelPolicyBlocked:
		return domain.ModelPolicyBlocked
	default:
		return domain.ModelUnavailable
	}
}

var httpCodePattern = regexp.MustCompile(`\bHTTP\s+(\d{3})\b`)

func ClassifyModelError(err error) *ModelError {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &ModelError{Kind: ModelTransportError, Err: err}
	}
	message := strings.ToLower(err.Error())
	status := 0
	if match := httpCodePattern.FindStringSubmatch(err.Error()); len(match) == 2 {
		status, _ = strconv.Atoi(match[1])
	}
	switch {
	case status == 401 || status == 403 || strings.Contains(message, "unauthoriz") || strings.Contains(message, "api key"):
		return &ModelError{Kind: ModelAuthentication, Status: status, Err: err}
	case status == 408 || status == 409 || status == 429 || strings.Contains(message, "rate limit") || strings.Contains(message, "too many"):
		return &ModelError{Kind: ModelRateLimited, Status: status, Err: err}
	case strings.Contains(message, "context") && (strings.Contains(message, "length") || strings.Contains(message, "window") || strings.Contains(message, "token")):
		return &ModelError{Kind: ModelContextOverflow, Status: status, Err: err}
	case strings.Contains(message, "policy") || strings.Contains(message, "safety") || strings.Contains(message, "content filter"):
		return &ModelError{Kind: ModelPolicyBlocked, Status: status, Err: err}
	case status >= 500 || strings.Contains(message, "timeout") || strings.Contains(message, "connection") || strings.Contains(message, "temporarily"):
		return &ModelError{Kind: ModelTransportError, Status: status, Err: err}
	case strings.Contains(message, "response") || strings.Contains(message, "json") || strings.Contains(message, "protocol"):
		return &ModelError{Kind: ModelInvalidOutput, Status: status, Err: err}
	default:
		return &ModelError{Kind: ModelUnavailable, Status: status, Err: err}
	}
}
