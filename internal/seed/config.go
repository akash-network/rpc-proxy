package seed

import "time"

// Config specifies the required configuration to configure a Seeder.
type Config struct {
	// SeedURL is the URL to fetch for server updates.
	SeedURL string

	// SeedRefreshInterval sets how frequently to fetch SeedURL for updates.
	SeedRefreshInterval time.Duration

	// HealthRefreshInterval sets how frequently to re-probe the cached node list for
	// health and block height. It is decoupled from SeedRefreshInterval so freshness
	// can be re-evaluated on a block-time cadence without re-fetching the seed URL.
	// When zero or negative it falls back to SeedRefreshInterval.
	HealthRefreshInterval time.Duration

	// ChainID is the ID of the chain.
	ChainID string

	// EnableRemote enables/disables remote seed fetching
	EnableRemote bool

	// AdditionalNodes contains additional configured nodes
	AdditionalNodes struct {
		RPC  []string
		REST []string
		GRPC []string
	}
}
