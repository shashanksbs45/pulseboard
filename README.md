# pulseboard

A simple platform to collect, store, and visualize system and application metrics.

Pulseboard is a single Go binary. Applications push gauge and counter points to it over HTTP, and it stores them in one SQLite file. It is sized for a homelab: about 10 sources and about 1k points/sec. Querying and a dashboard come in later changes.

## Build and run

Requires Go 1.26. The build is pure Go, so no C toolchain is needed.

```sh
go build -o pulseboard ./cmd/pulseboard
PULSEBOARD_INGEST_TOKEN=change-me ./pulseboard serve
```

The server listens on `:8080` and stores data in `./pulseboard.db`. Stop it with Ctrl-C or SIGTERM. In-flight requests get up to 10 seconds to finish, and then the database is closed.

Check that it's healthy (no token needed). It returns 200 when the database is reachable and 503 otherwise:

```sh
curl -i http://localhost:8080/healthz
```

## Run with Docker

The image runs `pulseboard serve` as the unprivileged user 65532, listens on port 8080 and keeps its database at `/data/pulseboard.db`. Mount a volume on `/data` so the data outlives the container:

```sh
docker build -t pulseboard .
docker run -d --name pulseboard \
  -e PULSEBOARD_INGEST_TOKEN=change-me \
  -p 8080:8080 \
  -v pulseboard-data:/data \
  --stop-timeout 15 \
  pulseboard
curl -i http://localhost:8080/healthz
```

