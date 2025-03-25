package seed

import (
	"context"
	"crypto/tls"
	"fmt"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"time"
)

type Probe interface {
	Probe(provider Provider) (Status, error)
}

// ProbeFunc type is an adapter to allow the use of ordinary functions as probes.
// If a given function f is a function with the appropriate signature, ProbeFunc(f) is a Probe that calls f.
type ProbeFunc func(provider Provider) (Status, error)

// Probe implements the Probe interface for ProbeFunc to allow for defining probes as standalone function
func (f ProbeFunc) Probe(provider Provider) (Status, error) {
	return f(provider)
}

func RPCProbe(provider Provider) (Status, error) {
	client, err := rpchttp.New(provider.Address, "/") // TODO: Test this
	if err != nil {
		unreachable := Status{
			CatchingUp: false,
			Reachable:  false,
		}
		return unreachable, fmt.Errorf("getting RPC client status: %w", err)
	}

	status, err := client.Status(context.Background())
	if err != nil {
		unreachable := Status{
			CatchingUp: false,
			Reachable:  false,
		}
		return unreachable, fmt.Errorf("getting RPC client status: %w", err)
	}

	return Status{
		CatchingUp: status.SyncInfo.CatchingUp,
		Reachable:  true,
	}, nil
}

func GRPCProbe(provider Provider) (Status, error) {
	creds := credentials.NewTLS(&tls.Config{
		InsecureSkipVerify: false,
	})

	conn, err := grpc.NewClient(provider.Address, grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(1024*1024*10)))
	if err != nil {
		unreachable := Status{
			CatchingUp: false,
			Reachable:  false,
		}
		return unreachable, fmt.Errorf("creating gRPC client: %w", err)
	}
	defer conn.Close()

	serviceClient := cmtservice.NewServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	catchingUp, err := serviceClient.GetSyncing(ctx, &cmtservice.GetSyncingRequest{})
	if err != nil {
		unreachable := Status{
			CatchingUp: false,
			Reachable:  false,
		}
		return unreachable, fmt.Errorf("getting gRPC client sync: %w", err)
	}

	return Status{
		CatchingUp: catchingUp.Syncing,
		Reachable:  true,
	}, nil

}

func RESTProbe(provider Provider) (Status, error) {
	return Status{
		Reachable:  true,
		CatchingUp: false,
	}, nil
}
