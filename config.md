# Environment Variables

## Config

 - `AKASH_PROXY_LISTEN` (default: `:https`) - Address to listen to.
 - `AKASH_PROXY_AUTOCERT_EMAIL` - Autocert account email.
 - `AKASH_PROXY_AUTOCERT_HOSTS` (comma-separated) - Autocert domains.
 - `AKASH_PROXY_TLS_CERT` - TLS certificate to use. If empty, will try to use autocert.
 - `AKASH_PROXY_TLS_KEY` - TLS key to use. If empty, will try to use autocert.
 - `AKASH_PROXY_SEED_URL` (default: `https://raw.githubusercontent.com/cosmos/chain-registry/master/akash/chain.json`) - Proxy seed URL to fetch for server updates.
 - `AKASH_PROXY_SEED_REFRESH_INTERVAL` (default: `5m`) - How frequently fetch SEED_URL for updates.
 - `AKASH_PROXY_CHAIN_ID` (default: `akashnet-2`) - Expected chain ID.
 - `AKASH_PROXY_HEALTHY_THRESHOLD` (default: `10s`) - How slow on average a node needs to be to be marked as unhealthy.
 - `AKASH_PROXY_HEALTH_INTERVAL` (default: `5m`) - Check Health on endpoints.
 - `AKASH_PROXY_HEALTHY_ERROR_RATE_THRESHOLD` (default: `30`) - Percentage of request errors deemed acceptable.
 - `AKASH_PROXY_HEALTHY_ERROR_RATE_BUCKET_TIMEOUT` (default: `1m`) - How long in the past requests are considered to check for status codes.
 - `AKASH_PROXY_PROXY_REQUEST_TIMEOUT` (default: `15s`) - Request timeout for a proxied request.
 - `AKASH_PROXY_UNHEALTHY_SERVER_RECOVERY_CHANCE_PERCENT` (default: `1`) - How much chance (in %, 0-100), a node marked as unhealthy have to get a
request again and recover.

