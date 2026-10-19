// Package upstream is ssoosshd-nss's side of the HTTPS lookups: it wraps
// internal/api's NSS calls with the state Part 3 of
// docs/proposals/nss-directory gives them ("Request path", Upstream): the
// offline state with exponential backoff, the 401 latch, Retry-After on
// 429, the concurrency cap, SRV discovery and reconnection retries.
//
// Every failure is classified into one of a few errors the daemon acts
// on: ErrNotFound is the only one it may negative-cache, ErrInvalid is a
// miss it must not cache, and everything wrapping ErrUnavailable means
// "serve from cache if possible, else ServiceNotAvailable".
package upstream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/nssd/config"
)

// How a lookup failed, as the daemon needs to know it.
var (
	// ErrNotFound is the server's authoritative miss (404 nss_not_found).
	ErrNotFound = errors.New("no such user or group upstream")
	// ErrInvalid is the server refusing the name or id (400
	// nss_bad_request). Answered as not found, never cached as one.
	ErrInvalid = errors.New("the server refused the name or id as invalid")
	// ErrUnavailable wraps every reason no answer was had.
	ErrUnavailable = errors.New("the directory is unavailable")
	// ErrOffline is a server that could not be reached or answered with
	// an error, or the backoff after one.
	ErrOffline = fmt.Errorf("%w: offline", ErrUnavailable)
	// ErrUnauthorized is the 401 latch: the fleet key was refused and no
	// lookup is sent until SIGHUP or a key file change.
	ErrUnauthorized = fmt.Errorf("%w: the server refused the fleet key", ErrUnavailable)
	// ErrRateLimited is the wait a 429's Retry-After asked for.
	ErrRateLimited = fmt.Errorf("%w: rate limited by the server", ErrUnavailable)
	// ErrBusy is a lookup over the concurrency cap, refused at once.
	ErrBusy = fmt.Errorf("%w: too many upstream lookups in flight", ErrUnavailable)
)

// States the client reports through Status.
const (
	// StateUnknown is a client that has not reached the server yet.
	StateUnknown = "unknown"
	// StateOnline is a client whose last lookup got an answer.
	StateOnline = "online"
	// StateOffline is a client backing off after a failure.
	StateOffline = "offline"
	// StateUnauthorized is the 401 latch.
	StateUnauthorized = "unauthorized"
	// StateRateLimited is a client waiting out a Retry-After.
	StateRateLimited = "rate_limited"
)

// MaxInFlight is the concurrency cap: upstream lookups in flight at once.
// One more is refused with ErrBusy rather than queued, so a flood of cold
// misses never makes a caller wait.
const MaxInFlight = 8

// maxOfflineBackoff caps the doubling backoff, as sssd's
// offline_timeout_max does at its default of an hour.
const maxOfflineBackoff = time.Hour

// latchLogInterval is how often a latched client repeats its error log.
const latchLogInterval = time.Minute

// retryPause is the wait between two attempts on one server. With
// several discovered servers the next attempt goes to the next one at
// once.
const retryPause = 200 * time.Millisecond

// Upstream is what the daemon asks of the directory. Client implements it.
type Upstream interface {
	// UserByName looks a user up by name.
	UserByName(ctx context.Context, name string) (*apitypes.NSSUserResponse, error)
	// UserByUID looks a user up by uid.
	UserByUID(ctx context.Context, uid uint32) (*apitypes.NSSUserResponse, error)
	// GroupByName looks a group up by name.
	GroupByName(ctx context.Context, name string) (*apitypes.NSSGroupResponse, error)
	// GroupByGID looks a group up by gid.
	GroupByGID(ctx context.Context, gid uint32) (*apitypes.NSSGroupResponse, error)
	// Status reports the connection state.
	Status() Status
	// Reset clears the 401 latch and the backoff and re-reads the key
	// file, for SIGHUP.
	Reset()
}

