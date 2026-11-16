# Proxy test lookup services

Code: `app/proxy_service.go` (`proxyProber`). Setting: `GetProxyLookup` and `SetProxyLookup`. File: `<data folder>/proxy-lookup.json`.

## What a proxy test does

1. It starts a relay to the proxy (`core/proxy`).
2. It sends one GET request to the exit IP URL through the relay. The answer is the exit IP as plain text.
3. It sends one GET request to the location URL through the same relay. `{ip}` in the URL is the exit IP. The answer is JSON with a country code and an IANA timezone.
4. It closes the relay.

Both requests leave through the proxy. The two services see the IP of the proxy. They do not see the real IP of the user.

## Defaults

| Setting | Default | Answer |
|---|---|---|
| `ipUrl` | `https://api.ipify.org` | The IP as plain text |
| `geoUrl` | `https://ipwho.is/{ip}` | JSON with `country_code` and `timezone.id` |

The app does not call any other third-party service. Nothing is sent at start, only when the user tests a proxy, runs "Health Check All" or uses "Timezone and language from proxy".

## Privacy note

- The services get the exit IP of the proxy and the time of the test. They get no profile name, no fingerprint and no credentials.
- The proxy provider sees both requests, as it sees all traffic of the profile.
- To use no public service, point `ipUrl` at a service you run, and clear `geoUrl`. Without `geoUrl`, the test gives the exit IP and the latency, and leaves country, timezone and language empty.
- The location answer is read from these JSON keys: `country_code`, `countryCode` or `country` (two letters), and `timezone` (text or an object with `id` or `name`), `time_zone` or `timezone_id`. A service with other keys gives no location.

## Errors

A test that fails does not stop the app. `ProxyCheckResult.code` is `proxy.auth` if the proxy answered 407, and `proxy.down` for any other failure (no connection, timeout, bad answer of the IP service). The test has a 25 second limit.
