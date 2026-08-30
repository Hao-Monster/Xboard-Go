package httpapi

import (
	"context"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/subscription"
)

func (s *server) publicSubscriptionURL(ctx context.Context, token, fragment string) (string, error) {
	config, err := s.store.GetSubscriptionRenderConfig(ctx, "")
	if err != nil {
		return "", err
	}
	return s.publicSubscriptionURLFromConfig(config, token, fragment)
}

func (s *server) publicSubscriptionURLFromConfig(config store.SubscriptionRenderConfig, token, fragment string) (string, error) {
	return subscription.BuildPublicURL(subscription.PublicURLConfig{
		Origins: config.SubscribeURL, AppURL: config.AppURL, PanelURL: s.panelURL,
		Path: config.Path, ForceHTTPS: config.ForceHTTPS,
	}, token, fragment)
}
