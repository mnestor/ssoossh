package api

// Test methodology: a fake SRVResolver answers the query, and IntN is
// replaced by a scripted sequence, so each RFC 2782 ordering decision is
// pinned exactly rather than sampled.

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
)

// fakeResolver answers LookupSRV with fixed records and records the query.
type fakeResolver struct {
	records []*net.SRV
	err     error

	service, proto, name string
}

// LookupSRV implements SRVResolver.
func (f *fakeResolver) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	f.service, f.proto, f.name = service, proto, name
	return "_" + service + "._" + proto + "." + name + ".", f.records, f.err
}

// scriptedIntN returns IntN answers from picks in order, and records each
// n it was asked for. Past the end of picks it answers 0.
func scriptedIntN(picks []int, asked *[]int) func(int) int {
	return func(n int) int {
		*asked = append(*asked, n)
		if len(picks) == 0 {
			return 0
		}
		p := picks[0]
		picks = picks[1:]
		return p
	}
}

// TestServerDiscovery_ShouldQueryTheSSOOSSHServiceRecord pins the name
// looked up: _ssoossh._tcp.<domain>.
func TestServerDiscovery_ShouldQueryTheSSOOSSHServiceRecord(t *testing.T) {
	t.Parallel()

	resolver := &fakeResolver{records: []*net.SRV{{Target: "sso.example.com.", Port: 443}}}
	if _, err := (ServerDiscovery{Resolver: resolver}).DiscoverServer(context.Background(), " example.com "); err != nil {
		t.Fatalf("DiscoverServer() error = %v", err)
	}
	if got := []string{resolver.service, resolver.proto, resolver.name}; !reflect.DeepEqual(got, []string{"ssoossh", "tcp", "example.com"}) {
		t.Errorf("queried %v, want [ssoossh tcp example.com]", got)
	}
}

// TestServerDiscovery_Order pins RFC 2782's ordering for scripted draws.
func TestServerDiscovery_Order(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		records   []*net.SRV
		picks     []int
		want      []string
		wantAsked []int
	}{
		{
			name: "should try the lowest priority first when priorities differ",
			records: []*net.SRV{
				{Target: "c.example.com.", Port: 443, Priority: 30},
				{Target: "a.example.com.", Port: 443, Priority: 10},
				{Target: "b.example.com.", Port: 443, Priority: 20},
			},
			want:      []string{"https://a.example.com:443", "https://b.example.com:443", "https://c.example.com:443"},
			wantAsked: []int{1, 1, 1},
		},
		{
			name: "should pick the heavier record when the draw lands in its share",
			records: []*net.SRV{
				{Target: "light.example.com.", Port: 443, Priority: 10, Weight: 10},
				{Target: "heavy.example.com.", Port: 443, Priority: 10, Weight: 90},
			},
			picks:     []int{50, 0},
			want:      []string{"https://heavy.example.com:443", "https://light.example.com:443"},
			wantAsked: []int{101, 11},
		},
		{
			name: "should pick the lighter record when the draw lands in its share",
			records: []*net.SRV{
				{Target: "light.example.com.", Port: 443, Priority: 10, Weight: 10},
				{Target: "heavy.example.com.", Port: 443, Priority: 10, Weight: 90},
			},
			picks:     []int{5, 0},
			want:      []string{"https://light.example.com:443", "https://heavy.example.com:443"},
			wantAsked: []int{101, 91},
		},
		{
			name: "should put a weight 0 record first when the draw is 0",
			records: []*net.SRV{
				{Target: "weighted.example.com.", Port: 443, Priority: 10, Weight: 10},
				{Target: "zero.example.com.", Port: 443, Priority: 10, Weight: 0},
			},
			picks:     []int{0},
			want:      []string{"https://zero.example.com:443", "https://weighted.example.com:443"},
			wantAsked: []int{11, 11},
		},
		{
			name: "should pass over a weight 0 record when the draw is above 0",
			records: []*net.SRV{
				{Target: "zero.example.com.", Port: 443, Priority: 10, Weight: 0},
				{Target: "weighted.example.com.", Port: 443, Priority: 10, Weight: 10},
			},
			picks:     []int{1},
			want:      []string{"https://weighted.example.com:443", "https://zero.example.com:443"},
			wantAsked: []int{11, 1},
		},
		{
			name: "should keep the published order when every weight is 0",
			records: []*net.SRV{
				{Target: "first.example.com.", Port: 443, Priority: 10},
				{Target: "second.example.com.", Port: 443, Priority: 10},
			},
			want:      []string{"https://first.example.com:443", "https://second.example.com:443"},
			wantAsked: []int{1, 1},
		},
		{
			name: "should order by weight inside each priority when priorities and weights mix",
			records: []*net.SRV{
				{Target: "backup.example.com.", Port: 8443, Priority: 20, Weight: 1},
				{Target: "a.example.com.", Port: 443, Priority: 10, Weight: 1},
				{Target: "b.example.com.", Port: 443, Priority: 10, Weight: 3},
			},
			picks:     []int{2, 0, 0},
			want:      []string{"https://b.example.com:443", "https://a.example.com:443", "https://backup.example.com:8443"},
			wantAsked: []int{5, 2, 2},
		},
		{
			name: "should take the last record when the draw is out of range",
			records: []*net.SRV{
				{Target: "a.example.com.", Port: 443, Priority: 10, Weight: 1},
				{Target: "b.example.com.", Port: 443, Priority: 10, Weight: 1},
			},
			picks:     []int{99, 0},
			want:      []string{"https://b.example.com:443", "https://a.example.com:443"},
			wantAsked: []int{3, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var asked []int
			d := ServerDiscovery{Resolver: &fakeResolver{records: tt.records}, IntN: scriptedIntN(tt.picks, &asked)}
			got, err := d.DiscoverServer(context.Background(), "example.com")
			if err != nil {
				t.Fatalf("DiscoverServer() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(asked, tt.wantAsked) {
				t.Errorf("IntN asked %v, want %v", asked, tt.wantAsked)
			}
		})
	}
}

