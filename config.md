# Environment Variables

## Config

 - `LISTEN` (default: `:https`) - Address to listen to.
 - `AUTOCERT_EMAIL` - Autocert account email.
 - `AUTOCERT_HOSTS` (comma-separated) - Autocert domains.
 - `SEED_URL` (default: `https://raw.githubusercontent.com/cosmos/chain-registry/master/akash/chain.json`) - Proxy seed URL to fetch for server updates.
 - `SEED_REFRESH_INTERVAL` (default: `5m`) - How frequently fetch SEED_URL for updates.
 - `CHAIN_ID` (default: `akashnet-2`) - Expected chain ID.
 - `HEALTHY_THRESHOLD` (default: `1s`) - How slow on average a node needs to be to be marked as unhealthy.
 - `PROXY_REQUEST_TIMEOUT` (default: `5s`) - Request timeout for a proxied request.
 - `UNHEALTHY_SERVER_RECOVERY_CHANCE_PERCENT` (default: `1`) - How much chance (in %, 0-100), a node marked as unhealthy have to get a
request again and recover.

