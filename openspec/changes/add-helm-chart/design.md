# Design

## Context

See proposal.md for why. The constraints come from what already exists:

- The image (`Dockerfile`) runs `pulseboard serve` as UID/GID 65532 on distroless, listens on `:8080` (`PULSEBOARD_ADDR`) and keeps SQLite at `/data/pulseboard.db` (`PULSEBOARD_DB`). It has no shell, so there are no exec probes and no init scripts inside the image.
- The server needs `PULSEBOARD_INGEST_TOKEN` and exits non-zero without it. `PULSEBOARD_RETENTION` defaults to `168h`. Startup logs `db=... retention=...`.
- `GET /healthz` returns 200, or 503 when the database is unreachable. On SIGTERM the server drains for up to `server.ShutdownTimeout` (10s) and then closes the database.
- SQLite is one file with a single writer. Two pods on the same volume would corrupt or lock it, and a ReadWriteOnce volume cannot be shared across nodes anyway.
- The image isn't published anywhere. On minikube it is built with `docker build` and copied into the cluster with `minikube image load`. (`minikube image build` does not work here: `.dockerignore` excludes `Dockerfile`, and minikube's containerd BuildKit build reads the Dockerfile from the filtered context.)
- `scripts/check.sh` is the only check entry point. A check returns 77 to report SKIP when a tool is missing. CI runs the script on `ubuntu-latest`, which ships `helm`.

## Goals / Non-Goals

**Goals:**
- `helm install` on a fresh minikube gives a working, persistent pulseboard with only a token supplied.
- The chart's static guarantees (single pod, Recreate, probes, grace period, non-root, PVC kept, token handling) are asserted by `scripts/check.sh helm` on every PR without a cluster.

**Non-Goals:**
- Supporting `replicas > 1`. The value is not exposed.
- Configuring the in-container listen address. The Deployment pins `PULSEBOARD_ADDR` to `:8080` instead of relying on the image's `ENV` default, written as plain literals next to `containerPort: 8080`, with `check_helm` asserting the two agree; probes and the Service follow through the `http` port name. Only the Service port is configurable.
- Chart unit-test plugins (helm-unittest) or schema-validation tools (kubeconform). They are extra pinned dependencies for a chart with five templates.
- A NetworkPolicy, ServiceAccount RBAC, or HPA.

## Decisions

### Chart layout and naming
The chart lives in `deploy/helm/pulseboard/` (`apiVersion: v2`, `version: 0.1.0`, `appVersion: 0.1.0`). Templates: `deployment.yaml`, `service.yaml`, `pvc.yaml`, `secret.yaml`, `NOTES.txt`, plus `_helpers.tpl` for the name, labels and token-source helpers. It uses the standard `app.kubernetes.io/*` labels. Resource names follow the usual `<release>-pulseboard` fullname rule, collapsing to the release name when it already contains `pulseboard`. *Alternative:* `charts/pulseboard/`. `deploy/` leaves room for other deployment assets (Compose, plain manifests) without a top-level directory each.

### Deployment, `replicas: 1`, `strategy: Recreate`
Recreate deletes the old pod before creating the new one, which satisfies "Single pod at a time" and lets the ReadWriteOnce volume detach and reattach cleanly. *Alternative:* a StatefulSet. It also guarantees one pod, but its `volumeClaimTemplates` are hard to resize or rename later, and it adds no value for a single replica. The user chose a Deployment.

### Ingest token: value or existing Secret, existing wins
Values: `ingestToken: ""`, `existingSecret: ""`, `existingSecretKey: ingest-token`. A helper resolves the Secret name and key:
- `existingSecret` set → use it with `existingSecretKey` (an empty key falls back to `ingest-token` instead of rendering `key: ""`, which the pod would only reject at runtime); `secret.yaml` renders nothing.
- else `ingestToken` set → `secret.yaml` renders `<fullname>` with key `ingest-token`.
- else → `fail "pulseboard: set ingestToken or existingSecret (the ingest token is required)"`, so `helm install` and `helm template` both stop before creating anything.

The container gets `PULSEBOARD_INGEST_TOKEN` through `valueFrom.secretKeyRef`. When the chart owns the Secret, the pod template carries a `checksum/secret` annotation, so changing `ingestToken` on upgrade rolls the pod. *Alternative:* failing when both are set. The user chose "existing wins", which lets an operator move from a value to a managed Secret by adding one setting.