// TestServerDiscovery_Failures covers the answers that leave nothing to
// connect to, and the one partial failure that still does.
func TestServerDiscovery_Failures(t *testing.T) {
	t.Parallel()

	lookupErr := errors.New("SERVFAIL")

	tests := []struct {
		name     string
		domain   string
		resolver *fakeResolver
		want     []string
		wantErr  error
		anyErr   bool
	}{
		{name: "should refuse an empty domain", domain: " ", resolver: &fakeResolver{}, anyErr: true},
		{name: "should report the lookup error when there are no records", domain: "example.com", resolver: &fakeResolver{err: lookupErr}, wantErr: lookupErr},
		{name: "should report no servers when the answer is empty", domain: "example.com", resolver: &fakeResolver{}, wantErr: ErrNoServerRecords},
		{
			name:     "should report no servers when the domain says the service is not offered",
			domain:   "example.com",
			resolver: &fakeResolver{records: []*net.SRV{{Target: ".", Port: 0}}},
			wantErr:  ErrNoServerRecords,
		},
		{
			name:     "should skip a record when it has no target",
			domain:   "example.com",
			resolver: &fakeResolver{records: []*net.SRV{nil, {Target: ""}, {Target: "sso.example.com.", Port: 443}}},
			want:     []string{"https://sso.example.com:443"},
		},
		{
			name:     "should use the valid records when the resolver dropped some",
			domain:   "example.com",
			resolver: &fakeResolver{records: []*net.SRV{{Target: "sso.example.com.", Port: 443}}, err: lookupErr},
			want:     []string{"https://sso.example.com:443"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := (ServerDiscovery{Resolver: tt.resolver}).DiscoverServer(context.Background(), tt.domain)
			if tt.anyErr || tt.wantErr != nil {
				if err == nil || (tt.wantErr != nil && !errors.Is(err, tt.wantErr)) {
					t.Errorf("got %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DiscoverServer() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDiscoverServer_ShouldUseTheSystemResolver covers the package
// function. A cancelled context makes the system resolver fail at once,
// so the test needs no network.
func TestDiscoverServer_ShouldUseTheSystemResolver(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DiscoverServer(ctx, "example.invalid"); err == nil {
		t.Error("expected an error from a cancelled lookup, got none")
	}
}
