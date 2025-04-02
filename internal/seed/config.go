package seed

import "time"

// Config specifies the required configuration to configure a Seeder.
type Config struct {
	// SeedURL is the URL to fetch for server updates.
	SeedURL string

	// SeedRefreshInterval sets how frequently to fetch SeedURL for updates.
	SeedRefreshInterval time.Duration

	// ChainID is the ID of the chain.
	ChainID string
}
