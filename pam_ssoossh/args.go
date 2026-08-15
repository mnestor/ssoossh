//go:build pam

package main

import (
	"strconv"
	"strings"
	"time"
)

// defaultSkewTolerance is used when skew-tolerance is absent or unparseable.
// Symmetric: applied to both ValidAfter and ValidBefore (check 4). Chosen
// together with the server's cert_options.pam.valid_duration — see
// docs/release-phase5-pam-client.md.
const defaultSkewTolerance = 2 * time.Second

// defaultWaitTimeout is used when timeout is absent or unparseable. Bounds
// how long Authenticate blocks on browser approval before giving up — see
// docs/release-phase5-pam-client.md, "Timeouts and cancellation".
const defaultWaitTimeout = 60 * time.Second

type config struct {
	// values to convert
	// not in args or in args with value false = don't log
	// set with no value or true = log to logger
	// set with value of stdout = log to stdout
	debug              string        // debug
	insecureSkipVerify bool          // insecure-skip-verify
	server             string        // server
	trustedCAFile      string        // trusted-ca-file
	skewTolerance      time.Duration // skew-tolerance
	waitTimeout        time.Duration // timeout
}

// parseArgs converts PAM module arguments into a config. Each element of
// args is already one complete argument by the time it gets here: PAM's
// standard way to give an argument spaces is to bracket it in the pam.d
// config line ("key=[a value with spaces]"), per pam.conf(5), and libpam's
// own config-line parser resolves that bracketing — stripping the
// brackets and merging the enclosed text into a single element of args —
// before our module is ever invoked. So no re-splitting or re-joining is
// needed here; a bracketed value's spaces are already intact in the
// element that contains it.
func parseArgs(args []string) config {
	raw := make(map[string]string)
	for _, v := range args {
		// Split only on the first '=' so values may contain '=' as well.
		key, val, hasValue := strings.Cut(v, "=")
		if key == "" {
			continue
		}
		if hasValue {
			raw[key] = val
		} else {
			// flag-style argument (no '='). Treat as boolean true.
			raw[key] = "true"
		}
	}

	cfg := config{
		server:        raw["server"],
		trustedCAFile: raw["trusted-ca-file"],
	}

	// debug is a three-state string, not a bool — see the field comment on
	// config above: absent or "false" means don't log at all, a bare flag
	// or "true" means log to the normal logger, and "stdout" means log to
	// stdout specifically. Anything else is treated the same as "true"
	// (log to the logger) rather than silently dropped.
	switch strings.ToLower(raw["debug"]) {
	case "", "false":
		cfg.debug = ""
	case "stdout":
		cfg.debug = "stdout"
	default:
		cfg.debug = "true"
	}

	if v, ok := raw["insecure-skip-verify"]; ok {
		cfg.insecureSkipVerify, _ = strconv.ParseBool(v) //nolint:errcheck // invalid value leaves insecureSkipVerify at its safe default (false)
	}

	cfg.skewTolerance = defaultSkewTolerance
	if v, ok := raw["skew-tolerance"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.skewTolerance = d
		}
	}

	cfg.waitTimeout = defaultWaitTimeout
	if v, ok := raw["timeout"]; ok {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.waitTimeout = d
		}
	}

	return cfg
}
