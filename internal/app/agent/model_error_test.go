package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type routerClient struct {
	streamErr error
	message   *port.Message
	calls     int
}

func (c *routerClient) Stream(context.Context, *port.Context, func(port.AssistantEvent)) (*port.Message, error) {
	c.calls++
	return c.message, c.streamErr
}
func (c *routerClient) Complete(context.Context, *port.Context) (*port.Message, error) {
	return c.message, c.streamErr
}
func (c *routerClient) Ping(context.Context) error { return c.streamErr }

func TestClassifyModelErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		kind    ModelErrorKind
		failure domain.ModelFailureKind
	}{
		{name: "auth", err: errors.New("LLM HTTP 401: invalid api key"), kind: ModelAuthentication, failure: domain.ModelAuthentication},
		{name: "rate", err: errors.New("LLM HTTP 429: rate limit"), kind: ModelRateLimited, failure: domain.ModelRateLimited},
		{name: "server", err: errors.New("LLM HTTP 503: unavailable"), kind: ModelTransportError, failure: domain.ModelUnavailable},
		{name: "context", err: errors.New("context length exceeded"), kind: ModelContextOverflow, failure: domain.ModelContextOverflow},
		{name: "policy", err: errors.New("content filter policy blocked"), kind: ModelPolicyBlocked, failure: domain.ModelPolicyBlocked},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classified := ClassifyModelError(test.err)
			if classified.Kind != test.kind || classified.FailureKind() != test.failure {
				t.Fatalf("classified=%+v failure=%s", classified, classified.FailureKind())
			}
		})
	}
}

func TestProviderRouterFallsBackAndDoesNotBypassPolicy(t *testing.T) {
	primary := &routerClient{streamErr: fmt.Errorf("LLM HTTP 503: primary down")}
	secondary := &routerClient{message: &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonStop}}
	router, err := NewProviderRouter([]Provider{{Name: "primary", Priority: 1, Client: primary}, {Name: "secondary", Priority: 2, Client: secondary}}, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Stream(context.Background(), &port.Context{}, nil); err != nil {
		t.Fatal(err)
	}
	if primary.calls != 1 || secondary.calls != 1 {
		t.Fatalf("fallback calls primary=%d secondary=%d", primary.calls, secondary.calls)
	}

	blocked := &routerClient{streamErr: errors.New("content filter policy blocked")}
	backup := &routerClient{message: secondary.message}
	blockedRouter, _ := NewProviderRouter([]Provider{{Name: "blocked", Priority: 1, Client: blocked}, {Name: "backup", Priority: 2, Client: backup}}, 2, time.Minute)
	if _, err := blockedRouter.Stream(context.Background(), &port.Context{}, nil); err == nil {
		t.Fatal("policy block must be returned instead of bypassed")
	}
	if backup.calls != 0 {
		t.Fatal("policy block 不得调用备用 Provider")
	}
}
