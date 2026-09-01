package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"reqflow/internal/port"
)

type Provider struct {
	Name     string
	Priority int
	Client   port.LLMClient
}

type providerState struct {
	Failures  int
	OpenUntil time.Time
}

type ProviderRouter struct {
	providers []Provider
	threshold int
	cooldown  time.Duration
	now       func() time.Time
	mu        sync.Mutex
	state     map[string]providerState
}

func NewProviderRouter(providers []Provider, failureThreshold int, cooldown time.Duration) (*ProviderRouter, error) {
	if len(providers) == 0 {
		return nil, fmt.Errorf("provider router 至少需要一个 Provider")
	}
	if failureThreshold <= 0 {
		failureThreshold = 2
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	clean := make([]Provider, 0, len(providers))
	seen := map[string]bool{}
	for _, provider := range providers {
		provider.Name = strings.TrimSpace(provider.Name)
		if provider.Name == "" || provider.Client == nil || seen[provider.Name] {
			return nil, fmt.Errorf("Provider 名称或客户端非法/重复")
		}
		seen[provider.Name] = true
		clean = append(clean, provider)
	}
	sortProviders(clean)
	return &ProviderRouter{providers: clean, threshold: failureThreshold, cooldown: cooldown,
		now: time.Now, state: make(map[string]providerState, len(clean))}, nil
}

func (r *ProviderRouter) Stream(ctx context.Context, value *port.Context, onEvent func(port.AssistantEvent)) (*port.Message, error) {
	return r.try(ctx, func(provider Provider) (*port.Message, error) { return provider.Client.Stream(ctx, value, onEvent) })
}

func (r *ProviderRouter) Complete(ctx context.Context, value *port.Context) (*port.Message, error) {
	return r.try(ctx, func(provider Provider) (*port.Message, error) { return provider.Client.Complete(ctx, value) })
}

func (r *ProviderRouter) Ping(ctx context.Context) error {
	_, err := r.try(ctx, func(provider Provider) (*port.Message, error) {
		return nil, provider.Client.Ping(ctx)
	})
	return err
}

func (r *ProviderRouter) try(ctx context.Context, call func(Provider) (*port.Message, error)) (*port.Message, error) {
	var lastMessage *port.Message
	var failures []string
	for _, provider := range r.availableProviders() {
		if err := ctx.Err(); err != nil {
			return lastMessage, err
		}
		message, err := call(provider)
		if err == nil {
			r.markSuccess(provider.Name)
			return message, nil
		}
		lastMessage = message
		modelErr := ClassifyModelError(err)
		modelErr.Provider = provider.Name
		failures = append(failures, modelErr.Error())
		r.markFailure(provider.Name, modelErr)
		if !modelErr.Retryable() {
			return message, modelErr
		}
	}
	if len(failures) == 0 {
		return lastMessage, fmt.Errorf("没有健康的 LLM Provider")
	}
	return lastMessage, &ModelError{Kind: ModelUnavailable, Err: fmt.Errorf("Provider 全部失败: %s", strings.Join(failures, "; "))}
}

func (r *ProviderRouter) availableProviders() []Provider {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]Provider, 0, len(r.providers))
	for _, provider := range r.providers {
		state := r.state[provider.Name]
		if state.OpenUntil.After(now) {
			continue
		}
		result = append(result, provider)
	}
	return result
}

func (r *ProviderRouter) markSuccess(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state[name] = providerState{}
}

func (r *ProviderRouter) markFailure(name string, err *ModelError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.state[name]
	state.Failures++
	if state.Failures >= r.threshold {
		state.OpenUntil = r.now().Add(r.cooldown)
	}
	r.state[name] = state
	_ = err
}

func sortProviders(providers []Provider) {
	for i := 1; i < len(providers); i++ {
		for j := i; j > 0 && providers[j].Priority < providers[j-1].Priority; j-- {
			providers[j], providers[j-1] = providers[j-1], providers[j]
		}
	}
}
