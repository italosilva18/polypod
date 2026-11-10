package ai

import (
	"context"
	"errors"
	"strings"

	"github.com/costa/polypod/internal/smartroute"
)

// SetRoutingPolicy installs an offline-calibrated, same-provider policy. The
// caller supplies the configured provider and allowlist, so a local policy
// file cannot silently introduce a new model endpoint or provider.
func (s *Service) SetRoutingPolicy(policy smartroute.CalibratedPolicy, provider, defaultModel string, allowed []string) error {
	if s.client == nil || s.client.Model() != defaultModel {
		return errors.New("modelo padrão do cliente não corresponde à política")
	}
	if err := policy.Validate(provider, defaultModel, allowed); err != nil {
		return err
	}
	clone := policy.Clone()
	s.routingMu.Lock()
	s.routingPolicy = &clone
	s.routingMu.Unlock()
	return nil
}

func (s *Service) withRoutedModel(ctx context.Context, req AnswerRequest) context.Context {
	category := strings.TrimSpace(req.RoutingCategory)
	if category == "" {
		// Only these channels have a stable, specific task category. A
		// conversational CLI or WhatsApp turn is too broad to classify from
		// its transport, and therefore retains the configured model.
		switch req.Channel {
		case "task_queue", "scheduler", "cli_bg":
			category = "routine"
		}
	}
	if category == "" {
		return ctx
	}
	s.routingMu.RLock()
	if s.manualModel || s.routingPolicy == nil {
		s.routingMu.RUnlock()
		return ctx
	}
	model := s.routingPolicy.ModelFor(category)
	s.routingMu.RUnlock()
	return withRequestModel(ctx, model)
}
