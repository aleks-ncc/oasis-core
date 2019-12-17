package grpc

import (
	"context"

	"google.golang.org/grpc"
)

type ServiceAuthFunc interface {
	AuthFunc(ctx context.Context, fullMethodName string, req interface{}) (context.Context, error)
}

// TODO: authStreamInterceptor.
func authUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// XXX: this is for POC. in prod. we should require all endpoints to define the ServiceAuthFunc
		// (endpoints without auth would have an "allow-all" policy)
		overrideSrv, ok := info.Server.(ServiceAuthFunc)
		if !ok {
			// No authentication.
			return handler(ctx, req)
		}
		// Otherwise enforce it.
		ctx, err := overrideSrv.AuthFunc(ctx, info.FullMethod, req)
		if err != nil {
			return nil, err
		}

		return handler(ctx, req)
	}
}
