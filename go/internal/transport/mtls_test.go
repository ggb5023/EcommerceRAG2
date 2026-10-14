package transport

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestMutualTLSRejectsUntrustedPeersAndUnsafeKeys(t *testing.T) {
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	raw, _ := x509.MarshalPKCS8PrivateKey(key)
	for name, data := range map[string][]byte{"ca": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), "cert": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("V2_RPC_TLS", "mtls")
	t.Setenv("V2_RPC_CA_FILE", filepath.Join(dir, "ca"))
	t.Setenv("V2_RPC_CERT_FILE", filepath.Join(dir, "cert"))
	t.Setenv("V2_RPC_KEY_FILE", filepath.Join(dir, "key"))
	config, err := Config()
	if err != nil {
		t.Fatal(err)
	}
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(config)))
	healthpb.RegisterHealthServer(server, health.NewServer())
	go server.Serve(listener)
	defer server.Stop()
	testCall := func(config credentials.TransportCredentials, want bool) {
		t.Helper()
		conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(config))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
		if (err == nil) != want {
			t.Fatalf("authenticated call success=%v want=%v", err == nil, want)
		}
	}
	testCall(credentials.NewTLS(config.Clone()), true)
	missing := config.Clone()
	missing.Certificates = nil
	testCall(credentials.NewTLS(missing), false)
	untrusted := config.Clone()
	untrusted.RootCAs = x509.NewCertPool()
	testCall(credentials.NewTLS(untrusted), false)
	os.Chmod(filepath.Join(dir, "key"), 0644)
	if _, err := Config(); err == nil {
		t.Fatal("unsafe key accepted")
	}
	t.Setenv("V2_RPC_TLS", "unknown")
	if _, err := ClientCredentials(); err == nil {
		t.Fatal("transport fallback accepted")
	}
}
