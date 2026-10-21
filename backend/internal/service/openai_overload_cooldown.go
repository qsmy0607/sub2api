package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

func (s *OpenAIGatewayService) openAI503OverloadSettings(ctx context.Context) (retryCount, cooldownSeconds int) {
	retryCount, cooldownSeconds = 0, 15
	if s == nil || s.settingService == nil {
		return retryCount, cooldownSeconds
	}
	settings, err := s.settingService.GetOverloadCooldownSettings(ctx)
	if err != nil {
		return retryCount, cooldownSeconds
	}
	return settings.OpenAI503SameAccountRetryCount, settings.OpenAI503CooldownSeconds
}

// ApplyOpenAI503OverloadRetryPolicy applies the current runtime setting to a
// recognized 503 overload. It is intentionally evaluated for every failure so
// admin changes take effect without restarting the process.
func (s *OpenAIGatewayService) ApplyOpenAI503OverloadRetryPolicy(ctx context.Context, failoverErr *UpstreamFailoverError) {
	if failoverErr == nil || failoverErr.StatusCode != http.StatusServiceUnavailable || !failoverErr.IsOpenAICapacityShed() {
		return
	}
	retryCount, _ := s.openAI503OverloadSettings(ctx)
	failoverErr.SameAccountRetryMaxSet = true
	failoverErr.SameAccountRetryMax = retryCount
}

// MarkOpenAIOverloadCooldown temporarily removes one account+model pair from
// scheduling after its same-account retry budget has been exhausted.
func (s *OpenAIGatewayService) MarkOpenAIOverloadCooldown(
	ctx context.Context,
	account *Account,
	model string,
	failoverErr *UpstreamFailoverError,
) {
	if s == nil || s.accountRepo == nil || account == nil || failoverErr == nil {
		return
	}
	if failoverErr.StatusCode != http.StatusServiceUnavailable || !failoverErr.IsOpenAICapacityShed() {
		return
	}
	_, seconds := s.openAI503OverloadSettings(ctx)
	model = strings.TrimSpace(model)
	if seconds <= 0 || model == "" {
		return
	}

	resetAt := time.Now().Add(time.Duration(seconds) * time.Second)
	reason := fmt.Sprintf("openai 503 server_is_overloaded cooldown (%ds)", seconds)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, model, resetAt, reason); err != nil {
		logger.FromContext(ctx).Warn("openai.overload_model_cooldown_failed",
			zap.Int64("account_id", account.ID),
			zap.String("model", model),
			zap.Error(err),
		)
		return
	}
	logger.FromContext(ctx).Warn("openai.overload_model_cooldown_set",
		zap.Int64("account_id", account.ID),
		zap.String("model", model),
		zap.Int("cooldown_seconds", seconds),
	)
}
