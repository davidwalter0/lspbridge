// Command lspdoctor diagnoses whether the lspbroker daemon is installed,
// running, and reachable at the socket path its real consumers (mcp-ast,
// mcp-agent-editor) will actually dial — the one-command answer to the
// failure mode where the broker is silently unreachable, or reachable but
// not answering real queries, while every liveness signal short of an
// actual query looks fine. See
// docs/design/broker-lifecycle-and-socket-path.org for the full incident
// and design story this tool exists to shortcut.
//
// Usage:
//
//	lspdoctor [-probe] [-probe-timeout DUR] [-timeout DUR] [-enforce]
//
// Read-only, by construction: every check either exec.LookPath's a binary,
// shells out to `systemctl --user is-active` (never enable/start/stop/
// restart), or dials a unix socket and calls broker/status or a real LSP
// read method (documentSymbol, references) against a private fixture file
// under a temp directory. Nothing here writes to a running broker's state,
// starts or stops anything, or touches ~/.config.
//
// Flags use the standard-library flag package, matching cmd/lspbroker: this
// module is deliberately pure-stdlib / zero-external-dependency (see
// README.org), and a support tool living beside the daemon should not be
// the one to break that.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/davidwalter0/lspbridge/pkg/broker"
)

func main() {
	if !run() {
		os.Exit(1)
	}
}

// run executes every check, prints the report, and returns whether the run
// should be treated as successful (always true unless -enforce is set and
// at least one check FAILed). Factored out of main so the reporting logic
// is callable without the process-exit side effect.
func run() bool {
	probe := flag.Bool("probe", true,
		"also run a live end-to-end documentSymbol->references probe (spawns pyright if not already warm)")
	probeTimeout := flag.Duration("probe-timeout", 30*time.Second,
		"timeout for the end-to-end probe (cold pyright start included)")
	dialTimeout := flag.Duration("timeout", 2*time.Second, "per-socket dial+status timeout")
	enforce := flag.Bool("enforce", false, "exit nonzero if any check FAILed")
	flag.Parse()

	results := collectResults(*probe, *probeTimeout, *dialTimeout)

	failed := false
	for _, r := range results {
		fmt.Println(r.String())
		if r.status == statusFail {
			failed = true
		}
	}

	if *enforce && failed {
		return false
	}
	return true
}

// collectResults runs every check in order and returns their results. The
// order matters for a human reading top-to-bottom: binary, then service
// supervision, then the two candidate socket paths, then the diagnosis that
// names a mismatch between them explicitly, then — if asked — whether a
// real query actually answers through the path a consumer would use.
func collectResults(probe bool, probeTimeout, dialTimeout time.Duration) []result {
	results := []result{
		checkBinary(),
		checkService(),
	}

	consumerPath := consumerSocketPath()
	defaultPath := broker.DefaultSocketPath()

	consumerResult, consumerReachable := checkReachability("consumer path", consumerPath, dialTimeout)
	results = append(results, consumerResult)

	defaultReachable := consumerReachable
	if defaultPath != consumerPath {
		var defaultResult result
		defaultResult, defaultReachable = checkReachabilityInfo("XDG_RUNTIME_DIR path", defaultPath, dialTimeout)
		results = append(results, defaultResult)
	}
	results = append(results, checkDivergence(consumerPath, defaultPath, consumerReachable, defaultReachable))

	if probe {
		results = append(results, probeEndToEnd(consumerPath, probeTimeout))
	}

	return results
}
