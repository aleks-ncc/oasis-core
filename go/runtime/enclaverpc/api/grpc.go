package api

import (
	"context"
	"fmt"

	"google.golang.org/grpc"

	"github.com/oasislabs/oasis-core/go/common"
	cmnGrpc "github.com/oasislabs/oasis-core/go/common/grpc"
)

var (
	errInvalidRequestType = fmt.Errorf("invalid request type")

	serviceNameBase = "EnclaveRPC"

	methodCallEnclaveName = "CallEnclave"
)

// EnclaveRPC is the enclave rpc gRPC service.
type EnclaveRPC struct {
	// ServiceName is the EnclaveRPC service name.
	ServiceName cmnGrpc.ServiceName
	// MethodCallEnclave is the EnclaveRPC CallEnclave method description.
	MethodCallEnclave *cmnGrpc.MethodDesc
	// ServiceDesc is the EnclaveRPC gRPC service descriptor.
	ServiceDesc grpc.ServiceDesc
}

func (e *EnclaveRPC) handlerCallEnclave( // nolint: golint
	srv interface{},
	ctx context.Context,
	dec func(interface{}) error,
	interceptor grpc.UnaryServerInterceptor,
) (interface{}, error) {
	var req CallEnclaveRequest
	if err := dec(&req); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(Transport).CallEnclave(ctx, &req)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: e.MethodCallEnclave.FullName(),
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(Transport).CallEnclave(ctx, req.(*CallEnclaveRequest))
	}
	return interceptor(ctx, &req, info, handler)
}

// RegisterService registers a new EnclaveRPC transport service with the given gRPC server.
func (e *EnclaveRPC) RegisterService(server *grpc.Server, service Transport) {
	server.RegisterService(&e.ServiceDesc, service)
}

// EnclaveRPC creates a new EnclaveRPC service.
func New(serviceNamePrefix string, accessControl func(req interface{}) bool) *EnclaveRPC {
	serviceName := cmnGrpc.NewServiceName(serviceNamePrefix + "." + serviceNameBase)

	methodCallEnclave := serviceName.NewMethod(methodCallEnclaveName, CallEnclaveRequest{}).
		WithNamespaceExtractor(func(req interface{}) (common.Namespace, error) {
			r, ok := req.(*CallEnclaveRequest)
			if !ok {
				return common.Namespace{}, errInvalidRequestType
			}
			return r.RuntimeID, nil
		}).
		WithAccessControl(accessControl)

	erpc := &EnclaveRPC{
		ServiceName:       serviceName,
		MethodCallEnclave: methodCallEnclave,
		ServiceDesc: grpc.ServiceDesc{
			ServiceName: string(serviceName),
			HandlerType: (*Transport)(nil),
			Methods:     []grpc.MethodDesc{},
			Streams:     []grpc.StreamDesc{},
		},
	}

	erpc.ServiceDesc.Methods = []grpc.MethodDesc{
		{
			MethodName: methodCallEnclave.ShortName(),
			Handler:    erpc.handlerCallEnclave,
		},
	}

	return erpc
}

type transportClient struct {
	conn    *grpc.ClientConn
	service *EnclaveRPC
}

func (c *transportClient) CallEnclave(ctx context.Context, request *CallEnclaveRequest) ([]byte, error) {
	var rsp []byte
	if err := c.conn.Invoke(ctx, c.service.MethodCallEnclave.FullName(), request, &rsp); err != nil {
		return nil, err
	}
	return rsp, nil
}

// NewTransportClient creates a new gRPC EnclaveRPC transport client service.
func NewTransportClient(service *EnclaveRPC, c *grpc.ClientConn) Transport {
	return &transportClient{c, service}
}