### Storage: one PVC per release, kept on uninstall
`pvc.yaml` creates `<fullname>-data` (`ReadWriteOnce`, `persistence.size: 1Gi`, optional `persistence.storageClass`, empty means the cluster default; minikube's is `standard`), annotated `helm.sh/resource-policy: keep`, and mounted at `/data`. Helm leaves the claim behind on uninstall. A later `helm install` with the same release name and namespace adopts it again, because the kept claim still carries matching `meta.helm.sh/release-name`/`release-namespace` annotations. *Alternative:* deleting data with the release. An operator who wants that runs `kubectl delete pvc` deliberately. Data loss should never be the default.

### Pod security
Pod: `runAsNonRoot: true`, `runAsUser: 65532`, `runAsGroup: 65532`, `fsGroup: 65532`, `seccompProfile: RuntimeDefault`, `automountServiceAccountToken: false`. Container: `allowPrivilegeEscalation: false`, `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]`. `fsGroup` is what makes a freshly provisioned volume writable by 65532, the Kubernetes equivalent of the README's `chown` advice for bind mounts. Because the root filesystem is read-only, an `emptyDir` is mounted at `/tmp` for any SQLite temporary files. *Alternative:* leaving the root filesystem writable. Read-only costs one emptyDir and removes a whole class of tampering.

### Probes and termination
Readiness and liveness are both `httpGet /healthz` on the named port `http` (8080). Readiness checks every 5s. Liveness checks every 10s with `failureThreshold: 3`, so one slow check doesn't restart the pod. Startup is fast (open SQLite, listen), so there is no startup probe. `terminationGracePeriodSeconds: 15`, matching the README's `--stop-timeout 15` for Docker, gives the 10s drain room before SIGKILL.

### Values surface
`image.repository: pulseboard`, `image.tag: dev`, `image.pullPolicy: IfNotPresent` (matches `docker build -t pulseboard:dev .` + `minikube image load pulseboard:dev`; a remote registry sets all three); `imagePullSecrets: []` (rendered into the pod spec only when non-empty, for private registries); `retention: ""` (rendered as `PULSEBOARD_RETENTION` only when non-empty, so the server's own default holds otherwise); `service.type: ClusterIP`, `service.port: 8080`; `persistence.*`; `resources: {}`; `podAnnotations: {}`; `nodeSelector`/`tolerations`/`affinity`. Nothing else. `defaultvalues.yaml` next to `values.yaml` is a minikube preset passed with `-f` (`existingSecret: pulseboard`, `retention: 720h`, `service.type: NodePort`, small requests and a 256Mi memory limit); Helm never reads it on its own, so it changes no defaults. Every value must be exercised by a `helm` check assertion or a manual verification step.

### `helm` check in `scripts/check.sh`
`check_helm` returns 77 when `helm` is not on `PATH`. Otherwise it runs `helm lint deploy/helm/pulseboard --set ingestToken=check` and then `helm template` assertions with `grep` on `--show-only` output for each template:
1. no token → non-zero exit, error contains "ingest token";
2. token value → a Secret renders; Deployment has `replicas: 1`, `type: Recreate`, `runAsUser: 65532`, `fsGroup: 65532`, `terminationGracePeriodSeconds: 15`, two `path: /healthz` probes; PVC has `helm.sh/resource-policy: keep`; no `PULSEBOARD_RETENTION` env;
3. `existingSecret=pb-token` plus `ingestToken` → `secret.yaml` renders empty, and the Deployment references `pb-token`;
4. `retention=720h` → `PULSEBOARD_RETENTION` with `720h`;
5. `service.type=NodePort,service.port=9000` → the Service renders both.

`ALL` gains `helm` after `container`. *Alternative:* helm-unittest, which gives structured assertions but adds a pinned plugin install. `grep` on scoped output matches how `check_container` already works.

### Cluster scenarios are verified by hand on minikube
The scenarios that need a running cluster (pod ready, Service answers, token accepted or rejected, data survives deletion and reinstall, no overlap on upgrade) are checked manually with the commands in `README.md` and recorded in tasks.md. The user chose not to add a kind cluster to CI.

## Risks / Trade-offs

- [Cluster behavior regresses without CI noticing, since only rendering is checked] → The render assertions pin every manifest property the cluster behavior depends on. The manual minikube pass is a task, and a kind smoke test can be added later as its own change.
- [The `grep`-based assertions are brittle to YAML formatting changes] → Each assertion is scoped with `--show-only`, and failures print the rendered manifest so they are quick to fix.
- [A kept PVC from an old release surprises an operator who expected a clean slate] → `NOTES.txt` and the README say the claim is kept and give the `kubectl delete pvc` command.
- [`image.tag: dev` with `IfNotPresent` keeps running a stale image after a rebuild on minikube] → The README tells operators to run `kubectl rollout restart deployment/<name>` after `minikube image load`. Registries should use immutable tags.
- [On a node failure, a ReadWriteOnce volume can stay attached to the dead node and block the replacement pod] → Accepted for a single-node minikube setup. The README notes that pulseboard is single-instance by design.

## Migration Plan

Additive only: no existing behavior changes. Rollback is `helm uninstall` (data kept), or deleting `deploy/helm/` and `check_helm`.
