//go:build pam

package main

import (
	"strconv"
	"strings"
)

type config struct {
	// values to convert
	// not in args or in args with value false = don't log
	// set with no value or true = log to logger
	// set with value of stdout = log to stdout
	debug              string // debug
	insecureSkipVerify bool   // insecure-skip-verify
	server             string // server
	trustedCAFile      string // trusted-ca-file
}

// parseArgs converts PAM module arguments into a config. Each element of
// args arrives as one whitespace-separated token — the config-line
// splitter that produced them doesn't understand quoting, so a quoted
// value like `key="a value with spaces"` is already shredded into several
// tokens (`key="a`, `value`, `with`, `spaces"`) by the time it gets here.
// regroupQuotedArgs reassembles those before the per-token key=value
// parsing below runs.
func parseArgs(args []string) config {
	raw := make(map[string]string)
	for _, v := range regroupQuotedArgs(args) {
		// Split only on the first '=' so values may contain '=' as well.
		parts := strings.SplitN(v, "=", 2)
		key := parts[0]
		if key == "" {
			continue
		}
		if len(parts) == 2 {
			val := parts[1]
			// If the value is quoted ("..." or '...'), try to unquote it.
			if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
				// strconv.Unquote expects Go-style quoted string; try it but fall back to trimming.
				if unq, err := strconv.Unquote(val); err == nil {
					val = unq
				} else {
					val = strings.Trim(val, "\"'")
				}
			}
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
		cfg.insecureSkipVerify, _ = strconv.ParseBool(v)
	}

	return cfg
}

// regroupQuotedArgs rejoins consecutive tokens that were split in the
// middle of a double- or single-quoted value, e.g. [`key="a`, `spaced`,
// `value"`] becomes [`key="a spaced value"`]. A token that opens a quote
// but never finds a matching close absorbs the rest of args verbatim.
func regroupQuotedArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		tok := args[i]
		quote := openingQuote(tok)
		for quote != 0 && i+1 < len(args) && !strings.HasSuffix(tok, string(quote)) {
			i++
			tok += " " + args[i]
		}
		out = append(out, tok)
	}
	return out
}

// openingQuote returns the quote character (a double or single quote) that tok's
// value (the part after its first '=') opens with but doesn't also close
// within that same token, or 0 if tok doesn't open an unterminated quote.
func openingQuote(tok string) byte {
	_, value, hasValue := strings.Cut(tok, "=")
	if !hasValue || value == "" {
		return 0
	}

	q := value[0]
	if q != '"' && q != '\'' {
		return 0
	}
	if len(value) >= 2 && value[len(value)-1] == q {
		return 0 // already closed within this same token
	}
	return q
}
