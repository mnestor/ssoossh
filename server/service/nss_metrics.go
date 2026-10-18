package service

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// The result attribute of the lookup metrics: how one lookup ended. Bounded
// on purpose, like kind and by, and nothing per key or per name, so the
// series count stays fixed however large the fleet.
const (
	// NSSResultHit is a lookup answered with a record.
	NSSResultHit = "hit"
	// NSSResultMiss is an authoritative not-found.
	NSSResultMiss = "miss"
	// NSSResultUnauthorized is a refused fleet key.
	NSSResultUnauthorized = "unauthorized"
	// NSSResultRateLimited is a lookup refused by a rate limit.
	NSSResultRateLimited = "rate_limited"
	// NSSResultForbidden is a lookup from outside nss.source_allow_list.
	NSSResultForbidden = "forbidden"
	// NSSResultBadRequest is a name or id that failed validation.
	NSSResultBadRequest = "bad_request"
	// NSSResultError is a lookup the server could not answer: the
	// database failed, or the directory could not be asked about a name
	// the database lacks.
	NSSResultError = "error"
)

// The kind and by attributes: what was looked up, and how.
const (
	// NSSKindUser is a passwd lookup.
	NSSKindUser = "user"
	// NSSKindGroup is a group lookup.
	NSSKindGroup = "group"
	// NSSByName is a lookup by name.
	NSSByName = "name"
	// NSSByID is a lookup by uid or gid.
	NSSByID = "id"
)

// nssLookupMetrics holds the lookup instruments, on the service rather than
// in package state. Built from the meter it is given, which in production
// is the global provider's: a no-op until metrics are switched on.
type nssLookupMetrics struct {
	lookups  metric.Int64Counter
	duration metric.Float64Histogram
}

// newNSSLookupMetrics creates ssoossh_nss_lookups and
// ssoossh_nss_lookup_duration on meter.
func newNSSLookupMetrics(meter metric.Meter) (nssLookupMetrics, error) {
	lookups, err := meter.Int64Counter("ssoossh_nss_lookups",
		metric.WithDescription("NSS directory lookups by kind, key and outcome"),
		metric.WithUnit("{lookup}"))
	if err != nil {
		return nssLookupMetrics{}, fmt.Errorf("create the ssoossh_nss_lookups counter: %w", err)
	}
	duration, err := meter.Float64Histogram("ssoossh_nss_lookup_duration",
		metric.WithDescription("Time to answer one NSS directory lookup, authentication included"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5))
	if err != nil {
		return nssLookupMetrics{}, fmt.Errorf("create the ssoossh_nss_lookup_duration histogram: %w", err)
	}
	return nssLookupMetrics{lookups: lookups, duration: duration}, nil
}

// record counts one lookup and its duration.
func (m nssLookupMetrics) record(ctx context.Context, kind, by, result string, duration time.Duration) {
	attrs := metric.WithAttributes(nssAttr("kind", kind), nssAttr("by", by), nssAttr("result", result))
	m.lookups.Add(ctx, 1, attrs)
	m.duration.Record(ctx, duration.Seconds(), attrs)
}

// nssAttr is one string attribute.
func nssAttr(key, value string) attribute.KeyValue {
	return attribute.String(key, value)
}