// Status is a snapshot of the client's state.
type Status struct {
	// State is one of the State* values.
	State string `json:"state"`
	// Server is the server last used, or the configured one.
	Server string `json:"server"`
	// KeyID is the fleet key id in use.
	KeyID string `json:"key_id"`
	// TLSCAFile is tls_ca_file as configured: a path, config.TLSCASystem
	// or empty. With TLSPins it is what status names as the trust.
	TLSCAFile string `json:"tls_ca_file,omitempty"`
	// TLSPins counts the tls_spki_pin entries.
	TLSPins int `json:"tls_spki_pins,omitempty"`
	// LastError is the last failure, empty when none.
	LastError string `json:"last_error,omitempty"`
	// LastErrorAt is when it happened.
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
	// RetryAt is when the next lookup will be tried, during a backoff or
	// a Retry-After.
	RetryAt time.Time `json:"retry_at,omitzero"`
}

// Options configures a Client. Config and Log are required; the rest
// default to the real thing and exist so tests can fake them.
type Options struct {
	// Config is the daemon's configuration.
	Config *config.Config
	// Log receives state changes.
	Log *slog.Logger
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// NewClient builds the HTTPS client for one server URL. Nil means
	// internal/api's.
	NewClient func(api.Config) (api.NSSClient, error)
	// Discover finds the servers from SRV records. Nil means
	// api.DiscoverServer.
	Discover func(ctx context.Context, domain string) ([]string, error)
	// Sleep waits between two attempts on one server. Nil means a timer
	// that ctx can interrupt.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client is the production Upstream.
type Client struct {
	cfg       *config.Config
	log       *slog.Logger
	now       func() time.Time
	newClient func(api.Config) (api.NSSClient, error)
	discover  func(context.Context, string) ([]string, error)
	sleep     func(context.Context, time.Duration) error

	// slots is the concurrency cap's semaphore.
	slots chan struct{}

	mu       sync.Mutex
	key      config.Key
	keyStamp config.Stamp
	keyErr   string
	clients  map[string]api.NSSClient
	servers  []string
	next     int

	state       string
	server      string
	backoff     time.Duration
	retryAt     time.Time
	lastErr     string
	lastErrAt   time.Time
	lastLatchAt time.Time
}

var _ Upstream = (*Client)(nil)

// New returns a Client, reading the fleet key file now: a missing or
// insecure key is a start-up error, never a silent first lookup failure.
func New(opts Options) (*Client, error) {
	c := &Client{
		cfg:       opts.Config,
		log:       opts.Log,
		now:       opts.Now,
		newClient: opts.NewClient,
		discover:  opts.Discover,
		sleep:     opts.Sleep,
		slots:     make(chan struct{}, MaxInFlight),
		clients:   map[string]api.NSSClient{},
		state:     StateUnknown,
		server:    opts.Config.Server,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.newClient == nil {
		c.newClient = func(cfg api.Config) (api.NSSClient, error) { return api.NewClient(cfg) }
	}
	if c.discover == nil {
		c.discover = api.DiscoverServer
	}
	if c.sleep == nil {
		c.sleep = sleepContext
	}

	key, err := config.ReadKeyFile(c.cfg.APIKeyFile)
	if err != nil {
		return nil, fmt.Errorf("fleet key: %w", err)
	}
	stamp, err := config.StampOf(c.cfg.APIKeyFile)
	if err != nil {
		// not covered: the file was read a moment ago; only a race with
		// its removal could fail the stat.
		return nil, fmt.Errorf("fleet key: %w", err)
	}
	c.key, c.keyStamp = key, stamp

	// Said once, at start: New runs once per daemon, and SIGHUP resets
	// the client without building a new one. With a pin as well the pin
	// decides which key may answer, so the store alone is not the trust.
	if c.cfg.TLSSystemRoots() && len(c.cfg.TLSSPKIPin) == 0 {
		c.log.Warn("tls_ca_file is system: every CA in this host's trust store can answer for the server, a TLS-inspection root included")
	}
	return c, nil
}

// UserByName implements Upstream.
func (c *Client) UserByName(ctx context.Context, name string) (*apitypes.NSSUserResponse, error) {
	var out *apitypes.NSSUserResponse
	err := c.do(ctx, func(ctx context.Context, n api.NSSClient) (err error) {
		out, err = n.GetNSSUserByName(ctx, name)
		return err
	})
	return out, err
}

// UserByUID implements Upstream.
func (c *Client) UserByUID(ctx context.Context, uid uint32) (*apitypes.NSSUserResponse, error) {
	var out *apitypes.NSSUserResponse
	err := c.do(ctx, func(ctx context.Context, n api.NSSClient) (err error) {
		out, err = n.GetNSSUserByUID(ctx, uid)
		return err
	})
	return out, err
}

// GroupByName implements Upstream.
func (c *Client) GroupByName(ctx context.Context, name string) (*apitypes.NSSGroupResponse, error) {
	var out *apitypes.NSSGroupResponse
	err := c.do(ctx, func(ctx context.Context, n api.NSSClient) (err error) {
		out, err = n.GetNSSGroupByName(ctx, name)
		return err
	})
	return out, err
}

// GroupByGID implements Upstream.
func (c *Client) GroupByGID(ctx context.Context, gid uint32) (*apitypes.NSSGroupResponse, error) {
	var out *apitypes.NSSGroupResponse
	err := c.do(ctx, func(ctx context.Context, n api.NSSClient) (err error) {
		out, err = n.GetNSSGroupByGID(ctx, gid)
		return err
	})
	return out, err
}

// Status implements Upstream.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{
		State:       c.state,
		Server:      c.server,
		KeyID:       c.key.ID,
		TLSCAFile:   c.cfg.TLSCAFile,
		TLSPins:     len(c.cfg.TLSSPKIPin),
		LastError:   c.lastErr,
		LastErrorAt: c.lastErrAt,
		RetryAt:     c.retryAt,
	}
}

