# Proxy

Load balancer and proxy for the akash network.

See [config.md](./config.md) for configuration details.

---

## HTTPS Localhost

Easiest way is to use [mkcert](https://github.com/FiloSottile/mkcert).

Create the certificate and key, and set them:

```sh
AKASH_PROXY_TLS_KEY=localhost-key.pem
AKASH_PROXY_TLS_CERT=localhost.pem
```

And start the server.
