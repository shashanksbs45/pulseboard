# Tasks

## 1. Chart skeleton and `helm` check

- [x] 1.1 Install `helm` v3 or v4 locally (`brew install helm`) and confirm `helm version` prints v3.x or v4.x
- [x] 1.2 Create `deploy/helm/pulseboard/` with `Chart.yaml` (`apiVersion: v2`, `version: 0.1.0`, `appVersion: 0.1.0`), `values.yaml` holding every value from design.md "Values surface" with comments, `.helmignore`, and `templates/_helpers.tpl` (name, fullname, labels, selector labels); verify `helm lint deploy/helm/pulseboard` passes
- [x] 1.3 Add `check_helm` to `scripts/check.sh` and to `ALL` after `container`: return 77 when `helm` is not on `PATH`, otherwise run `helm lint` with `--set ingestToken=check` and print the rendered manifest on any failed assertion; verify `scripts/check.sh helm` passes, and reports SKIP with `PATH` stripped of `helm`
- [x] 1.4 Add `helm` to the `scripts/check.sh` check lists in `README.md` and `AGENTS.md` (note it is skipped without `helm`), and add `deploy/helm/` to the repo description in `AGENTS.md`; verify the documented `scripts/check.sh helm` command runs as written

## 2. Ingest token handling

- [x] 2.1 Add the token-source helper to `_helpers.tpl` and `templates/secret.yaml`: existing Secret wins, else the chart creates `<fullname>` with key `ingest-token`, else `fail` with a message naming the ingest token; verify by hand with `helm template` for all three cases
- [x] 2.2 Add `check_helm` assertions: no token → non-zero exit and "ingest token" in the error; `ingestToken` → a Secret renders; `existingSecret=pb-token` plus `ingestToken` → `secret.yaml` renders nothing; verify each assertion fails when its template is temporarily broken

## 3. Workload: Deployment, storage and Service

- [x] 3.1 Add `templates/deployment.yaml`: `replicas: 1`, `strategy.type: Recreate`, the pod and container security context from design.md, `PULSEBOARD_INGEST_TOKEN` from the resolved Secret, `PULSEBOARD_RETENTION` only when `retention` is set, the `checksum/secret` annotation when the chart owns the Secret, the `/data` PVC mount and the `/tmp` emptyDir, readiness and liveness `httpGet /healthz` on port `http` (8080), and `terminationGracePeriodSeconds: 15`; verify `helm lint` passes
- [x] 3.2 Add `templates/pvc.yaml` (`<fullname>-data`, ReadWriteOnce, `persistence.size`, optional `persistence.storageClass`, `helm.sh/resource-policy: keep`) and `templates/service.yaml` (`service.type`, `service.port` → target port `http`); verify `helm lint` passes
- [x] 3.3 Add `check_helm` assertions for the Deployment (`replicas: 1`, `type: Recreate`, `runAsUser: 65532`, `fsGroup: 65532`, `terminationGracePeriodSeconds: 15`, two `/healthz` probes, no `PULSEBOARD_RETENTION` by default and `720h` with `retention=720h`), the PVC keep annotation, and the Service (`NodePort` and port 9000 with those values); verify `scripts/check.sh helm` passes and that changing `Recreate` to `RollingUpdate` makes it fail
- [x] 3.4 Add `templates/NOTES.txt` with the port-forward or `minikube service` command, a `/healthz` curl, and a note that the PVC is kept on uninstall with the `kubectl delete pvc` command; verify it prints after `helm install --dry-run`

## 4. Run on Kubernetes (docs)

- [x] 4.1 Add a "Run on Kubernetes" section to `README.md`: `minikube start`, `docker build -t pulseboard:dev .` and `minikube image load pulseboard:dev`, `helm install pulseboard deploy/helm/pulseboard --set ingestToken=change-me` (and the `existingSecret` alternative with `kubectl create secret generic`), `kubectl port-forward svc/pulseboard 8080:8080`, `kubectl rollout restart` after rebuilding the image, the main values, the kept PVC, and that pulseboard is single-instance by design; verify by following the section verbatim on a fresh minikube and getting 200 from `/healthz`

## 5. Manual cluster verification on minikube

- [x] 5.1 "Install with a token", "Pod runs as non-root and writes its volume" and "Default retention": install with a token; verify one Ready pod, 200 from `/healthz` through the Service, `kubectl exec` is impossible (distroless) so check `kubectl get pod -o jsonpath` for `runAsUser: 65532`, and the startup log shows `retention=168h0m0s`
- [x] 5.2 "Operator chooses the Service type and port" and "Operator sets retention": upgrade with `service.type=NodePort,service.port=9000,retention=720h`; verify the Service is NodePort on 9000 answering 200 (`minikube service --url`), and the new pod's log shows `retention=720h0m0s`
- [x] 5.3 "Upgrade replaces the pod without overlap": while running `kubectl get pods -w`, run the upgrade from 5.2; verify the old pod is Terminating/gone before the new pod is created and exactly one pod is Ready afterwards
- [x] 5.4 Token scenarios: an ingest batch with the value token is accepted (200, `"accepted":1`); reinstall with `existingSecret=pb-token` (holding `from-secret`) plus `ingestToken=from-value`; verify `kubectl get secret -l app.kubernetes.io/instance=pulseboard` lists none from the chart, `from-secret` is accepted and `from-value` gets 401
- [x] 5.5 "No token configured": `helm install` with no token; verify it fails, the error mentions the ingest token, and `kubectl get all,pvc,secret -l app.kubernetes.io/instance=<release>` shows nothing
- [x] 5.6 "Data survives pod deletion": post a `counter` named `jobs_done`, `kubectl delete pod`, wait for the replacement to be Ready; verify a `gauge` named `jobs_done` gets `type_conflict`
- [x] 5.7 "Reinstall reuses stored data": `helm uninstall pulseboard`; verify the `pulseboard-data` PVC still exists; `helm install` again with the same name; verify the install succeeds and a `gauge` named `jobs_done` gets `type_conflict`
- [x] 5.8 Run the full `scripts/check.sh`; verify every check passes or skips as documented, with `helm` reported as PASS
