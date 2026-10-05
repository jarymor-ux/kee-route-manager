package setup

const (
	defaultUpdateRepository = "jarymor-ux/kee-route-manager"
	defaultUpdatePublicKey  = "t8ZyoMK5zMz2vTBuWaH8HIwMOo+E1nJXydOak0RWKAE"
	defaultUpdateChannel    = "rc"
)

func defaultUpdateOptions() UpdateOptions {
	return UpdateOptions{
		Channel:          defaultUpdateChannel,
		GitHubRepository: defaultUpdateRepository,
		PublicKey:        defaultUpdatePublicKey,
		AutoApply:        false,
	}
}
