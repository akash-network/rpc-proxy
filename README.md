# Akash RPC Proxy

A proxy server for Akash RPC, gRPC and REST nodes that provides load balancing and automatic failover.

## Features

- Load balancing across multiple nodes
- Automatic failover when nodes become unhealthy
- Configurable health checks

## Running the Proxy

### Basic Usage

```
Usage:
  akash-proxy [flags]

Flags:
  -c, --config string                           config file (default is $HOME/.akash-proxy/config.yaml)
      --cors.allow-headers string               CORS allowed headers (default "Content-Type, Authorization")
      --cors.allow-methods string               CORS allowed methods (default "GET, POST, PUT, DELETE, OPTIONS")
      --cors.allow-origin string                CORS allowed origin (default "*")
      --health.healthy-threshold duration       Response time threshold for healthy nodes (default 10s)
      --health.proxy-request-timeout duration   Timeout for proxied requests (default 15s)
  -h, --help                                    help for akash-proxy
      --seed.additional-nodes.grpc strings      Comma-separated list of additional gRPC nodes
      --seed.additional-nodes.rest strings      Comma-separated list of additional REST nodes
      --seed.additional-nodes.rpc strings       Comma-separated list of additional RPC nodes
      --seed.chain-id string                    Expected chain ID (default "akashnet-2")
      --seed.refresh-interval duration          How often to refresh node list (default 5m0s)
      --seed.url string                         URL to fetch initial node list (default "https://raw.githubusercontent.com/cosmos/chain-registry/master/akash/chain.json")
      --server.listen string                    Address to listen on for HTTP REST & RPC requests (default ":25567")
      --server.listen-grpc string               Address to listen on for gRPC requests (default ":9090")
      --server.timeouts.idle duration           Server idle timeout (default 10s)
      --server.timeouts.read duration           Server read timeout (default 10s)
      --server.timeouts.write duration          Server write timeout (default 10s)
      --tls.autocert.email string               Email for Let's Encrypt certificates
      --tls.autocert.hosts strings              Comma-separated list of domains for Let's Encrypt
      --tls.cert string                         Path to TLS certificate file
      --tls.key string                          Path to TLS private key file

```

```bash
# Run with default settings
go run cmd/main.go
```

### With TLS Certificates

Using your own certificates:
```bash
# Using localhost certificates (for development)
go run cmd/main.go \
  --tls-cert=./localhost.pem \
  --tls-key=./localhost-key.pem

# Or using environment variables
export AKASH_PROXY_TLS_CERT=localhost.pem
export AKASH_PROXY_TLS_KEY=localhost-key.pem
go run cmd/main.go

# Or using a config file
go run cmd/main.go --config=./testdata/local.yaml
```

## Monitoring

### Deploying locally
You can deploy the monitoring stack locally by running docker compose.
```bash
docker-compose -f deploy/docker-compose.yml up -d
```
This will start a Grafana and Prometheus instance with datasources pre-configured as well as the dashboards provisioned.
Access it on [http://localhost:3000](http://localhost:3000) with the default user and password `admin`.
