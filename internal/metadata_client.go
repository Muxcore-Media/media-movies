package internal

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	metadatav1 "github.com/Muxcore-Media/contracts-metadata/muxcore/metadata/v1"
)

func (m *Module) metadataClient(ctx context.Context) (metadatav1.MetadataServiceClient, func(), error) {
	if m.metadataClientFn != nil {
		return m.metadataClientFn(ctx)
	}
	metaAddr, err := m.findMetadataAddr(ctx)
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(metaAddr, meshGRPCDialOpts()...)
	if err != nil {
		return nil, nil, fmt.Errorf("dial metadata: %w", err)
	}
	return metadatav1.NewMetadataServiceClient(conn), func() { _ = conn.Close() }, nil
}
