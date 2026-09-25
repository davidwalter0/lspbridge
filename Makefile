MODULE  := github.com/davidwalter0/lspbridge
COV_OUT := coverage.out

.PHONY: all build install lint vet vuln vuln-detect vuln-reach test cov cov-html clean check tidy \
        install-service uninstall-service doctor

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
	@# --no-ignore is required from inside a worktree (the primary's
	@# .gitignore excludes .worktree/, so a bare -r finds nothing: exit 128)
	@# and wrong from the primary checkout, where it descends into every
	@# sibling worktree and reports their stale branches. Measured in
	@# go-version: 15 of 15 findings came from .worktree/*/go.mod while the
	@# repo's own go.mod was clean. --experimental-exclude .worktree does
	@# NOT fix it -- from inside a worktree that pattern matches the scan
	@# root itself. Presence of a .worktree subdir is the discriminator.
	@if [ -d .worktree ]; then \
		echo 'osv-scanner scan source -r .   # primary checkout'; \
		osv-scanner scan source -r .; \
	 else \
		echo 'osv-scanner scan source -r . --no-ignore   # worktree'; \
		osv-scanner scan source -r . --no-ignore; \
	 fi

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
	rm -rf bin

# ---- lspbroker systemd --user install -------------------------------------
#
# lspbroker is a daemon: it owns warm LSP sessions shared by mcp-ast (read)
# and mcp-agent-editor (write) over a unix socket (see pkg/broker's package
# doc and README.org). Nothing auto-spawns it — see mcp-ast's
# pkg/semantic/conn.go doc comment for why not — so it needs an owner
# outside those callers. This section gives it one: a systemd --user unit,
# materialized here and activated by the user. Like mcp-gql-kit's
# install-service (the precedent this mirrors: packaging/mgk.service), it
# only WRITES files and PRINTS instructions; it never runs systemctl.
# Full story, including why the socket path below is NOT lspbroker's own
# default: docs/design/broker-lifecycle-and-socket-path.org.
#
# Every path is overridable, which is also how install-service is verified
# without touching the real ~/.config:
#   make install-service GOBIN=/tmp/x/bin XDG_CONFIG_HOME=/tmp/x/config

# Where the lspbroker binary lands. Defaults to `go env GOPATH`/bin (go
# install's own default).
GOBIN            ?= $(shell go env GOPATH)/bin
XDG_CONFIG_HOME  ?= $(HOME)/.config
SYSTEMD_USER_DIR ?= $(XDG_CONFIG_HOME)/systemd/user
LSPBROKER_EXEC   ?= $(GOBIN)/lspbroker

# The socket path lspbroker's real consumers (mcp-ast, mcp-agent-editor)
# will dial. RE-MEASURED 2026-09-25, superseding the 2026-08-12 note this
# replaces (kept below as history, not current fact): reading
# /proc/<pid>/environ live for both consumers (spawned by the mgk MCP
# gateway) now shows XDG_RUNTIME_DIR=/run/user/<uid> present in BOTH —
# mgk's spawn environment changed since the original measurement, which
# found neither XDG_RUNTIME_DIR nor TMPDIR present at all. TMPDIR is still
# absent from both consumers' environments today, but that no longer
# matters: pkg/broker.DefaultSocketPath() checks XDG_RUNTIME_DIR first and
# returns from that branch whenever it is non-empty, so TMPDIR's value (or
# absence) can't change the answer once XDG_RUNTIME_DIR is set.
#
# So the consumer now resolves $XDG_RUNTIME_DIR/lspbridge/broker.sock, and
# a systemd --user unit's ExecStart can name that value WITHOUT shell
# arithmetic, via the specifier systemd itself expands at unit-parse time:
# `%t` is "$XDG_RUNTIME_DIR ... for user managers" (systemd.unit(5), the
# Specifiers table) — the exact value systemd guarantees for every unit it
# spawns, uid-agnostic and with no `id -u` substitution needed. That is a
# stronger match than re-deriving the value in Make: it can never disagree
# with the systemd manager's own idea of the runtime directory, on this
# host or any other. `make doctor` re-derives the CONSUMER's value from the
# real Go function (cmd/lspdoctor's consumerSocketPath, which forces
# XDG_RUNTIME_DIR to the same uid-derived path before calling
# pkg/broker.DefaultSocketPath) on every run, so it will flag this line if
# the two ever disagree again — that is the authoritative, self-reverifying
# check; this Makefile line is a fast, static mirror of it, not the other
# way around. Full trace: docs/design/broker-lifecycle-and-socket-path.org.
#
# SUPERSEDED 2026-08-12 REASONING (history, not current — kept per the
# fleet's claims-decay convention rather than deleted):
# At that time both consumers were verified to carry NEITHER XDG_RUNTIME_DIR
# NOR TMPDIR (five env vars total, PATH among them, neither of these two),
# so this line pinned a literal "/tmp" — deliberately NOT
# "$${TMPDIR:-/tmp}", because a first draft using that formula evaluated
# $TMPDIR in the INSTALLING shell's own environment (TMPDIR=/tmp/user/1001,
# sandbox-assigned on this host) rather than the consumer's, and would have
# computed /tmp/user/1001/lspbridge-<uid>/... — a path the real, TMPDIR-less
# consumer of THAT era never dialed. Shell arithmetic over the INSTALLING
# shell's own environment was the wrong instrument then for the same reason
# it would be wrong now if applied to XDG_RUNTIME_DIR: it answers "what does
# the operator's shell resolve", not "what does the consumer resolve", and
# installing from a session that itself lacks XDG_RUNTIME_DIR (a bare SSH
# login, say) would compute a divergent path all over again. `%t` sidesteps
# that class of bug entirely: it is expanded by systemd from the ACTUAL user
# manager's runtime directory, never from the installing shell's.
LSPBROKER_SOCKET ?= %t/lspbridge/broker.sock

