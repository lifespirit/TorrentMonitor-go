package sitetpl

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxAIADepth     = 5
	maxAIAResponse  = 1 << 20 // 1 MiB
	aiaFetchTimeout = 5 * time.Second
	aiaCacheTTL     = 24 * time.Hour
)

type aiaIssuerFetchFunc func(context.Context, string) ([]*x509.Certificate, error)

type aiaCacheEntry struct {
	certs   []*x509.Certificate
	expires time.Time
}

type aiaIssuerFetcher struct {
	mu     sync.Mutex
	cache  map[string]aiaCacheEntry
	client *http.Client
}

var defaultAIAIssuerFetcher = newAIAIssuerFetcher()
var aiaFetchIssuer aiaIssuerFetchFunc = defaultAIAIssuerFetcher.fetch

func init() {
	// TorrentMonitor is an executable, so upgrading the process-wide default
	// transport also covers native tracker clients and transports cloned for
	// HTTP/SOCKS proxy access without tracker-specific exceptions.
	if transport, ok := http.DefaultTransport.(*http.Transport); ok {
		http.DefaultTransport = newAIATransport(transport)
	}
}

func newAIAIssuerFetcher() *aiaIssuerFetcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = safeAIADialContext
	return &aiaIssuerFetcher{
		cache: make(map[string]aiaCacheEntry),
		client: &http.Client{
			Transport: transport,
			Timeout:   aiaFetchTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 3 {
					return errors.New("too many AIA redirects")
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return fmt.Errorf("unsupported AIA redirect scheme %q", req.URL.Scheme)
				}
				return nil
			},
		},
	}
}

// newAIATransport keeps normal TLS verification semantics, but fills missing
// intermediate CA certificates from the leaf/intermediate AIA caIssuers URLs.
// The downloaded certificates are only intermediates: they still have to build
// a valid chain to the configured/system roots and match the requested host.
func newAIATransport(base *http.Transport) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	transport := base.Clone()
	cfg := &tls.Config{}
	if transport.TLSClientConfig != nil {
		cfg = transport.TLSClientConfig.Clone()
	}
	// Respect an explicitly insecure custom transport rather than silently
	// changing its semantics. TorrentMonitor's own transports are verified.
	if cfg.InsecureSkipVerify {
		return transport
	}
	roots := cfg.RootCAs
	serverName := cfg.ServerName
	originalVerifyConnection := cfg.VerifyConnection

	// Disable only crypto/tls' built-in verifier; VerifyConnection below fully
	// replaces it with x509.Verify plus AIA chain completion.
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		dnsName := cs.ServerName
		if dnsName == "" {
			dnsName = serverName
		}
		chains, err := verifyPeerCertificatesWithAIA(cs.PeerCertificates, dnsName, roots, aiaFetchIssuer)
		if err != nil {
			return err
		}
		cs.VerifiedChains = chains
		if originalVerifyConnection != nil {
			return originalVerifyConnection(cs)
		}
		return nil
	}
	transport.TLSClientConfig = cfg
	return transport
}

func verifyPeerCertificatesWithAIA(peer []*x509.Certificate, dnsName string, roots *x509.CertPool, fetch aiaIssuerFetchFunc) ([][]*x509.Certificate, error) {
	if len(peer) == 0 {
		return nil, errors.New("TLS peer sent no certificates")
	}
	if fetch == nil {
		return nil, errors.New("AIA issuer fetcher is nil")
	}

	intermediates := x509.NewCertPool()
	known := make(map[[32]byte]struct{}, len(peer)+maxAIADepth)
	for i, cert := range peer {
		if cert == nil {
			continue
		}
		known[sha256.Sum256(cert.Raw)] = struct{}{}
		if i > 0 {
			intermediates.AddCert(cert)
		}
	}

	verify := func() ([][]*x509.Certificate, error) {
		return peer[0].Verify(x509.VerifyOptions{
			DNSName:       dnsName,
			Roots:         roots,
			Intermediates: intermediates,
		})
	}

	chains, err := verify()
	if err == nil {
		return chains, nil
	}
	if !isUnknownAuthority(err) {
		return nil, err
	}

	frontier := append([]*x509.Certificate(nil), peer...)
	for depth := 0; depth < maxAIADepth; depth++ {
		next := make([]*x509.Certificate, 0, len(frontier))
		for _, child := range frontier {
			if child == nil {
				continue
			}
			for _, rawURL := range child.IssuingCertificateURL {
				certs, fetchErr := fetch(context.Background(), rawURL)
				if fetchErr != nil {
					continue
				}
				for _, issuer := range certs {
					if issuer == nil || !issuer.IsCA {
						continue
					}
					fingerprint := sha256.Sum256(issuer.Raw)
					if _, exists := known[fingerprint]; exists {
						continue
					}
					if child.CheckSignatureFrom(issuer) != nil {
						continue
					}
					known[fingerprint] = struct{}{}
					intermediates.AddCert(issuer)
					next = append(next, issuer)
				}
			}
		}
		if len(next) == 0 {
			break
		}
		chains, err = verify()
		if err == nil {
			return chains, nil
		}
		if !isUnknownAuthority(err) {
			return nil, err
		}
		frontier = next
	}
	return nil, err
}

