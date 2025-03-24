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

type NodeProbe func(provider Provider) (Status, error)

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
