package seed

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Seed struct {
	Status  string `json:"status"`
	ChainID string `json:"chain_id"`
	APIs    Apis   `json:"apis"`
}

type Provider struct {
	Address  string `json:"address"`
	Provider string `json:"provider"`
}

type Apis struct {
	RPC  []Provider `json:"rpc"`
	Rest []Provider `json:"rest"`
}

func fetch(url string) (Seed, error) {
	var seed Seed
	resp, err := http.Get(url)
	if err != nil {
		return seed, fmt.Errorf("get seed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return seed, fmt.Errorf("request failed: %s", resp.Status)
	}

	bts, err := io.ReadAll(resp.Body)
	if err != nil {
		return seed, fmt.Errorf("read seed: %w", err)
	}
	if err := json.Unmarshal(bts, &seed); err != nil {
		return seed, fmt.Errorf("parse seed: %w", err)
	}
	return seed, nil
}
