#!/usr/bin/env bash
# Runs every engineering-quality check and prints a summary.
# Usage: scripts/check.sh            run all checks
#        scripts/check.sh lint test  run only the named checks
# Exits non-zero if any check fails. Keeps going after a failure so you see everything at once.
set -uo pipefail

GOLANGCI_LINT_VERSION=v2.14.0
GOVULNCHECK_VERSION=v1.8.0
REDOCLY_CLI_VERSION=2.62.1

cd "$(dirname "$0")/.."

# Prefer an installed binary; otherwise build it on the fly. CGO is off so the
# build does not depend on the local macOS linker/SDK.
golangci_lint() {
	if command -v golangci-lint >/dev/null; then
		golangci-lint "$@"
	else
		CGO_ENABLED=0 go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" "$@"
	fi
}

govulncheck_() {
	if command -v govulncheck >/dev/null; then
		govulncheck "$@"
	else
		go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" "$@"
	fi
}

check_gofmt() {
	local files
	files=$(gofmt -l .)
	if [[ -n "$files" ]]; then
		echo "these files need gofmt -w:"
		echo "$files"
		return 1
	fi
}

check_build() { go build ./...; }

# Builds the container image; skipped when no Docker daemon is reachable.
check_docker() {
	docker info >/dev/null 2>&1 || { echo "docker daemon not reachable"; return 77; }
	docker build -t pulseboard:check .
}

