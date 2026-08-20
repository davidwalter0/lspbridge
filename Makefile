MODULE  := github.com/davidwalter0/lspbridge
COV_OUT := coverage.out

.PHONY: all build install lint vet vuln vuln-detect vuln-reach test cov cov-html clean check tidy

all: build

build:
	go build ./...

install:
	go install ./...

tidy:
	go mod tidy

lint:
	golangci-lint run ./...

vet:
	go vet ./...

OSV_SCANNER_VERSION := v2.3.8

# Vulnerability scanning is TWO layers and they answer different questions:
#
#   detection    "a vulnerable version is present"     -> osv-scanner, EVERY
#                                                          ecosystem
#   reachability "the vulnerable symbol is reached"    -> govulncheck (Go only)
#
# They routinely disagree, and the disagreement is informative rather than a
# contradiction: osv-scanner reads the `go` DIRECTIVE in go.mod as the stdlib
# version, while govulncheck resolves against the toolchain in PATH. A stale
# directive therefore shows up in detection and not in reachability, which is
# a real finding about the directive.
#
# --no-ignore is load-bearing: substantive work happens under .worktree/,
# which the primary checkout's .gitignore excludes and osv-scanner honours, so
# a bare `-r .` finds no packages there and exits 128. Never add
# --allow-no-lockfiles; that is the flag that would turn 128 into a vacuous 0.
# Do NOT add `--experimental-exclude .worktree` either: run from inside a
# worktree, that pattern matches the scan root itself and the walk returns 0
# packages. A missing scanner (127) and a real finding (1) are different
# conditions and must not share a branch, hence the guard below.
vuln: vuln-detect vuln-reach

vuln-detect:
	@command -v osv-scanner >/dev/null 2>&1 || { \
		echo "osv-scanner not installed. Install the pinned version:"; \
		echo "  go install github.com/google/osv-scanner/v2/cmd/osv-scanner@$(OSV_SCANNER_VERSION)"; \
		exit 127; }
	osv-scanner scan source -r . --no-ignore

vuln-reach:
	govulncheck ./...

test:
	go test ./...

check: lint vet vuln test cov

cov:
	@go test -coverprofile=$(COV_OUT) ./pkg/... 2>&1 | \
		grep -E 'coverage:|no test files' | \
		sed 's|.*$(MODULE)/||; s/\t/ /g' | \
		awk '{ pkg=$$1; cov="0.0%"; for(i=1;i<=NF;i++) if($$i ~ /%/) cov=$$i; printf "%-45s %s\n", pkg, cov }' | \
		sort | \
		(echo ""; printf "%-45s %s\n" "Package" "Coverage"; \
		 printf "%-45s %s\n" "─────────────────────────────────────────────" "────────"; \
		 cat; \
		 printf "%-45s %s\n" "─────────────────────────────────────────────" "────────"; \
		 printf "%-45s %s\n" "TOTAL" "$$(go tool cover -func=$(COV_OUT) | tail -1 | awk '{print $$NF}')")

cov-html:
	go test -coverprofile=$(COV_OUT) ./pkg/...
	go tool cover -html=$(COV_OUT)

clean:
	rm -f $(COV_OUT)
