package internal

import (
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func meshInsecureEnabled() bool {
	return os.Getenv("MUXCORE_INSECURE_DISABLE_TLS") == "true" ||
		os.Getenv("MUXCORE_GRPC_INSECURE") == "true"
}

func meshGRPCDialOpts() []grpc.DialOption {
	if meshInsecureEnabled() {
		return []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	// Production mesh dials use core client TLS; callers that bypass client.Dial
	// fall back to insecure only when explicitly allowed above.
	return []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
}
