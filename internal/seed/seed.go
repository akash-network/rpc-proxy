package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
)

type Seed struct {
	Status  string `json:"status"`
	ChainID string `json:"chain_id"`
	APIs    Apis   `json:"apis"`
}

type Provider struct {
	Address  string `json:"address"`
	Provider string `json:"provider"`
	Status   Status
}

type Status struct {
	Reachable  bool
	CatchingUp bool
}

type Apis struct {
	RPC  []Provider `json:"rpc"`
	Rest []Provider `json:"rest"`
	GRPC []Provider `json:"grpc"`
}

var LatestBlock = 0 // TODO: remove, very bad design.

func fetch(log *slog.Logger, url string) (Seed, error) {
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

	for i, rpcProxy := range seed.APIs.RPC {
		// Create an HTTP RPC client
		client, err := rpchttp.New(rpcProxy.Address)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create RPC client: %v", err), "name", rpcProxy.Provider)
			rpcProxy.Status.Reachable = false
			continue
		}

		// Fetch the status
		status, err := client.Status(context.Background())
		if err != nil {
			log.Error(fmt.Sprintf("failed to create RPC client: %v", err), "name", rpcProxy.Provider)
			rpcProxy.Status.Reachable = false
			continue
		}

		log.Info("added rpc server", "name", rpcProxy.Provider, "catching_up", status.SyncInfo.CatchingUp)

		seed.APIs.GRPC[i] = rpcProxy.WithStatus(Status{
			CatchingUp: status.SyncInfo.CatchingUp,
			Reachable:  true,
		})
	}

	for i, restProxy := range seed.APIs.Rest {
		// TODO: add rest node healtchecks
		seed.APIs.GRPC[i] = restProxy.WithStatus(Status{
			CatchingUp: false,
			Reachable:  true,
		})
	}

	for i, grpcProxy := range seed.APIs.GRPC {
		// TODO: add grpc node healthchecks
		seed.APIs.GRPC[i] = grpcProxy.WithStatus(Status{
			CatchingUp: false,
			Reachable:  true,
		})
	}

	return seed, nil
}

func (p Provider) WithStatus(status Status) Provider {
	return Provider{
		Provider: p.Provider,
		Address:  p.Address,
		Status:   status,
	}
}
