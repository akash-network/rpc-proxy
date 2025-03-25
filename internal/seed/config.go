package seed

import "time"

type Config struct {
	// SeedURL is the URL to fetch for server updates.
	SeedURL string

	// SeedRefreshInterval sets how frequently to fetch SeedURL for updates.
	SeedRefreshInterval time.Duration

	// ChainID is the ID of the chain.
	ChainID string
}