# The PATH lspbroker needs to find the language servers it spawns
# (pyright-langserver, typescript-language-server, rust-analyzer, dart,
# ngserver). Captured from the INSTALLING shell rather than invented: a
# systemd --user unit's own default PATH is a minimal system one that does
# not include ~/.local/bin or ~/.cargo/bin (measured 2026-08-10). Run
# `make install-service` from a shell that can already find these tools —
# the same shell you'd run `lspbroker` from by hand.
LSPBROKER_PATH   ?= $(PATH)

bin/lspbroker: cmd/lspbroker/main.go pkg/broker/*.go pkg/lsp/*.go pkg/jsonrpc/*.go pkg/server/*.go pkg/projectcontext/*.go
	@mkdir -p bin
	go build -o bin/lspbroker ./cmd/lspbroker

## install-service: build lspbroker, install it to GOBIN, write the systemd
## --user unit from packaging/lspbridge.service (placeholders substituted),
## and print the enable/logs/doctor instructions. Never runs systemctl.
install-service: bin/lspbroker
	@mkdir -p $(GOBIN)
	install -m 0755 bin/lspbroker $(GOBIN)/lspbroker
	@mkdir -p $(SYSTEMD_USER_DIR)
	@sed -e 's|__EXEC__|$(LSPBROKER_EXEC)|g' \
	     -e 's|__SOCKET__|$(LSPBROKER_SOCKET)|g' \
	     -e 's|__PATH__|$(LSPBROKER_PATH)|g' \
	     packaging/lspbridge.service > $(SYSTEMD_USER_DIR)/lspbridge.service
	@echo
	@echo "Installed:"
	@echo "  binary : $(GOBIN)/lspbroker"
	@echo "  unit   : $(SYSTEMD_USER_DIR)/lspbridge.service"
	@echo "  socket : $(LSPBROKER_SOCKET)"
	@echo "           (the path mcp-ast/ae will dial — see the unit's own comments for why;"
	@echo "            the literal %t is expanded by systemd ITSELF at unit load to"
	@echo "            \$$XDG_RUNTIME_DIR — run \`make doctor\` for the expanded value)"
	@echo
	@echo "Next steps (nothing above was started/enabled — run these yourself):"
	@echo "  1. systemctl --user daemon-reload && systemctl --user enable --now lspbridge.service"
	@echo "  2. journalctl --user-unit=lspbridge -f"
	@echo "  3. make doctor            # confirm the socket path actually matches, and that a live query works"
	@echo "  4. Full story: docs/design/broker-lifecycle-and-socket-path.org"

## uninstall-service: PRINT the commands to stop, disable, and remove the
## systemd --user unit. Never touches running service state itself.
uninstall-service:
	@echo "To remove lspbridge.service, run:"
	@echo "  systemctl --user disable --now lspbridge.service"
	@echo "  rm -f $(SYSTEMD_USER_DIR)/lspbridge.service"
	@echo "  systemctl --user daemon-reload"

# DOCTOR_ARGS lets a caller pass flags through, e.g.
#   make doctor DOCTOR_ARGS=-probe=false     # skip the live pyright probe
#   make doctor DOCTOR_ARGS=-enforce         # nonzero exit if anything failed
DOCTOR_ARGS ?=

bin/lspdoctor: cmd/lspdoctor/main.go pkg/broker/*.go pkg/lsp/*.go pkg/jsonrpc/*.go
	@mkdir -p bin
	go build -o bin/lspdoctor ./cmd/lspdoctor

## doctor: diagnose the exact outage class this daemon is prone to — binary
## installed?, service running?, which socket path is the daemon actually
## listening on?, which path would a consumer with a filtered/no
## XDG_RUNTIME_DIR environment resolve?, do the two match?, and does a real
## query through that path actually answer (not just "is the socket up")?
## One command: the whole point is that this used to take a multi-step
## investigation while every symptom looked like "capability absent" instead
## of "daemon unreachable" or "daemon reachable but not answering". Read-only
## — dials, calls broker/status and a live documentSymbol/references probe,
## never writes, never touches a running broker's process.
doctor: bin/lspdoctor
	@./bin/lspdoctor $(DOCTOR_ARGS)
