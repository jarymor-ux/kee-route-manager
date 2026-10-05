package setup

import "github.com/jarymor-ux/kee-route-manager/internal/releasetrust"

const (
	defaultUpdateRepository = "jarymor-ux/kee-route-manager"
	defaultUpdateChannel    = "rc"
)

func defaultUpdateOptions() UpdateOptions {
	return UpdateOptions{
		Channel:          defaultUpdateChannel,
		GitHubRepository: defaultUpdateRepository,
		PublicKey:        releasetrust.PublicKey(),
		AutoApply:        false,
	}
}
