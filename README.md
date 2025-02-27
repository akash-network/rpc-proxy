# Proxy

Load balancer and proxy for the akash network.

See [config.md](./config.md) for configuration details.

---

## HTTPS Localhost

Easiest way is to use [mkcert](https://github.com/FiloSottile/mkcert).
Make sure to install the untrusted localhost certificate with `mkcert -install`.

Create the certificate and key, and set them:

```sh
mkcert localhost
mkcert -install

export AKASH_PROXY_TLS_KEY=localhost-key.pem
export AKASH_PROXY_TLS_CERT=localhost.pem
```

And start the server by running `go run cmd/main.go`.

## Testing

Test the proxy by running `grpcurl localhost:9090 list`.
