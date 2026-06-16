package messages_search

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/olivere/elastic"
)

var (
	osMu     sync.Mutex
	osClient *elastic.Client
)

// ESClient returns the process-wide olivere/elastic v6 client connected to the
// configured OpenSearch read cluster. Sniffing is disabled because the cluster
// usually sits behind a service VIP that does not expose intra-cluster IPs to
// callers.
//
// Self-healing: a previous sync.Once layout meant a single ping failure at
// boot would pin osErr forever; we now re-attempt construction on every call
// that finds the cached client nil. Successful builds are cached; failed
// builds are retried on the next request rather than poisoning the cache.
//
// The cached client is keyed on nothing: the first successful build wins for
// the process lifetime. That is correct today because SearchConfig is read
// once from the environment at module init and never mutates; if config ever
// becomes reloadable, this cache must be keyed on the connection fields
// (addrs/credentials) or invalidated on change.
//
// Concurrency: the build (which includes a ping with up to ~5s of network
// wait) runs *outside* the mutex so that concurrent requests during an OS
// outage fail fast in parallel instead of serialising behind one lock holder.
// Two goroutines may race to build; the loser's client is closed and the
// winner's is kept.
func ESClient(cfg SearchConfig) (*elastic.Client, error) {
	osMu.Lock()
	if osClient != nil {
		c := osClient
		osMu.Unlock()
		return c, nil
	}
	osMu.Unlock()

	c, err := buildESClient(cfg)
	if err != nil {
		return nil, err
	}

	osMu.Lock()
	defer osMu.Unlock()
	if osClient != nil {
		c.Stop()
		return osClient, nil
	}
	osClient = c
	return osClient, nil
}

// errCredentialsOverCleartext blocks basic-auth credentials from travelling
// over non-loopback http:// transport. Deployments that genuinely need it
// (e.g. a TLS-terminating sidecar) must opt in via
// OCTO_SEARCH_OS_INSECURE_HTTP=true.
var errCredentialsOverCleartext = errors.New(
	"messages_search: OCTO_SEARCH_OS_USERNAME is set but OCTO_SEARCH_OS_ADDRS contains a non-loopback http:// address; " +
		"use https:// or explicitly set OCTO_SEARCH_OS_INSECURE_HTTP=true")

// checkTransportSecurity rejects configs that would send credentials in
// cleartext. Loopback addresses are exempt (local dev / same-host proxy).
func checkTransportSecurity(cfg SearchConfig) error {
	if cfg.OSUsername == "" || cfg.OSInsecureHTTP {
		return nil
	}
	for _, addr := range cfg.OSAddrs {
		u, err := url.Parse(addr)
		if err != nil || u.Scheme == "https" {
			continue
		}
		host := u.Hostname()
		if host == "localhost" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			continue
		}
		return errCredentialsOverCleartext
	}
	return nil
}

func buildESClient(cfg SearchConfig) (*elastic.Client, error) {
	if err := checkTransportSecurity(cfg); err != nil {
		return nil, err
	}
	opts := []elastic.ClientOptionFunc{
		elastic.SetURL(cfg.OSAddrs...),
		elastic.SetSniff(false),
		elastic.SetHealthcheck(true),
		elastic.SetHealthcheckTimeout(3 * time.Second),
	}
	if cfg.OSInsecureSkipVerify {
		// Self-signed / internal-CA OS clusters in dev / test environments
		// where the pod's system trust store has no chain to verify against.
		// Disable cert verification on the HTTP client olivere uses for both
		// the startup healthcheck and subsequent requests. MUST NOT be set
		// in production deployments — see config.go::OSInsecureSkipVerify.
		hc := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
		if cfg.Timeout > 0 {
			hc.Timeout = cfg.Timeout
		}
		opts = append(opts, elastic.SetHttpClient(hc))
	}
	if cfg.OSUsername != "" {
		opts = append(opts, elastic.SetBasicAuth(cfg.OSUsername, cfg.OSPassword))
	}
	c, err := elastic.NewClient(opts...)
	if err != nil {
		return nil, err
	}
	if len(cfg.OSAddrs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, _, err := c.Ping(cfg.OSAddrs[0]).Do(ctx); err != nil {
			c.Stop()
			return nil, err
		}
	}
	return c, nil
}

// resetESClientForTest is only called from tests to swap in fakes.
func resetESClientForTest() {
	osMu.Lock()
	defer osMu.Unlock()
	osClient = nil
}
