---
title: TLS and reverse proxies
description: Terminating TLS in ssoosshd or in front of it, renewing certificates without a restart, and keeping the client's real address.
eyebrow: Server operations
sidebar:
  order: 3
---

Plain HTTP only works for loopback development. Everything else terminates
TLS somewhere: either in `ssoosshd` itself, or in a reverse proxy in front of
it. This page covers both, and the two settings that fail silently until
somebody tries to log in.

## Option A: ssoosshd terminates TLS

```yaml
http:
  public_url: "https://ssh.example.com"
  address: "0.0.0.0"
  port: 443
  tls:
    certificate_file: /etc/ssoossh/tls/server.crt
    private_key_file: /etc/ssoossh/tls/server.key
```

[`http.tls.certificate_file`](/ssoossh/reference/config/http/tls/#certificate_file)
and
[`http.tls.private_key_file`](/ssoossh/reference/config/http/tls/#private_key_file)
are paths, and both must be set -- neither alone is a usable pair. PEM pasted
inline is not accepted, because it could not then be rotated without rewriting
the config file. With no pair configured the server serves plain HTTP, with
HTTP/2 cleartext (h2c) enabled.

Three more keys tune the profile, and all three fail startup on a value
`crypto/tls` rejects rather than logging and carrying on:

| Key | Default | Notes |
| --- | --- | --- |
| [`http.tls.min_version`](/ssoossh/reference/config/http/tls/#min_version) | `TLS1.3` | `TLS1.0`/`TLS1.1` resolve but log a deprecation warning |
| [`http.tls.cipher_suites`](/ssoossh/reference/config/http/tls/#cipher_suites) | empty (Go's defaults) | TLS 1.0-1.2 only; an explicit list must include `TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256` or `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256`, or `net/http`'s HTTP/2 setup refuses to serve |
| [`http.tls.curves`](/ssoossh/reference/config/http/tls/#curves) | empty (Go's defaults) | naming any curve replaces the defaults entirely rather than adding to them |

[`http.hsts`](/ssoossh/reference/config/http/#hsts) ships as
`max-age=31536000; includeSubDomains` and is sent on every response, including
`/healthz` and `/ping`. Browsers ignore it over a connection they see as plain
HTTP, so it is harmless when a proxy in front is the one doing TLS -- set it
empty if that proxy sets its own policy.

### Renewing the certificate

`ssoosshd` re-reads both files on `SIGHUP` and serves the new certificate on
connections accepted after it. Nothing restarts and no connection drops.

```bash
# certbot
certbot renew --deploy-hook 'systemctl reload ssoosshd'
```

```ini
# in the systemd unit
ExecReload=/bin/kill -HUP $MAINPID
```

Where nothing can signal the process, poll instead:

```yaml
http:
  tls:
    reload_interval: 1h
```

[`http.tls.reload_interval`](/ssoossh/reference/config/http/tls/#reload_interval)
exists for the Kubernetes secret mount, which is replaced as a directory
symlink rather than written in place: no signal is available and a filesystem
watch on the paths would never fire. Zero or negative disables the timer,
leaving `SIGHUP` as the only trigger.

A reload that fails is logged at `WARN` and the previous certificate keeps
serving, so catching the files mid-rewrite costs nothing. The paths themselves
are fixed for the process's lifetime; only the contents are re-read.

## Option B: a reverse proxy terminates TLS

The common case. Remove the `tls` block, listen on loopback, and tell
`ssoosshd` two things:

```yaml
http:
  # What browsers reach. The https:// scheme is what marks the deployment
  # as HTTPS, even though this process listens on plain HTTP.
  public_url: "https://ssh.example.com"
  address: "127.0.0.1"
  port: 8080
  # CIDRs of the proxy, trusted to set X-Forwarded-For / X-Forwarded-Proto.
  trusted_proxies: ["127.0.0.1/32"]
```

Minimal nginx, proxying to `ssoosshd` on `127.0.0.1:8080`:

```nginx
server {
    listen 443 ssl;
    server_name ssh.example.com;
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Any proxy works. The requirements, whichever you use:

- Forward the client address in `X-Forwarded-For`, and the original scheme in
  `X-Forwarded-Proto`.
- Pass the `Host` through, or set it to `public_url`'s host. A request
  addressed to any other name is answered `421 Misdirected Request`; the
  health endpoints are exempt so probes can reach the server by IP.
- Do not buffer the response on `GET /api/certs/requests/{id}/events`. That is
  a real `text/event-stream`, held open until the request resolves, and it is
  how a waiting client learns its certificate was signed. The hold can last up
  to [`cert_options.client_timeout`](/ssoossh/reference/config/cert_options/#client_timeout)
  (default 5m), so the proxy's read timeout has to be at least that. In nginx
  that means `proxy_buffering off;` and a `proxy_read_timeout` past the
  timeout; other proxies have their own spelling. A buffering proxy makes
  logins look like they hang and then fail, while the web UI shows the
  approval as done.
- The deployment has no WebSocket endpoints. There is nothing to upgrade.

:::caution
Set
[`http.trusted_proxies`](/ssoossh/reference/config/http/#trusted_proxies) to
the proxy's CIDR, or `X-Forwarded-For` is ignored and **every request shows
the proxy's IP**. The list ships empty, and the router passes it to gin's
`SetTrustedProxies` unconditionally -- gin's own default is to trust every
proxy, so this is a deliberate fail-closed choice rather than an oversight.
:::

That address is not cosmetic. It is the source address recorded on the
request, which means it is:

- the `{{.ClientIP}}` field in key IDs, and therefore in the target host's
  `sshd` auth log;
- what
  [source policy](/ssoossh/operations/certificate-policy/) matches against, so
  with `trusted_proxies` unset every client appears to come from the proxy --
  very likely an internal range, so everyone silently lands in the most
  generous tier. `ssoosshd` warns at startup when a source policy is
  configured and `trusted_proxies` is empty;
- what [`cert_options.console.allowed_networks`](/ssoossh/reference/config/cert_options/console/#allowed_networks)
  gates on, which without `trusted_proxies` either admits everything or
  nothing.

### PROXY protocol

[`http.trusted_proxies`](/ssoossh/reference/config/http/#trusted_proxies) is
for an HTTP-level proxy that sets a header. A TCP-level proxy that prefixes
the connection with a PROXY protocol v1/v2 header is a different mechanism and
a different key:

```yaml
http:
  proxy_protocol: ["10.0.0.0/24"]
```

[`http.proxy_protocol`](/ssoossh/reference/config/http/#proxy_protocol) is
empty by default, which disables PROXY protocol support entirely. Once it is
set, connections from any other source are rejected outright. It is mutually
exclusive with
[`http.unix_socket`](/ssoossh/reference/config/http/#unix_socket): PROXY
protocol is a TCP-connection concept and has nothing to prefix on a Unix
socket.

### Listening on a Unix socket

```yaml
http:
  unix_socket: /run/ssoossh/ssoosshd.sock
```

`address` and `port` are ignored when this is set, and `proxy_protocol` must
be empty.

## Load balancers

Several `ssoosshd serve api` instances can sit behind haproxy, nginx, or a
cloud load balancer. No sticky sessions are required: approvals, certificate
delivery, and web sessions all cross instance boundaries. The full procedure,
including what NATS carries and what happens when it does not, is
[Multi-instance and NATS](/ssoossh/operations/multi-instance/).

Two things to carry over from above: the load balancer is the hop
`trusted_proxies` has to name, and it must not buffer the event stream.

## The two silent failures

Both of these look like nothing until someone tries to log in.

| Symptom | Cause |
| --- | --- |
| the identity provider rejects the redirect URI | [`http.public_url`](/ssoossh/reference/config/http/#public_url) is not what browsers actually reach |
| every audit record, key ID, and policy decision shows the proxy's address | [`http.trusted_proxies`](/ssoossh/reference/config/http/#trusted_proxies) does not name the proxy |

Worked configurations for both TLS options are on
[Server configuration examples](/ssoossh/examples/server-configs/).
