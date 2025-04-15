# Akash RPC Proxy

A proxy server for Akash RPC nodes that provides load balancing and automatic failover.

## Features

- Load balancing across multiple RPC nodes
- Automatic failover when nodes become unhealthy
- Support for both HTTP and gRPC endpoints
- Automatic TLS certificate management via Let's Encrypt
- Configurable health checks and error rate thresholds

## Running the Proxy

### Basic Usage

```bash
# Run with default settings
go run cmd/main.go
```

### With TLS Certificates

You can run the proxy with TLS certificates in two ways:

1. Using Let's Encrypt (automatic certificate management):
```bash
go run cmd/main.go \
  --autocert-email=your-email@example.com \
  --autocert-hosts=your-domain.com,another-domain.com
```

2. Using your own certificates:
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
go run cmd/main.go --config=./config/local.yaml
```

## Building

```bash
go build -o akash-rpc-proxy
```

## License

MIT