- **Settings:** the environment variables and flags from [Configuration](#configuration) work unchanged, e.g. `-e PULSEBOARD_RETENTION=720h`, or flags after the image name: `pulseboard serve --retention 720h`.
- **Bind mounts:** a host directory must be writable by UID 65532, or the container can't create the database and exits at startup. Run `sudo chown 65532:65532 /path/to/dir` first. A named volume, as above, needs nothing.
- **Stopping:** the server drains in-flight requests for up to 10 seconds, which is also Docker's default stop timeout, so a slow request could be killed before the database closes. `--stop-timeout 15` (or `stop_grace_period: 15s` in Compose) gives it room. `docker stop pulseboard` then exits with status 0.
- **Health checks:** the image has no `HEALTHCHECK`, because the distroless base has no shell or HTTP client. Probe `GET /healthz` from outside instead, for example a Kubernetes `httpGet` liveness and readiness probe on port 8080. It returns 503 when the database is unreachable.

## Run on Kubernetes

The Helm chart in `deploy/helm/pulseboard` (Helm 3 or 4) runs pulseboard as a single pod with its database on a PersistentVolumeClaim. On minikube, build the image locally and load it into the cluster, so no registry is needed:

```sh
minikube start
docker build -t pulseboard:dev .
minikube image load pulseboard:dev
helm install pulseboard deploy/helm/pulseboard --set ingestToken=change-me
kubectl rollout status deployment/pulseboard
kubectl port-forward svc/pulseboard 8080:8080 &
curl -i http://localhost:8080/healthz
```

To keep the token out of Helm's release history, create the Secret yourself and point the chart at it. An existing Secret always wins over `ingestToken`:

```sh
kubectl create secret generic pb-token --from-literal=ingest-token=change-me
helm install pulseboard deploy/helm/pulseboard --set existingSecret=pb-token
```

The chart fails to install if neither `ingestToken` nor `existingSecret` is set. Other values (see `values.yaml`):

| Value | Default | Meaning |
|---|---|---|
| `image.repository` / `image.tag` / `image.pullPolicy` | `pulseboard` / `dev` / `IfNotPresent` | Image to run |
| `existingSecretKey` | `ingest-token` | Key holding the token in `existingSecret` |
| `retention` | *(server default, 168h)* | `PULSEBOARD_RETENTION` |
| `service.type` / `service.port` | `ClusterIP` / `8080` | How the server is exposed. With `NodePort`, use `minikube service pulseboard --url` |
| `persistence.size` / `persistence.storageClass` | `1Gi` / cluster default | The data volume |
| `resources`, `podAnnotations`, `nodeSelector`, `tolerations`, `affinity` | empty | Usual pod settings |

- **Single instance:** SQLite has one writer, so the chart always runs exactly one pod and replaces it with the `Recreate` strategy: the old pod stops before the new one starts, so an upgrade has a short gap. The volume is ReadWriteOnce, so if a node dies the pod may wait for the volume to detach.
- **Rebuilding the image:** with the `dev` tag and `IfNotPresent`, a rebuilt image isn't picked up on its own. Run `kubectl rollout restart deployment/pulseboard` after `minikube image load`.
- **Data on uninstall:** `helm uninstall pulseboard` keeps the `pulseboard-data` claim, and installing again with the same name and namespace reuses it. To delete the data, run `kubectl delete pvc pulseboard-data`.
- **Stopping:** the pod has a 15-second termination grace period, longer than the server's 10-second drain, so it shuts down cleanly.

## Configuration

Each setting can be passed as a flag or an environment variable. If both are set, the flag wins.

| Flag | Environment variable | Default | Meaning |
|---|---|---|---|
| `--addr` | `PULSEBOARD_ADDR` | `:8080` | Listen address |
| `--db` | `PULSEBOARD_DB` | `./pulseboard.db` | SQLite database path |
| `--ingest-token` | `PULSEBOARD_INGEST_TOKEN` | *(required)* | Bearer token for the ingest endpoint |
| `--retention` | `PULSEBOARD_RETENTION` | `168h` | How long raw points are kept (a Go duration, must be positive) |

`pulseboard serve` exits non-zero with a message if the token is empty, the retention isn't a positive duration, or the database can't be opened.

Points older than the retention period are deleted at startup and then every hour.

## Pushing metrics

`POST /api/v1/ingest` takes a JSON array of points:

```sh
curl -s http://localhost:8080/api/v1/ingest \
  -H "Authorization: Bearer change-me" \
  -H "Content-Type: application/json" \
  -d '[
        {"name":"queue_depth","type":"gauge","value":42,"labels":{"host":"pi-1"}},
        {"name":"jobs_done","type":"counter","value":1234,"labels":{"queue":"emails"}},
        {"name":"2xx-rate","type":"gauge","value":0.98}
      ]'
```

```json
{"accepted":2,"rejected":[{"index":2,"reason":"invalid_name"}]}
```

Each point has these fields:

| Field | Required | Rules |
|---|---|---|
| `name` | yes | Matches `^[a-zA-Z_][a-zA-Z0-9_]*$`, at most 200 characters |
| `type` | yes | `"gauge"` or `"counter"`. A name keeps the type it was first stored with |
| `value` | yes | A number. Counters must not be negative |
| `labels` | no | An object of string to string with at most 10 labels. Keys follow the name rule, and values are 1–128 characters |
| `ts` | no | Unix time in **milliseconds**. Defaults to the time the server received the request. Must be no more than 10 minutes ahead and no older than the retention period |

A series is a name plus its exact label set (label order doesn't matter). Pulseboard holds at most 10,000 active series. A point sent with the same series and `ts` as a stored point replaces its value.

### Responses

| Status | Meaning |
|---|---|
| `200` | Every point was accepted |
| `207` | Some points were accepted and some rejected |
| `422` | Every point was rejected |
| `400` | The body isn't a JSON array of objects, or the array is empty. Nothing is stored |
| `401` | The bearer token is missing or wrong. Nothing is stored |
| `413` | The batch has more than 5,000 points or the body is over 5 MiB. Nothing is stored |

For 200, 207 and 422 the body is `{"accepted":<n>,"rejected":[{"index":<i>,"reason":"<code>"}]}`, where `index` is the point's zero-based position in the batch. The reason codes are:

| Reason | Cause |
|---|---|
| `invalid_name` | The name is missing, malformed or too long |
| `invalid_type` | The type isn't `gauge` or `counter` |
| `invalid_value` | The value is missing or not a number, or a counter is negative |
| `invalid_label` | `labels` isn't an object of strings, or a key or value breaks the rules |
| `too_many_labels` | The point has more than 10 labels |
| `timestamp_out_of_range` | `ts` is more than 10 minutes ahead, older than the retention period, or not an integer |
| `type_conflict` | The name is already stored, or earlier in the same batch, with the other type |
| `series_limit_exceeded` | The point would create a series beyond the 10,000 limit |

### Batch your points

Every request is one durable (fsynced) SQLite transaction, so many tiny requests are much slower than a few large ones. Buffer points on the client and send them in batches, for example every few seconds or every few hundred points, up to 5,000 per request.

## Development

```sh
go build ./...
go vet ./...
go test -race ./...
```

To run every quality check (go build, gofmt, go vet, golangci-lint, tests, race tests, govulncheck, the Docker image build and container smoke test when Docker is running, the Helm chart lint and render checks when `helm` is installed, and the OpenAPI checks once a spec exists) and get a pass/fail summary:

```sh
scripts/check.sh            # all checks
scripts/check.sh lint race  # only some
```

It exits non-zero if any check fails. golangci-lint and govulncheck use your installed binaries if present, and otherwise run pinned versions with `go run`.

### End-to-end check

This starts the built binary on a temporary database, pushes a mixed batch, stops the server with SIGTERM, restarts it, and confirms that the accepted points survived:

```sh
go build -o pulseboard ./cmd/pulseboard
DB=$(mktemp -d)/e2e.db
export PULSEBOARD_INGEST_TOKEN=e2e PULSEBOARD_ADDR=127.0.0.1:18080 PULSEBOARD_DB=$DB

./pulseboard serve & PID=$!
until curl -sf http://127.0.0.1:18080/healthz >/dev/null; do sleep 0.2; done

curl -s -w ' %{http_code}\n' http://127.0.0.1:18080/api/v1/ingest \
  -H "Authorization: Bearer e2e" \
  -d '[{"name":"e2e_gauge","type":"gauge","value":1.5,"labels":{"run":"a"}},
       {"name":"bad-name","type":"gauge","value":1},
       {"name":"e2e_counter","type":"counter","value":3}]'
# {"accepted":2,"rejected":[{"index":1,"reason":"invalid_name"}]} 207

kill -TERM $PID; wait $PID; echo "exit status $?"   # exit status 0

./pulseboard serve & PID=$!
until curl -sf http://127.0.0.1:18080/healthz >/dev/null; do sleep 0.2; done
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:18080/healthz   # 200
sqlite3 "$DB" "SELECT m.name, m.type, s.labels, p.value FROM points p
               JOIN series s ON s.id = p.series_id JOIN metrics m ON m.name = s.metric
               ORDER BY m.name;"
# e2e_counter|counter|{}|3.0
# e2e_gauge|gauge|{"run":"a"}|1.5
kill -TERM $PID; wait $PID
```
