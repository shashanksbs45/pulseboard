#!/usr/bin/env bash
# Runs every engineering-quality check and prints a summary.
# Usage: scripts/check.sh            run all checks
#        scripts/check.sh lint test  run only the named checks
# Exits non-zero if any check fails. Keeps going after a failure so you see everything at once.
set -uo pipefail

GOLANGCI_LINT_VERSION=v2.14.0
GOVULNCHECK_VERSION=v1.8.0

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

check_vet()  { go vet ./...; }
check_lint() { golangci_lint run ./...; }
check_test() { go test ./...; }
check_race() { go test -race -count=1 ./...; }
check_vuln() { govulncheck_ ./...; }

# OpenAPI checks are skipped until the repo has a spec (openapi.yaml).
check_openapi_lint() {
	[[ -f openapi.yaml ]] || { echo "no openapi.yaml yet"; return 77; }
	npx --yes @redocly/cli@latest lint openapi.yaml
}
check_openapi_contract() {
	[[ -f openapi.yaml ]] || { echo "no openapi.yaml yet"; return 77; }
	go test -run Contract ./...
}

ALL=(build gofmt vet lint test race vuln docker openapi_lint openapi_contract)
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
