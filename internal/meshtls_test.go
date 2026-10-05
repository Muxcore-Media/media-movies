package internal

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
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Existing lifecycle tests start plaintext servers; the dev flag is explicit
// here and cleared by TestServesMeshTLS.
func TestMain(m *testing.M) {
	_ = os.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "true")
	os.Exit(m.Run())
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// testPKI writes a CA and a leaf (SAN localhost/127.0.0.1) and returns paths.
func testPKI(t *testing.T) (caPath, certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	caPath = filepath.Join(dir, "ca.pem")
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	writePEM(t, caPath, "CERTIFICATE", caDER)
	writePEM(t, certPath, "CERTIFICATE", leafDER)
	writePEM(t, keyPath, "PRIVATE KEY", keyDER)
	return caPath, certPath, keyPath
}

// TestServesMeshTLS starts the real module gRPC server with MUXCORE_TLS_* set
// and checks a TLS client with a certificate connects while plaintext fails.
func TestServesMeshTLS(t *testing.T) {
	caPath, certPath, keyPath := testPKI(t)
	t.Setenv("MUXCORE_INSECURE_DISABLE_TLS", "")
	t.Setenv("MUXCORE_TLS_CA", caPath)
	t.Setenv("MUXCORE_TLS_CERT", certPath)
	t.Setenv("MUXCORE_TLS_KEY", keyPath)

	m := NewModule(Config{
		DBPath:   filepath.Join(t.TempDir(), "mesh.db"),
		ImageDir: filepath.Join(t.TempDir(), "images"),
		HTTPAddr: "127.0.0.1:0",
		GRPCAddr: "127.0.0.1:0",
	})
	ctx := context.Background()
	if err := m.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	addr := m.grpcLis.Addr().String()

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	tlsConn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: pool, ServerName: "localhost", Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tlsConn.Close() }()
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// A TLS client reaches the server (unknown method => Unimplemented).
	err = tlsConn.Invoke(cctx, "/meshtls.test/Probe", &emptypb.Empty{}, &emptypb.Empty{})
	if status.Code(err) == codes.Unavailable {
		t.Fatalf("TLS client could not connect: %v", err)
	}

	plain, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	pctx, pcancel := context.WithTimeout(ctx, 5*time.Second)
	defer pcancel()
	err = plain.Invoke(pctx, "/meshtls.test/Probe", &emptypb.Empty{}, &emptypb.Empty{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("plaintext client must be rejected, got %v", err)
	}
}
