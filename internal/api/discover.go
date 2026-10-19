package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"slices"
	"strconv"
	"strings"
)

// ErrNoServerRecords is a domain whose _ssoossh._tcp SRV records name no
// server to try: none at all, or only the "." target RFC 2782 uses to say
// the service is decidedly not offered there.
var ErrNoServerRecords = errors.New("no ssoossh server is published for the domain")

// ErrServerOutsideDomain is a domain whose _ssoossh._tcp SRV records only
// name servers outside it. Such a record is refused rather than tried: see
// ServerDiscovery.
var ErrServerOutsideDomain = errors.New("the domain's SRV records name only servers outside it")

// SRVResolver is the part of *net.Resolver that server discovery uses, so a
// test can answer the query itself.
type SRVResolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
}

// ServerDiscovery finds ssoosshd from a domain's _ssoossh._tcp SRV records.
// The zero value uses the system resolver.
//
// Discovery chooses where to connect, never whom to trust: a spoofed record
// points at a server that then fails the TLS pin (see Config.TLSSPKIPins)
// or a private CA (Config.TLSCAFile). Config.TLSSystemRoots, or a CA file
// holding a public CA's roots, is not such a bound on its own, because the
// host name verified is the one the record names and a public CA certifies
// any name its owner controls. So a record whose target is not the domain
// itself or a name under it is refused: a public CA certifies a name under
// the domain only for whoever controls the domain, which a spoofed DNS
// answer does not give, and with a pin or a private CA the rule costs
// nothing. A server in another domain is reached by setting server, or by
// discovering from that domain.
type ServerDiscovery struct {
	// Resolver answers the SRV query. Nil means net.DefaultResolver.
	Resolver SRVResolver

	// IntN returns a uniform random integer in [0, n), for RFC 2782's
	// weighted choice. Nil means math/rand/v2's IntN; a test sets it to
	// make the order deterministic.
	IntN func(n int) int
}

// DiscoverServer returns the ssoosshd base URLs domain publishes, in the
// order to try them. It is ServerDiscovery's zero value at work.
func DiscoverServer(ctx context.Context, domain string) ([]string, error) {
	return ServerDiscovery{}.DiscoverServer(ctx, domain)
}

// DiscoverServer looks up _ssoossh._tcp.<domain> and returns one
// "https://host:port" URL per record, ordered as RFC 2782 says: lowest
// priority first, and within a priority a weighted random order. The
// caller tries them in turn until one answers.
func (d ServerDiscovery) DiscoverServer(ctx context.Context, domain string) ([]string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, errors.New("server discovery needs a domain")
	}

	var resolver SRVResolver = net.DefaultResolver
	if d.Resolver != nil {
		resolver = d.Resolver
	}
	// math/rand, not crypto/rand: the draw only spreads load across
	// servers, and nothing secret depends on it.
	intN := rand.IntN
	if d.IntN != nil {
		intN = d.IntN
	}

	_, records, err := resolver.LookupSRV(ctx, "ssoossh", "tcp", domain)
	// LookupSRV drops records whose target is not a valid name and reports
	// that as an error alongside the rest, so the rest are still usable.
	if err != nil && len(records) == 0 {
		return nil, fmt.Errorf("look up _ssoossh._tcp.%s: %w", domain, err)
	}

	usable := slices.DeleteFunc(slices.Clone(records), func(r *net.SRV) bool {
		return r == nil || r.Target == "" || r.Target == "."
	})
	if len(usable) == 0 {
		return nil, fmt.Errorf("%w: _ssoossh._tcp.%s", ErrNoServerRecords, domain)
	}
	var outside []string
	usable = slices.DeleteFunc(usable, func(r *net.SRV) bool {
		if inDomain(r.Target, domain) {
			return false
		}
		outside = append(outside, strings.TrimSuffix(r.Target, "."))
		return true
	})
	if len(usable) == 0 {
		return nil, fmt.Errorf("%w: _ssoossh._tcp.%s names %s, none of them %s or a name under it",
			ErrServerOutsideDomain, domain, strings.Join(outside, ", "), domain)
	}

	ordered := orderSRV(usable, intN)
	urls := make([]string, 0, len(ordered))
	for _, r := range ordered {
		host := strings.TrimSuffix(r.Target, ".")
		urls = append(urls, "https://"+net.JoinHostPort(host, strconv.Itoa(int(r.Port))))
	}
	return urls, nil
}

// inDomain reports whether target, an SRV record's host name, is domain
// itself or a name under it. DNS names compare case-insensitively, and a
// trailing dot on either is dropped first.
func inDomain(target, domain string) bool {
	t := strings.ToLower(strings.TrimSuffix(target, "."))
	d := strings.ToLower(strings.TrimSuffix(domain, "."))
	return d != "" && (t == d || strings.HasSuffix(t, "."+d))
}

// orderSRV sorts records into RFC 2782 order: ascending priority, and
// within each priority the weighted random order weightedOrder gives.
func orderSRV(records []*net.SRV, intN func(int) int) []*net.SRV {
	sorted := slices.Clone(records)
	slices.SortStableFunc(sorted, func(a, b *net.SRV) int { return cmp.Compare(a.Priority, b.Priority) })

	ordered := make([]*net.SRV, 0, len(sorted))
	for start := 0; start < len(sorted); {
		end := start + 1
		for end < len(sorted) && sorted[end].Priority == sorted[start].Priority {
			end++
		}
		ordered = append(ordered, weightedOrder(sorted[start:end], intN)...)
		start = end
	}
	return ordered
}

// weightedOrder orders one priority's records by RFC 2782's selection:
// put the weight 0 records first, draw a number in [0, sum of weights],
// take the first record whose running weight sum reaches it, remove it and
// repeat. A heavier record is likelier to come early, and a weight 0 one
// still has a small chance when every weight is not 0.
func weightedOrder(group []*net.SRV, intN func(int) int) []*net.SRV {
	remaining := slices.Clone(group)
	slices.SortStableFunc(remaining, func(a, b *net.SRV) int {
		return cmp.Compare(min(a.Weight, 1), min(b.Weight, 1))
	})

	ordered := make([]*net.SRV, 0, len(remaining))
	for len(remaining) > 0 {
		total := 0
		for _, r := range remaining {
			total += int(r.Weight)
		}
		pick := intN(total + 1)

		chosen, running := len(remaining)-1, 0
		for i, r := range remaining {
			running += int(r.Weight)
			if running >= pick {
				chosen = i
				break
			}
		}
		ordered = append(ordered, remaining[chosen])
		remaining = slices.Delete(remaining, chosen, chosen+1)
	}
	return ordered
}
