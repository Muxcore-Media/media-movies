package internal

import (
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
)

func newMetadataTestClient(t *testing.T, srv metadatav1.MetadataServiceServer) (metadatav1.MetadataServiceClient, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	metadatav1.RegisterMetadataServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return metadatav1.NewMetadataServiceClient(conn), func() {
		gs.Stop()
		_ = conn.Close()
	}
}
