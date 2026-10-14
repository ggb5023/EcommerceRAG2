// Package transport supplies the authenticated internal RPC transport.
package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"syscall"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func Config() (*tls.Config, error) {
	if os.Getenv("V2_RPC_TLS") != "mtls" {
		return nil, errors.New("mTLS must be explicitly enabled")
	}
	info, err := os.Lstat(os.Getenv("V2_RPC_KEY_FILE"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, errors.New("RPC key must be an owned regular 0600 file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return nil, errors.New("RPC key owner invalid")
	}
	cert, err := tls.LoadX509KeyPair(os.Getenv("V2_RPC_CERT_FILE"), os.Getenv("V2_RPC_KEY_FILE"))
	if err != nil {
		return nil, errors.New("RPC certificate unavailable")
	}
	ca, err := os.ReadFile(os.Getenv("V2_RPC_CA_FILE"))
	if err != nil {
		return nil, errors.New("RPC trust unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("RPC trust invalid")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}, nil
}
func ClientCredentials() (credentials.TransportCredentials, error) {
	if os.Getenv("V2_RPC_TLS") == "" {
		return insecure.NewCredentials(), nil
	}
	config, err := Config()
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(config), nil
}