// Reset implements Upstream: it re-reads the key file (keeping the old key
// if the new one is unreadable), clears the latch and the backoff, and
// forgets the discovered servers so the next lookup starts afresh.
func (c *Client) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reloadKeyLocked(true)
	c.state, c.backoff, c.retryAt = StateUnknown, 0, time.Time{}
	c.servers, c.clients = nil, map[string]api.NSSClient{}
	c.log.Info("upstream state reset")
}

// lookup is one API call on one server's client.
type lookup func(ctx context.Context, n api.NSSClient) error

// do runs one lookup through the gate, the cap, discovery and the retries,
// and classifies the outcome.
func (c *Client) do(ctx context.Context, call lookup) error {
	if err := c.gate(); err != nil {
		return err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return ErrBusy
	}

	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	// Set by WithTimeout, so always there.
	deadline, _ := ctx.Deadline()

	servers, err := c.serverList(ctx)
	if err != nil {
		return c.fail(parent, err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.ReconnectionRetries; attempt++ {
		url := c.pick(servers, attempt)
		client, err := c.clientFor(url)
		if err != nil {
			return c.fail(parent, err)
		}
		last := attempt == c.cfg.ReconnectionRetries
		attemptCtx, cancelAttempt := context.WithTimeout(ctx, attemptTimeout(time.Until(deadline), c.cfg.SocketAnswerDeadline, last))
		err = call(attemptCtx, client)
		cancelAttempt()
		var nssErr *api.NSSError
		switch {
		case err == nil:
			c.online(url)
			return nil
		case errors.Is(err, api.ErrNSSNotFound):
			c.online(url)
			return ErrNotFound
		case errors.As(err, &nssErr) && nssErr.StatusCode == http.StatusBadRequest:
			c.online(url)
			return fmt.Errorf("%w: %s", ErrInvalid, nssErr.ErrorCode)
		case errors.Is(err, api.ErrNSSUnauthorized):
			c.latch(url)
			return ErrUnauthorized
		case errors.As(err, &nssErr) && nssErr.StatusCode == http.StatusTooManyRequests:
			return c.rateLimited(url, nssErr.RetryAfter)
		case nssErr != nil:
			// Any other status is an answer from a server, not a
			// transport failure: trying again at once would get the same.
			return c.fail(parent, err)
		}
		// No HTTP answer: the connection this attempt used may be dead,
		// silently dropped by something on the path.
		c.retire(url, client)
		lastErr = err
		if ctx.Err() != nil || last {
			break
		}
		if len(servers) == 1 {
			if err := c.sleep(ctx, retryPause); err != nil {
				break
			}
		}
	}
	return c.fail(parent, lastErr)
}

// retryReserve is how much of the answer deadline the first attempt leaves
// for a retry: retryPause, then a fresh connection's dial, TLS handshake
// and answer, which took about 20 ms on a real path. The rest is slack for
// a slower one.
const retryReserve = time.Second

// attemptTimeout bounds one attempt, given the time left of the lookup's
// timeout (which stays the hard bound on the whole call) and the daemon's
// socket_answer_deadline. The last attempt gets everything left. Every
// other one gets
//
//	min(left/2, max(answerDeadline-retryReserve, answerDeadline/2))
//
// so an attempt hung on a dead connection gives up early enough for a
// retry on a fresh one to answer the userdb caller inside its deadline
// (1.5 s with the defaults: 2.5 s answer deadline, 5 s timeout), and never
// takes more than half of what is left of the timeout, so a retry always
// has as long as the attempt before it. A deadline too short to spare
// retryReserve is split in half instead.
//
// A slow but live server loses nothing by the shorter first attempt: the
// server singleflights its directory reads per key and finishes one whose
// caller gave up, so the retry joins the same read rather than starting a
// new one.
func attemptTimeout(left, answerDeadline time.Duration, last bool) time.Duration {
	if last || left <= 0 {
		return left
	}
	if answerDeadline <= 0 {
		return left / 2
	}
	return min(left/2, max(answerDeadline-retryReserve, answerDeadline/2))
}

// gate refuses a lookup the state says not to send: latched, backing off
// or waiting out a Retry-After. It also notices a changed key file, which
// clears the latch.
func (c *Client) gate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()

	if c.reloadKeyLocked(false) && c.state == StateUnauthorized {
		c.log.Info("the fleet key file changed; retrying the server")
		c.state, c.retryAt = StateUnknown, time.Time{}
	}

	switch {
	case c.state == StateUnauthorized:
		if now.Sub(c.lastLatchAt) >= latchLogInterval {
			c.log.Error("the server refused the fleet key; serving from cache until SIGHUP or a key file change",
				"key_id", c.key.ID, "server", c.server)
			c.lastLatchAt = now
		}
		return ErrUnauthorized
	case now.Before(c.retryAt) && c.state == StateRateLimited:
		return ErrRateLimited
	case now.Before(c.retryAt):
		return ErrOffline
	default:
		return nil
	}
}

// reloadKeyLocked re-reads the key file when its stamp changed, or always
// when force is set, and reports whether a new key was taken. A file that
// fails to read keeps the old key and is logged once per change.
func (c *Client) reloadKeyLocked(force bool) bool {
	stamp, err := config.StampOf(c.cfg.APIKeyFile)
	if err == nil && stamp == c.keyStamp && !force {
		return false
	}
	c.keyStamp = stamp
	key, err := config.ReadKeyFile(c.cfg.APIKeyFile)
	if err != nil {
		if msg := err.Error(); msg != c.keyErr {
			c.log.Error("cannot read the fleet key file; keeping the current key", "error", err)
			c.keyErr = msg
		}
		return false
	}
	c.keyErr = ""
	c.key = key
	c.clients = map[string]api.NSSClient{}
	return true
}

// serverList returns the servers to try: the configured one, or the
// discovered ones, discovering again after a failure cleared them.
func (c *Client) serverList(ctx context.Context) ([]string, error) {
	if c.cfg.Server != "" {
		return []string{c.cfg.Server}, nil
	}
	c.mu.Lock()
	servers := c.servers
	c.mu.Unlock()
	if len(servers) > 0 {
		return servers, nil
	}
	servers, err := c.discover(ctx, c.cfg.Domain)
	if err != nil {
		return nil, fmt.Errorf("discover the server: %w", err)
	}
	c.mu.Lock()
	c.servers, c.next = servers, 0
	c.mu.Unlock()
	return servers, nil
}

// pick returns the server for an attempt: the one that last answered
// first, then the rest in their discovered order.
func (c *Client) pick(servers []string, attempt int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return servers[(c.next+attempt)%len(servers)]
}

// connectTimeout bounds the TCP connect of every upstream call, so an
// unreachable server is given up on quickly while a slow answer still has
// the whole of the timeout setting.
const connectTimeout = 2 * time.Second

// clientFor returns the HTTPS client for url, building it on first use
// with the current key.
func (c *Client) clientFor(url string) (api.NSSClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if client, ok := c.clients[url]; ok {
		return client, nil
	}
	client, err := c.newClient(c.apiConfig(url))
	if err != nil {
		return nil, fmt.Errorf("build the client for %s: %w", url, err)
	}
	c.clients[url] = client
	return client, nil
}

// apiConfig is the client configuration for url. "tls_ca_file: system"
// stays a word of nss.yaml: it becomes api.Config.TLSSystemRoots here, so
// internal/api never reads a file name for a mode.
func (c *Client) apiConfig(url string) api.Config {
	cfg := api.Config{
		ServerURL:      url,
		TLSSPKIPins:    c.cfg.TLSSPKIPin,
		KeyID:          c.key.ID,
		KeySecret:      c.key.Secret,
		RequestTimeout: c.cfg.Timeout,
		DialTimeout:    connectTimeout,
		UserAgent:      "ssoosshd-nss/" + version.Version,
	}
	if c.cfg.TLSSystemRoots() {
		cfg.TLSSystemRoots = true
	} else {
		cfg.TLSCAFile = c.cfg.TLSCAFile
	}
	return cfg
}

// retire replaces url's client after an attempt that got no answer, so the
// next attempt, and every lookup after it, dials a fresh connection.
//
// Closing the client's idle connections alone is not enough: when an
// HTTP/2 attempt gives up, its stream is still being torn down on the
// connection, so the connection is not idle yet, survives the close, and
// the pool hands the same dead connection to the retry. A new client
// cannot reuse it. Lookups still in flight on the old client finish there,
// which a close of the connection itself would not allow. Its idle
// connections are closed now; a dead busy one is closed by the transport's
// ping within about 20 s, a live one when it has been idle for 90 s.
func (c *Client) retire(url string, client api.NSSClient) {
	c.mu.Lock()
	if c.clients[url] == client {
		delete(c.clients, url)
	}
	c.mu.Unlock()
	if closer, ok := client.(api.IdleCloser); ok {
		closer.CloseIdleConnections()
	}
}

// online records an answer from url.
func (c *Client) online(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == StateOffline || c.state == StateRateLimited {
		c.log.Info("the directory is reachable again", "server", url)
	}
	c.state, c.server, c.backoff, c.retryAt = StateOnline, url, 0, time.Time{}
	if i := slices.Index(c.servers, url); i >= 0 {
		c.next = i
	}
}

// latch records a 401 and logs it.
func (c *Client) latch(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.state, c.server, c.lastErr, c.lastErrAt, c.lastLatchAt = StateUnauthorized, url, ErrUnauthorized.Error(), now, now
	c.log.Error("the server refused the fleet key; serving from cache until SIGHUP or a key file change",
		"key_id", c.key.ID, "server", url)
}

// rateLimited records a 429: no lookup until Retry-After has passed, or
// offline_timeout when the server gave none.
func (c *Client) rateLimited(url string, retryAfter time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if retryAfter <= 0 {
		retryAfter = c.cfg.OfflineTimeout
	}
	now := c.now()
	c.state, c.server, c.retryAt = StateRateLimited, url, now.Add(retryAfter)
	c.lastErr, c.lastErrAt = ErrRateLimited.Error(), now
	c.log.Warn("the server is rate limiting this host", "server", url, "retry_after", retryAfter.String())
	return ErrRateLimited
}

// fail records a failed lookup and starts or doubles the backoff. A
// lookup cut short by its caller's context (the daemon shutting down) says
// nothing about the server and changes no state.
func (c *Client) fail(parent context.Context, err error) error {
	if parent.Err() != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, parent.Err())
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.state == StateOffline {
		c.backoff = min(2*c.backoff, maxOfflineBackoff)
	} else {
		c.backoff = c.cfg.OfflineTimeout
		c.log.Warn("the directory is unreachable; serving from cache", "error", err, "retry_in", c.backoff.String())
	}
	c.state, c.retryAt = StateOffline, now.Add(c.backoff)
	c.lastErr, c.lastErrAt = err.Error(), now
	// Discovery runs again on the next probe, in case the records moved.
	c.servers = nil
	return fmt.Errorf("%w: %w", ErrOffline, err)
}

// sleepContext waits d or until ctx ends.
func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