# Runs the deployment scenarios against the built image; skipped when no Docker
# daemon is reachable. Runs in a subshell so its EXIT trap removes every
# container, volume and tag it made, even when a step fails. Names are unique
# per run so parallel runs don't collide.
check_container() (
	set -e
	docker info >/dev/null 2>&1 || { echo "docker daemon not reachable"; exit 77; }

	local id="pulseboard-check-$$-${RANDOM}"
	local image="pulseboard:${id#pulseboard-}"
	local volume="${id}-data"

	cleanup() {
		local status=$?
		# -v: the image's VOLUME /data gives each container without -v an anonymous volume.
		docker ps -aq --filter "label=pulseboard.check=${id}" | xargs docker rm -fv >/dev/null 2>&1 || true
		docker volume rm -f "$volume" >/dev/null 2>&1 || true
		docker image rm -f "$image" >/dev/null 2>&1 || true
		exit "$status"
	}
	trap cleanup EXIT

	# Starts a detached container of the image under test, labelled for cleanup.
	# Usage: run_container <name-suffix> [docker run options...] [-- command...]
	run_container() {
		local name="${id}-$1"
		shift
		local -a opts=()
		while (($#)) && [[ $1 != -- ]]; do
			opts+=("$1")
			shift
		done
		(($#)) && shift
		docker run -d --name "$name" --label "pulseboard.check=${id}" ${opts[@]+"${opts[@]}"} "$image" "$@" >/dev/null
	}

	# Prints a failure with the container's logs and fails the check.
	fail() {
		echo "FAIL: $2"
		echo "--- logs of ${id}-$1:"
		docker logs "${id}-$1" 2>&1 | tail -20 || true
		exit 1
	}

	# Prints the host address published for a container port, e.g. 127.0.0.1:55001.
	host_addr() { docker port "${id}-$1" "$2/tcp" | head -1; }

	# Waits up to 10s for /healthz on a published container port to return 200.
	wait_healthy() {
		local addr i
		addr=$(host_addr "$1" "$2")
		for i in $(seq 50); do
			curl -sf "http://${addr}/healthz" >/dev/null && return 0
			sleep 0.2
		done
		fail "$1" "/healthz on container port $2 did not return 200 within 10s"
	}

	# Waits up to 10s for a container's log to contain a fixed string.
	wait_log() {
		local i
		for i in $(seq 50); do
			docker logs "${id}-$1" 2>&1 | grep -qF -- "$2" && return 0
			sleep 0.2
		done
		fail "$1" "log never contained: $2"
	}

	# Posts a JSON batch to a container's ingest endpoint and prints the response body.
	ingest() {
		curl -s "http://$(host_addr "$1" 8080)/api/v1/ingest" \
			-H "Authorization: Bearer check" -H "Content-Type: application/json" -d "$2"
	}

	docker build -q -t "$image" . >/dev/null
	docker volume create "$volume" >/dev/null

	echo "-- no ingest token"
	run_container notoken
	status=$(docker wait "${id}-notoken")
	[[ $status != 0 ]] || fail notoken "container without a token exited 0"
	docker logs "${id}-notoken" 2>&1 | grep -qi "ingest token" || fail notoken "log does not mention the ingest token"

	echo "-- container starts with defaults"
	run_container main -e PULSEBOARD_INGEST_TOKEN=check -v "${volume}:/data" -p 127.0.0.1::8080
	wait_healthy main 8080
	wait_log main "db=/data/pulseboard.db"

	echo "-- server runs as non-root"
	uids=$(docker top "${id}-main" -eo pid,uid | awk 'NR > 1 { print $2 }' | sort -u)
	[[ $uids == 65532 ]] || fail main "processes run as UID(s) '${uids//$'\n'/,}', want 65532"

	echo "-- idle container stops cleanly"
	body=$(ingest main '[{"name":"jobs_done","type":"counter","value":1}]')
	[[ $body == *'"accepted":1'* ]] || fail main "counter point not accepted: $body"
	docker stop "${id}-main" >/dev/null
	status=$(docker inspect -f '{{.State.ExitCode}}' "${id}-main")
	[[ $status == 0 ]] || fail main "docker stop gave exit status $status, want 0"

	echo "-- data survives container replacement"
	docker rm -v "${id}-main" >/dev/null
	run_container main2 -e PULSEBOARD_INGEST_TOKEN=check -v "${volume}:/data" -p 127.0.0.1::8080
	wait_healthy main2 8080
	# No query API yet: rejecting the same name with the other type proves the
	# first container's point is still stored.
	body=$(ingest main2 '[{"name":"jobs_done","type":"gauge","value":1}]')
	[[ $body == *'"type_conflict"'* ]] || fail main2 "expected type_conflict from the replaced container, got: $body"

	echo "-- operator overrides the listen address"
	run_container addr -e PULSEBOARD_INGEST_TOKEN=check -e PULSEBOARD_ADDR=:9090 -p 127.0.0.1::9090
	wait_healthy addr 9090

	echo "-- operator passes serve flags"
	run_container flags -e PULSEBOARD_INGEST_TOKEN=check -- serve --retention 24h
	wait_log flags "retention=24h0m0s"
)

# Lints the Helm chart and asserts properties of its rendered manifests; skipped
# when helm is not installed. Needs no cluster. Runs in a subshell so set -e
# and the helpers stay local.
check_helm() (
	set -e
	command -v helm >/dev/null || { echo "helm not installed"; exit 77; }

	local chart=deploy/helm/pulseboard

	# Prints a failure, plus the rendered manifest if one is given, and fails the check.
	fail() {
		echo "FAIL: $1"
		if [[ -n ${2-} ]]; then
			echo "--- rendered:"
			echo "$2"
		fi
		exit 1
	}

	# Renders the chart as release "check" with the given --set options.
	render() { helm template check "$chart" "$@" 2>&1; }

	# Prints one template's documents from a full render, e.g. section "$out" secret.yaml.
	# (--show-only errors on a template that renders nothing, so split the output instead.)
	section() { awk -v src="# Source: pulseboard/templates/$2" '/^# Source: /{ on = ($0 == src) } on' <<<"$1"; }

	# Asserts that rendered text contains, or lacks, a fixed string.
	has()   { grep -qF -- "$2" <<<"$1" || fail "$3: missing '$2'" "$1"; }
	lacks() { ! grep -qF -- "$2" <<<"$1" || fail "$3: unexpected '$2'" "$1"; }

	echo "-- lint"
	helm lint "$chart" --set ingestToken=check

	echo "-- no ingest token"
	if out=$(render); then
		fail "chart rendered without an ingest token" "$out"
	fi
	has "$out" "ingest token" "error without a token"

	echo "-- token given as a value"
	out=$(render --set ingestToken=check)
	has "$(section "$out" secret.yaml)" "kind: Secret" "secret.yaml with ingestToken"

	echo "-- existing Secret wins over a token value"
	out=$(render --set ingestToken=check --set existingSecret=pb-token)
	[[ -z $(section "$out" secret.yaml) ]] || fail "secret.yaml rendered although existingSecret is set" "$out"
	has "$(section "$out" deployment.yaml)" "name: pb-token" "deployment.yaml token Secret"

	echo "-- single pod, non-root, probes, grace period"
	out=$(render --set ingestToken=check)
	dep=$(section "$out" deployment.yaml)
	has "$dep" "replicas: 1" "deployment.yaml"
	has "$dep" "type: Recreate" "deployment.yaml"
	has "$dep" "runAsUser: 65532" "deployment.yaml"
	has "$dep" "runAsGroup: 65532" "deployment.yaml"
	has "$dep" "fsGroup: 65532" "deployment.yaml"
	has "$dep" "terminationGracePeriodSeconds: 15" "deployment.yaml"
	[[ $(grep -cF "path: /healthz" <<<"$dep") == 2 ]] || fail "deployment.yaml: want readiness and liveness probes on /healthz" "$dep"
	has "$dep" "containerPort: 8080" "deployment.yaml"
	lacks "$dep" "PULSEBOARD_RETENTION" "deployment.yaml without retention"

	echo "-- data kept on uninstall"
	has "$(section "$out" pvc.yaml)" "helm.sh/resource-policy: keep" "pvc.yaml"

	echo "-- operator sets retention"
	dep=$(section "$(render --set ingestToken=check --set retention=720h)" deployment.yaml)
	has "$dep" "PULSEBOARD_RETENTION" "deployment.yaml with retention"
	has "$dep" '"720h"' "deployment.yaml with retention"

	echo "-- operator chooses the Service type and port"
	svc=$(section "$(render --set ingestToken=check --set service.type=NodePort --set service.port=9000)" service.yaml)
	has "$svc" "type: NodePort" "service.yaml"
	has "$svc" "port: 9000" "service.yaml"
)

check_vet()  { go vet ./...; }
check_lint() { golangci_lint run ./...; }
check_test() { go test ./...; }
check_race() { go test -race -count=1 ./...; }
check_vuln() { govulncheck_ ./...; }

# OpenAPI checks are skipped until the repo has a spec (openapi.yaml).
check_openapi_lint() {
	[[ -f openapi.yaml ]] || { echo "no openapi.yaml yet"; return 77; }
	npx --yes @redocly/cli@${REDOCLY_CLI_VERSION} lint openapi.yaml
}
check_openapi_contract() {
	[[ -f openapi.yaml ]] || { echo "no openapi.yaml yet"; return 77; }
	go test -run Contract ./...
}

ALL=(build gofmt vet lint test race vuln docker container helm openapi_lint openapi_contract)
SELECTED=("${@:-${ALL[@]}}")

declare -a RESULTS
failed=0
for name in "${SELECTED[@]}"; do
	if ! declare -F "check_${name}" >/dev/null; then
		echo "unknown check: ${name} (choose from: ${ALL[*]})" >&2
		exit 2
	fi
	printf '\n\033[1m== %s\033[0m\n' "$name"
	"check_${name}"
	case $? in
		0) RESULTS+=("PASS  ${name}") ;;
		77) RESULTS+=("SKIP  ${name}") ;;
		*) RESULTS+=("FAIL  ${name}"); failed=1 ;;
	esac
done

printf '\n\033[1m== summary\033[0m\n'
printf '%s\n' "${RESULTS[@]}"
exit "$failed"
