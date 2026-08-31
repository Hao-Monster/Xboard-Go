package subscription

import "github.com/Hao-Monster/Xboard-Go/internal/publicurl"

// PublicURLConfig is the immutable projection needed to build an externally
// visible subscription address. Origins is normalized by the settings store.
type PublicURLConfig = publicurl.SubscriptionConfig

// BuildPublicURL preserves Xboard's random distribution across configured
// origins, while falling back to app_url and then the deployment panel URL.
func BuildPublicURL(config PublicURLConfig, token, fragment string) (string, error) {
	return publicurl.BuildSubscription(config, token, fragment)
}
