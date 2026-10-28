package checker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rnurdaulet/pingbot/internal/config"
)

func TestCheckSSLWarn(t *testing.T) {
	t.Parallel()

	// TLS checks NotAfter against the wall clock.
	now := time.Now().UTC().Truncate(time.Second)

	tests := []struct {
		name       string
		notAfter   time.Time
		warnDays   int
		skipVerify bool
		wantStatus Status
		wantSubstr string
	}{
		{
			name:       "cert ok outside warn window",
			notAfter:   now.Add(30 * 24 * time.Hour),
			warnDays:   14,
			wantStatus: OK,
		},
		{
			name:       "cert within warn window",
			notAfter:   now.Add(5 * 24 * time.Hour),
			warnDays:   14,
			wantStatus: Slow,
			wantSubstr: "ssl cert expires in 5d",
		},
		{
			name:       "cert expired via checkSSL",
			notAfter:   now.Add(-time.Hour),
			warnDays:   14,
			skipVerify: true, // reach checkSSL after handshake
			wantStatus: Fail,
			wantSubstr: "ssl cert expired",
		},
		{
			name:       "cert expired rejects handshake",
			notAfter:   now.Add(-time.Hour),
			warnDays:   14,
			wantStatus: Fail,
			wantSubstr: "expired",
		},
		{
			name:       "warn disabled",
			notAfter:   now.Add(5 * 24 * time.Hour),
			warnDays:   0,
			wantStatus: OK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newTLSServer(t, tt.notAfter, tt.skipVerify)
			defer srv.Close()

			chk := &Checker{
				client: srv.Client(),
				now:    func() time.Time { return now },
			}
			result := chk.Check(context.Background(), config.Target{
				Name:         "t",
				URL:          srv.URL,
				Method:       "GET",
				ExpectStatus: []int{200},
				Timeout:      config.Duration(5 * time.Second),
				SSLWarnDays:  tt.warnDays,
			})

			if result.Status != tt.wantStatus {
				t.Fatalf("status=%v want=%v err=%q", result.Status, tt.wantStatus, result.Err)
			}
			if tt.wantSubstr != "" && !strings.Contains(result.Err, tt.wantSubstr) {
				t.Fatalf("err=%q want substring %q", result.Err, tt.wantSubstr)
			}
		})
	}
}

func TestCheckSSLWarnPriorityOverLatency(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	srv := newTLSServer(t, now.Add(3*24*time.Hour), false)
	defer srv.Close()

	chk := &Checker{
		client: srv.Client(),
		now:    func() time.Time { return now },
	}
	result := chk.Check(context.Background(), config.Target{
		Name:         "t",
		URL:          srv.URL,
		Method:       "GET",
		ExpectStatus: []int{200},
		Timeout:      config.Duration(5 * time.Second),
		MaxLatencyMS: 1, // force latency slow if cert check skipped
		SSLWarnDays:  14,
	})
	if result.Status != Slow {
		t.Fatalf("status=%v want Slow", result.Status)
	}
	if !strings.Contains(result.Err, "ssl cert expires") {
		t.Fatalf("expected cert reason, got %q", result.Err)
	}
}

func newTLSServer(t *testing.T, notAfter time.Time, skipVerify bool) *httptest.Server {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    notAfter.Add(-365 * 24 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	srv.StartTLS()

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	srv.Client().Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			RootCAs:            pool,
			InsecureSkipVerify: skipVerify,
		},
	}
	return srv
}

func TestCheckFollowRedirects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		follow   bool
		expect   []int
		wantCode int
		wantHit  bool
	}{
		{name: "301 not followed", follow: false, expect: []int{301}, wantCode: 301, wantHit: false},
		{name: "301 followed", follow: true, expect: []int{200}, wantCode: 200, wantHit: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hit := make(chan struct{}, 1)
			final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/landed" {
					http.NotFound(w, r)
					return
				}
				hit <- struct{}{}
				w.WriteHeader(http.StatusOK)
			}))
			defer final.Close()

			redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, final.URL+"/landed", http.StatusMovedPermanently)
			}))
			defer redir.Close()

			follow := tt.follow
			chk := New()
			res := chk.Check(context.Background(), config.Target{
				Name:            "redir",
				URL:             redir.URL,
				Method:          http.MethodGet,
				ExpectStatus:    tt.expect,
				Timeout:         config.Duration(5 * time.Second),
				FollowRedirects: &follow,
			})
			if res.Status != OK || res.StatusCode != tt.wantCode {
				t.Fatalf("status=%v code=%d err=%q", res.Status, res.StatusCode, res.Err)
			}
			select {
			case <-hit:
				if !tt.wantHit {
					t.Fatal("request reached redirect target")
				}
			default:
				if tt.wantHit {
					t.Fatal("request did not reach redirect target")
				}
			}
		})
	}
}
