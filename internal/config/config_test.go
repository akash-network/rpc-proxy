package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTest(t *testing.T) {
	cfg := Must()
	require.NotEmpty(t, cfg.Listen)
	require.NotEmpty(t, cfg.SeedURL)
	require.NotZero(t, cfg.SeedRefreshInterval)
	require.NotZero(t, cfg.ChainID)
	require.NotZero(t, cfg.HealthyThreshold)
	require.NotZero(t, cfg.ProxyRequestTimeout)
	require.NotZero(t, cfg.UnhealthyServerRecoverChancePct)
}