func isUnknownAuthority(err error) bool {
	var unknown x509.UnknownAuthorityError
	return errors.As(err, &unknown)
}

func (f *aiaIssuerFetcher) fetch(ctx context.Context, rawURL string) ([]*x509.Certificate, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse AIA issuer URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported AIA issuer scheme %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("AIA issuer URL has no host")
	}
	if u.User != nil {
		return nil, errors.New("AIA issuer URL must not contain userinfo")
	}
	cacheKey := u.String()
	now := time.Now()

	// A missing chain is rare. Serializing first-time AIA fetches avoids a burst
	// of identical CA downloads when many tracker checks start together.
	f.mu.Lock()
	defer f.mu.Unlock()
	if entry, ok := f.cache[cacheKey]; ok {
		if now.Before(entry.expires) {
			return append([]*x509.Certificate(nil), entry.certs...), nil
		}
		delete(f.cache, cacheKey)
	}

	fetchCtx, cancel := context.WithTimeout(ctx, aiaFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, cacheKey, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/pkix-cert, application/x-x509-ca-cert, application/pem-certificate-chain;q=0.9, */*;q=0.1")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch AIA issuer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetch AIA issuer: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAIAResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read AIA issuer: %w", err)
	}
	if len(body) > maxAIAResponse {
		return nil, fmt.Errorf("AIA issuer response exceeds %d bytes", maxAIAResponse)
	}
	certs, err := parseAIACertificates(body)
	if err != nil {
		return nil, err
	}

	expires := now.Add(aiaCacheTTL)
	for _, cert := range certs {
		if cert.NotAfter.Before(expires) {
			expires = cert.NotAfter
		}
	}
	if expires.After(now) {
		if len(f.cache) >= 256 {
			for key, entry := range f.cache {
				if !now.Before(entry.expires) {
					delete(f.cache, key)
				}
			}
			if len(f.cache) >= 256 {
				for key := range f.cache {
					delete(f.cache, key)
					break
				}
			}
		}
		f.cache[cacheKey] = aiaCacheEntry{certs: append([]*x509.Certificate(nil), certs...), expires: expires}
	}
	return certs, nil
}

func parseAIACertificates(body []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := body
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PEM AIA issuer certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if len(certs) > 0 {
		return certs, nil
	}
	certs, err := x509.ParseCertificates(body)
	if err != nil {
		return nil, fmt.Errorf("parse DER AIA issuer certificate: %w", err)
	}
	if len(certs) == 0 {
		return nil, errors.New("AIA issuer response contains no certificates")
	}
	return certs, nil
}

func safeAIADialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse AIA address %q: %w", address, err)
	}
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		ips, err = net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve AIA host %q: %w", host, err)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("AIA host %q has no addresses", host)
	}
	for _, addr := range ips {
		if !safeAIAIP(addr.IP) {
			return nil, fmt.Errorf("AIA host %q resolves to non-public address %s", host, addr.IP)
		}
	}

	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, addr := range ips {
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	return nil, fmt.Errorf("dial AIA host %q: %w", host, lastErr)
}

func safeAIAIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 is shared carrier-grade NAT space and can still reach
		// infrastructure-local services on some hosts/providers.
		if v4[0] == 100 && v4[1]&0xc0 == 64 {
			return false
		}
	}
	return true
}
