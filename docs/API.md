# REST API (`log-analyzer serve`)

`log-analyzer serve` runs the investigator as a small web service, so a script, a dashboard or
Prometheus Alertmanager can ask for a report. The status is the same as the rest of the tool: early
release, not yet tried on a production cluster.

## First try (two terminals)

Start the server:

```
log-analyzer serve
```

In a second terminal, ask it for a report. These are the same flags as a local run, plus `--server`:

<!-- quickstart -->
```
log-analyzer --server 127.0.0.1:8080 --namespace shop
```
<!-- /quickstart -->

`--service`, `--symptom`, `--since` and `--output` work too. The report is printed on the screen. If the
server needs a token, add `--token-file FILE`. To avoid typing `--server` every time, set
`LOG_ANALYZER_SERVER=127.0.0.1:8080` once (the command always says which server it is asking).

With no API key on the server, the built-in rules answer and nothing leaves your computer. Add
`--api-key-file FILE` to `log-analyzer serve` to use an AI model (same options as the command line:
`--provider`, `--model`, `--base-url`). Open `http://127.0.0.1:8080/` in a browser to see these
instructions again.

## From a script or another program

The command above is a small client. Anything else can make the same call over HTTP; the report comes
back in the same call:

<!-- curl -->
```
curl -X POST "http://127.0.0.1:8080/v1/investigations?wait=true" -d '{"namespace":"shop"}'
```
<!-- /curl -->

## Calls

| Call | What it does |
|---|---|
| `POST /v1/investigations` | Start one. Body: `{"namespace":"shop"}`. Optional: `service`, `symptom`, `since` (for example `30m`). Add `?wait=true` to get the report in the same call. Without it you get `202` and an `id`. |
| `GET /v1/investigations/{id}` | The report. `202` while it is still running. Add `?format=md` for text to paste into a chat or a ticket. |
| `GET /v1/investigations` | The 50 most recent ones, newest first. |
| `POST /v1/alertmanager` | Alertmanager webhook (see below). |
| `GET /healthz` | Health check. Never needs a token. |

Only `namespace` is required. A missing `symptom` becomes a neutral "Something is wrong". A missing
`since` becomes 30 minutes.

## Answers

| Code | Meaning |
|---|---|
| 200 | The report (JSON, or text with `?format=md`). `status` is `done`, or `partial` if it ran out of rounds. |
| 202 | Accepted, or still running. Ask again after `Retry-After` seconds, or use `?wait=true`. |
| 400 / 413 | The request is wrong. The `hint` field says what to send. |
| 401 | The token is missing or wrong. |
| 403 | The namespace is not allowed (`--namespaces`), or a browser request while no token is set. |
| 404 | Unknown or expired id. Finished reports are kept for one hour (`--result-ttl`). |
| 429 | Too many are waiting. See `Retry-After`. |
| 500 | The investigation failed (a tool or the model). `error` says why. |
| 504 | It took longer than `--timeout`. |

The JSON report has these fields: `id`, `status`, `namespace`, `service`, `symptom`, `category`,
`confidence`, `summary`, `evidence` (each with `tool`, `line`, `quoted`, `verified`), `evidence_check`,
`ruled_out`, `recommendations`, `next_command`, `notes`, `model_claim` (only when the check overruled
the model), `tool_calls`, `tools`, `duration_s`. Every quoted evidence line is checked against what the
tools returned; see `internal/report`.

## One at a time

One investigation runs at a time, because each one reads your cluster and costs model credits. Up to
`--queue` more wait in line (default 5); beyond that you get `429` with `Retry-After`.

## Access

- **On this computer only (default).** It listens on `127.0.0.1:8080` and needs no token. Requests that
  a web page in your browser sends are refused, and so are requests addressed to another host name.
- **Open to the network.** `--listen :8080` needs a token, or it refuses to start:

  ```
  openssl rand -hex 24 > token && chmod 600 token
  log-analyzer serve --listen :8080 --token-file token
  ```

  Clients send `Authorization: Bearer <token>`. The token is also read from `LOG_ANALYZER_TOKEN_FILE` or
  `LOG_ANALYZER_TOKEN`. It must be at least 16 characters.
- **Which namespaces.** By default any namespace the Kubernetes account can read. The account's
  permissions are the real limit. `--namespaces shop,payments` narrows it further.

The server speaks plain HTTP. If you open it to the network, put TLS in front of it (an ingress or a
reverse proxy). Per-user permissions, single sign-on and an audit trail are deliberately later; see
[ROADMAP.md](ROADMAP.md).

## In a cluster (Helm)

`helm install la deploy/helm/log-analyzer --set server.enabled=true --set 'server.namespaces={shop}'` runs
the server as a Deployment with a Service (port 8080). It always needs a token: the chart creates one in the
Secret `la-token` (or use your own with `server.tokenSecret`). Read it with
`kubectl get secret la-token -o jsonpath='{.data.token}' | base64 -d`. The server speaks plain http, so put
TLS in front of it (your ingress) before anything outside the cluster talks to it. Inside the cluster the
address is `http://la:8080`. `make helm-k3d` tests this on a throwaway cluster.

## Alertmanager

Point a webhook at `/v1/alertmanager`. Each firing alert with a `namespace` label starts one
investigation (the `service`, `app` or `deployment` label narrows it, and the alert's summary becomes the
symptom). Resolved alerts are ignored. The same alert for the same service is not investigated again
within 30 minutes. If the queue is full you get `429`, and Alertmanager retries.

```yaml
receivers:
  - name: log-analyzer
    webhook_configs:
      - url: http://log-analyzer:8080/v1/alertmanager
        send_resolved: false
        http_config:
          authorization:
            type: Bearer
            credentials_file: /etc/alertmanager/log-analyzer-token
route:
  routes:
    - receiver: log-analyzer
      matchers: [ severity="critical" ]
      continue: true
```

The reports are fetched with `GET /v1/investigations`. Pushing them to Slack or a ticket is planned.

## Limits

- Reports are kept in memory for `--result-ttl` and are lost when the server restarts.
- The symptom is cleaned (control characters removed, 500 characters at most) and then given to the model
  as written. Treat alert text as untrusted, like the logs.
- Not yet run against a production cluster.
