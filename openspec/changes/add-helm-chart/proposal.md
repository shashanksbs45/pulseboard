# Proposal

## Why

pulseboard runs as a container image, but the only supported way to run it is `docker run`. Running it on Kubernetes (starting with a local minikube cluster) means hand-writing manifests that get the SQLite constraints right: one pod at a time, a persistent volume writable by UID 65532, the ingest token from a Secret, and `/healthz` probes. A Helm chart captures those constraints once so operators can install, upgrade and remove pulseboard with one command.

## What Changes

- A Helm chart at `deploy/helm/pulseboard/` that installs pulseboard as a single-replica Deployment with the `Recreate` strategy, a PersistentVolumeClaim mounted at `/data`, a Service, and (optionally) a Secret holding the ingest token.
- The ingest token comes from either `ingestToken` (the chart creates a Secret) or `existingSecret` (a Secret the operator manages). If both are set, `existingSecret` wins. If neither is set, `helm install` fails with an error that names the ingest token.
- The pod runs as UID/GID 65532 with `fsGroup: 65532` so it can write to the volume, uses `GET /healthz` on port 8080 for readiness and liveness probes, and gets a termination grace period longer than the server's 10-second drain.
- The PVC is kept on `helm uninstall`, so removing the release never deletes metric data.
- Values for the image, retention, Service type and port, storage size and class, and resources.
- A new `helm` check in `scripts/check.sh` runs `helm lint` and asserts the rendered manifests. It is skipped (exit 77) when `helm` is not installed. GitHub's Ubuntu runners include `helm`, so CI runs it.
- `README.md` gains a "Run on Kubernetes" section covering minikube: building the image into the cluster, installing the chart, and reaching the Service.

Out of scope: an Ingress, a StatefulSet, more than one replica, publishing the chart or image to a registry, and a cluster-based smoke test in CI (kind or similar). Cluster behavior is verified by hand on minikube.

## Capabilities

### New Capabilities
<!-- None. Running on Kubernetes is another way the image is deployed, so it belongs in `deployment`. -->

### Modified Capabilities
- `deployment`: adds requirements for installing pulseboard on Kubernetes with the Helm chart: token handling, single-pod storage, non-root pod, health probes, graceful termination, and keeping data across pod replacement and uninstall. The existing container-image requirements do not change.

## Impact

- **Code:** no Go changes. New files under `deploy/helm/pulseboard/`; edits to `scripts/check.sh`, `README.md` and `AGENTS.md`.
- **Dependencies:** `helm` (v3 or v4) for operators and for the `helm` check; `kubectl` and `minikube` for local use. No new Go modules.
- **Systems:** CI runs one more check, which needs `helm` on the runner (preinstalled on `ubuntu-latest`).
- **Operators:** a supported way to run pulseboard on Kubernetes: `helm install pulseboard deploy/helm/pulseboard --set ingestToken=...`.
