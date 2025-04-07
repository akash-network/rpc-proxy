package seed

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/akash-network/rpc-proxy/internal/block"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Probe is the interface that wraps the Probe method.
//
// Probe probes the given node for health.
// A healthy node must return a healthy status.
// Health is subject to the caller's interpretation of the returned Status.
type Probe interface {
	Probe(ctx context.Context, node Node) (Status, error)
}

// ProbeFunc type is an adapter to allow the use of ordinary functions as probes.
// If a given function f is a function with the appropriate signature, ProbeFunc(f) is a Probe that calls f.
type ProbeFunc func(ctx context.Context, node Node) (Status, error)

// Probe implements the Probe interface for ProbeFunc to allow for defining probes as standalone function
func (f ProbeFunc) Probe(ctx context.Context, node Node) (Status, error) {
	return f(ctx, node)
}

// RPCProbe probes an RPC Node.
// It queries the Node status through RPC and tries to set the latest block height globally.
func RPCProbe(ctx context.Context, node Node) (Status, error) {
	client, err := rpchttp.New(node.Address, "/")
	if err != nil {
		return Status{}, fmt.Errorf("getting RPC client status: %w", err)
	}

	status, err := client.Status(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("getting RPC client status: %w", err)
	}

	errLowBlock := block.GetInstance().SetLatestBlock(status.SyncInfo.LatestBlockHeight)

	return Status{
		CatchingUp:    status.SyncInfo.CatchingUp,
		Reachable:     true,
		IsLatestBlock: errLowBlock == nil,
	}, nil
}

// GRPCProbe probes a gRPC Node.
// It checks if the node is catching up, queries the Node status through gRPC and tries to set the latest block
// height globally.
func GRPCProbe(ctx context.Context, node Node) (Status, error) {
	creds := credentials.NewTLS(&tls.Config{
		InsecureSkipVerify: false,
	})

	conn, err := grpc.NewClient(node.Address, grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1024*1024*10)))
	if err != nil {
		return Status{}, fmt.Errorf("creating gRPC client: %w", err)
	}
	defer conn.Close()

	serviceClient := cmtservice.NewServiceClient(conn)

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	catchingUp, err := serviceClient.GetSyncing(ctx, &cmtservice.GetSyncingRequest{})
	if err != nil {
		return Status{}, fmt.Errorf("getting gRPC client sync: %w", err)
	}

	latestBlock, err := serviceClient.GetLatestBlock(ctx, &cmtservice.GetLatestBlockRequest{})
	if err != nil {
		return Status{}, fmt.Errorf("getting gRPC client latest block: %w", err)
	}

	errLowBlock := block.GetInstance().SetLatestBlock(latestBlock.Block.Header.Height)

	return Status{
		CatchingUp:    catchingUp.Syncing,
		Reachable:     true,
		IsLatestBlock: errLowBlock == nil,
	}, nil
}

// RESTProbe probes a REST Node.
// It checks if the node is catching up querying the REST endpoint, queries the Node latest block and tries to set the
// height globally.
func RESTProbe(ctx context.Context, node Node) (Status, error) {
	type syncInfoResponse struct {
		CatchingUp bool `json:"catching_up"`
	}

	type latestBlockResponse struct {
		Block struct {
			Header struct {
				Height string `json:"height"`
			} `json:"header"`
		} `json:"block"`
	}

	client := &http.Client{}

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/syncing", node.Address), nil)
	if err != nil {
		return Status{}, fmt.Errorf("creating REST client request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Status{}, fmt.Errorf("getting REST client sync: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return Status{}, fmt.Errorf("unexpected status from REST client [%d %s]: %w", resp.StatusCode, resp.Status, err)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Status{}, fmt.Errorf("reading body from REST client response: %w", err)
	}

	var syncing syncInfoResponse
	if err := json.Unmarshal(body, &syncing); err != nil {
		return Status{}, fmt.Errorf("unmarshaling body from REST client response: %w", err)
	}

	latestBlockReq, err := http.NewRequest("GET", fmt.Sprintf("%s/blocks/latest", node.Address), nil)
	if err != nil {
		return Status{}, fmt.Errorf("creating REST client request: %w", err)
	}

	latestBlockResp, err := client.Do(latestBlockReq)
	if err != nil {
		return Status{}, fmt.Errorf("getting REST client sync: %w", err)
	}
	defer latestBlockResp.Body.Close()

	if latestBlockResp.StatusCode != 200 {
		return Status{}, fmt.Errorf("unexpected status from REST client [%d %s]: %w", latestBlockResp.StatusCode, latestBlockResp.Status, err)
	}

	latestBlockBody, err := io.ReadAll(latestBlockResp.Body)
	if err != nil {
		return Status{}, fmt.Errorf("reading body from REST client response: %w", err)
	}

	var latestBlock latestBlockResponse
	if err := json.Unmarshal(latestBlockBody, &latestBlock); err != nil {
		return Status{}, fmt.Errorf("unmarshaling body from REST client response: %w", err)
	}

	height, err := strconv.ParseInt(latestBlock.Block.Header.Height, 10, 64)
	if err != nil {
		return Status{}, fmt.Errorf("parsing block height: %w", err)
	}

	errLowBlock := block.GetInstance().SetLatestBlock(height)

	return Status{
		CatchingUp:    syncing.CatchingUp,
		Reachable:     true,
		IsLatestBlock: errLowBlock == nil,
	}, nil
}
