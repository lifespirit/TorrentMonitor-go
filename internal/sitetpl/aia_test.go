package sitetpl

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func TestAIATransportCompletesMissingIntermediate(t *testing.T) {
	root := makeTestCA(t, "Root", nil)
	issuer := makeTestCA(t, "Issuer", root)
	leaf := makeTestLeaf(t, "tracker.test", issuer, []string{"http://ca.example/issuer.der"})

	oldFetch := aiaFetchIssuer
	aiaFetchIssuer = func(_ context.Context, rawURL string) ([]*x509.Certificate, error) {
		if rawURL != "http://ca.example/issuer.der" {
			return nil, errors.New("unexpected AIA URL")
		}
		return []*x509.Certificate{issuer.cert}, nil
	}
	defer func() { aiaFetchIssuer = oldFetch }()

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{leaf.cert.Raw},
		PrivateKey:  leaf.key,
		Leaf:        leaf.cert,
	}}}
	server.StartTLS()
	defer server.Close()

	roots := x509.NewCertPool()
	roots.AddCert(root.cert)
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{RootCAs: roots}
	serverAddr := server.Listener.Addr().String()
	base.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, serverAddr)
	}

	client := &http.Client{Transport: newAIATransport(base), Timeout: 5 * time.Second}
	resp, err := client.Get("https://tracker.test/")
	if err != nil {
		t.Fatalf("GET with AIA chain completion failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %s", resp.Status)
	}
}

func TestVerifyPeerCertificatesWithAIARecurses(t *testing.T) {
	root := makeTestCA(t, "Root", nil)
	bridge := makeTestCAWithAIA(t, "Bridge", root, nil)
	issuer := makeTestCAWithAIA(t, "Issuer", bridge, []string{"http://ca.example/bridge.der"})
	leaf := makeTestLeaf(t, "tracker.test", issuer, []string{"http://ca.example/issuer.der"})

	roots := x509.NewCertPool()
	roots.AddCert(root.cert)
	fetches := 0
	chains, err := verifyPeerCertificatesWithAIA([]*x509.Certificate{leaf.cert}, "tracker.test", roots, func(_ context.Context, rawURL string) ([]*x509.Certificate, error) {
		fetches++
		switch rawURL {
		case "http://ca.example/issuer.der":
			return []*x509.Certificate{issuer.cert}, nil
		case "http://ca.example/bridge.der":
			return []*x509.Certificate{bridge.cert}, nil
		default:
			return nil, errors.New("unexpected AIA URL")
		}
	})
	if err != nil {
		t.Fatalf("recursive AIA verification failed: %v", err)
	}
	if fetches != 2 {
		t.Fatalf("expected 2 AIA fetches, got %d", fetches)
	}
	if len(chains) == 0 || len(chains[0]) != 4 {
		t.Fatalf("expected leaf+issuer+bridge+root chain, got %#v", chains)
	}
}

func TestVerifyPeerCertificatesWithAIAStillChecksHostname(t *testing.T) {
	root := makeTestCA(t, "Root", nil)
	issuer := makeTestCA(t, "Issuer", root)
	leaf := makeTestLeaf(t, "tracker.test", issuer, []string{"http://ca.example/issuer.der"})
	roots := x509.NewCertPool()
	roots.AddCert(root.cert)

	_, err := verifyPeerCertificatesWithAIA([]*x509.Certificate{leaf.cert}, "other.test", roots, func(_ context.Context, _ string) ([]*x509.Certificate, error) {
		return []*x509.Certificate{issuer.cert}, nil
	})
	if err == nil {
		t.Fatal("expected hostname verification error")
	}
	var hostnameErr x509.HostnameError
	if !errors.As(err, &hostnameErr) {
		t.Fatalf("expected x509.HostnameError, got %T: %v", err, err)
	}
}

func TestSafeAIAIPRejectsLocalNetworks(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "::1", "fc00::1", "fe80::1"} {
		if safeAIAIP(net.ParseIP(raw)) {
			t.Errorf("expected %s to be rejected", raw)
		}
	}
	if !safeAIAIP(net.ParseIP("1.1.1.1")) {
		t.Error("expected public address to be accepted")
	}
}

func makeTestCA(t *testing.T, name string, parent *testCert) *testCert {
	return makeTestCAWithAIA(t, name, parent, nil)
}

func makeTestCAWithAIA(t *testing.T, name string, parent *testCert, aia []string) *testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IssuingCertificateURL: aia,
	}
	parentCert := tmpl
	parentKey := key
	if parent != nil {
		parentCert = parent.cert
		parentKey = parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCert{cert: cert, key: key}
}

func makeTestLeaf(t *testing.T, dnsName string, issuer *testCert, aia []string) *testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: dnsName},
		DNSNames:              []string{dnsName},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IssuingCertificateURL: aia,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer.cert, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCert{cert: cert, key: key}
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	limit := new(big.Int).Lsh(big.NewInt(1), 120)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatal(err)
	}
	return serial
}
