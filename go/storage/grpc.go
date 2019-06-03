package storage

import (
	"context"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/oasislabs/ekiden/go/common/crypto/hash"
	"github.com/oasislabs/ekiden/go/storage/api"

	pb "github.com/oasislabs/ekiden/go/grpc/storage"
)

var _ pb.StorageServer = (*grpcServer)(nil)

type grpcServer struct {
	backend api.Backend
}

func (s *grpcServer) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	id := req.GetId()
	if len(id) != api.KeySize {
		return nil, errors.New("storage:Get: empty key")
	}

	var k api.Key
	copy(k[:], id)
	// TODO: should these waits be here, or should the clients be updated to wait?
	<-s.backend.Initialized()
	v, err := s.backend.Get(ctx, k)
	if err == api.ErrKeyNotFound || err == api.ErrKeyExpired {
		return nil, status.Errorf(codes.NotFound, err.Error())
	} else if err != nil {
		return nil, err
	}

	return &pb.GetResponse{Data: v}, nil
}

func (s *grpcServer) GetBatch(ctx context.Context, req *pb.GetBatchRequest) (*pb.GetBatchResponse, error) {
	var keys []api.Key
	for _, id := range req.GetIds() {
		if len(id) != api.KeySize {
			return nil, errors.New("storage:GetBatch: empty key")
		}

		var k api.Key
		copy(k[:], id)
		keys = append(keys, k)
	}

	<-s.backend.Initialized()
	values, err := s.backend.GetBatch(ctx, keys)
	if err != nil {
		return nil, err
	}
	return &pb.GetBatchResponse{Data: values}, nil
}

func (s *grpcServer) GetReceipt(ctx context.Context, req *pb.GetReceiptRequest) (*pb.GetReceiptResponse, error) {
	var keys []api.Key
	for _, id := range req.GetIds() {
		if len(id) != api.KeySize {
			return nil, errors.New("storage:GetReceipt: empty key")
		}

		var k api.Key
		copy(k[:], id)
		keys = append(keys, k)
	}

	<-s.backend.Initialized()
	signed, err := s.backend.GetReceipt(ctx, keys)
	if err != nil {
		return nil, err
	}

	return &pb.GetReceiptResponse{Data: signed.MarshalCBOR()}, nil
}

func (s *grpcServer) Insert(ctx context.Context, req *pb.InsertRequest) (*pb.InsertResponse, error) {
	<-s.backend.Initialized()
	if err := s.backend.Insert(ctx, req.GetData(), req.GetExpiry(), api.InsertOptions{}); err != nil {
		return nil, err
	}
	return &pb.InsertResponse{}, nil
}

func (s *grpcServer) InsertBatch(ctx context.Context, req *pb.InsertBatchRequest) (*pb.InsertBatchResponse, error) {
	var values []api.Value
	for _, item := range req.GetItems() {
		values = append(values, api.Value{
			Data:       item.GetData(),
			Expiration: item.GetExpiry(),
		})
	}

	<-s.backend.Initialized()
	if err := s.backend.InsertBatch(ctx, values, api.InsertOptions{}); err != nil {
		return nil, err
	}

	return &pb.InsertBatchResponse{}, nil
}

func (s *grpcServer) GetKeys(req *pb.GetKeysRequest, stream pb.Storage_GetKeysServer) error {
	<-s.backend.Initialized()
	ch, err := s.backend.GetKeys(stream.Context())
	if err != nil {
		return err
	}

	for {
		var ki *api.KeyInfo
		var ok bool

		select {
		case ki, ok = <-ch:
		case <-stream.Context().Done():
		}
		if !ok {
			break
		}

		resp := &pb.GetKeysResponse{
			Key:    ki.Key[:],
			Expiry: uint64(ki.Expiration),
		}
		if err := stream.Send(resp); err != nil {
			return err
		}
	}

	return nil
}

func (s *grpcServer) Apply(ctx context.Context, req *pb.ApplyRequest) (*pb.ApplyResponse, error) {
	var root hash.Hash
	if err := root.UnmarshalBinary(req.GetRoot()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal root")
	}

	var expectedNewRoot hash.Hash
	if err := expectedNewRoot.UnmarshalBinary(req.GetExpectedNewRoot()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal expected new root")
	}

	var log api.WriteLog
	for _, item := range req.GetLog() {
		log = append(log, api.LogEntry{
			Key:   item.GetKey(),
			Value: item.GetValue(),
		})
	}

	<-s.backend.Initialized()
	signedReceipt, err := s.backend.Apply(ctx, root, expectedNewRoot, log)

	if err != nil {
		return nil, err
	}

	return &pb.ApplyResponse{Receipt: signedReceipt.MarshalCBOR()}, nil
}

func (s *grpcServer) ApplyBatch(ctx context.Context, req *pb.ApplyBatchRequest) (*pb.ApplyBatchResponse, error) {
	var ops []api.ApplyOp
	for _, op := range req.GetOps() {
		var root hash.Hash
		if err := root.UnmarshalBinary(op.GetRoot()); err != nil {
			return nil, errors.Wrap(err, "storage: failed to unmarshal root")
		}

		var expectedNewRoot hash.Hash
		if err := expectedNewRoot.UnmarshalBinary(op.GetExpectedNewRoot()); err != nil {
			return nil, errors.Wrap(err, "storage: failed to unmarshal expected new root")
		}

		var log api.WriteLog
		for _, item := range op.GetLog() {
			log = append(log, api.LogEntry{
				Key:   item.GetKey(),
				Value: item.GetValue(),
			})
		}

		ops = append(ops, api.ApplyOp{
			Root:            root,
			ExpectedNewRoot: expectedNewRoot,
			WriteLog:        log,
		})
	}

	<-s.backend.Initialized()
	signedReceipt, err := s.backend.ApplyBatch(ctx, ops)

	if err != nil {
		return nil, err
	}

	return &pb.ApplyBatchResponse{Receipt: signedReceipt.MarshalCBOR()}, nil
}

func (s *grpcServer) GetSubtree(ctx context.Context, req *pb.GetSubtreeRequest) (*pb.GetSubtreeResponse, error) {
	var root hash.Hash
	if err := root.UnmarshalBinary(req.GetRoot()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal root")
	}

	maxDepth := uint8(req.GetMaxDepth())

	nid := req.GetId()
	nodeID := api.NodeID{
		Path:  api.MKVSKey(nid.GetPath()),
		Depth: uint8(nid.GetDepth()),
	}

	<-s.backend.Initialized()
	subtree, err := s.backend.GetSubtree(ctx, root, nodeID, maxDepth)
	if err != nil {
		return nil, err
	}

	serializedSubtree, err := subtree.MarshalBinary()
	if err != nil {
		return nil, err
	}

	return &pb.GetSubtreeResponse{Subtree: serializedSubtree}, nil
}

func (s *grpcServer) GetPath(ctx context.Context, req *pb.GetPathRequest) (*pb.GetPathResponse, error) {
	var root hash.Hash
	if err := root.UnmarshalBinary(req.GetRoot()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal root")
	}

	var key api.MKVSKey
	if err := key.UnmarshalBinary(req.GetKey()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal key")
	}

	startDepth := uint8(req.GetStartDepth())

	<-s.backend.Initialized()
	subtree, err := s.backend.GetPath(ctx, root, key, startDepth)
	if err != nil {
		return nil, err
	}

	serializedSubtree, err := subtree.MarshalBinary()
	if err != nil {
		return nil, err
	}

	return &pb.GetPathResponse{Subtree: serializedSubtree}, nil
}

func (s *grpcServer) GetNode(ctx context.Context, req *pb.GetNodeRequest) (*pb.GetNodeResponse, error) {
	var root hash.Hash
	if err := root.UnmarshalBinary(req.GetRoot()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal root")
	}

	nid := req.GetId()
	var path api.MKVSKey
	if err := path.UnmarshalBinary(nid.GetPath()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal id")
	}

	nodeID := api.NodeID{
		Path:  path,
		Depth: uint8(nid.GetDepth()),
	}

	<-s.backend.Initialized()
	node, err := s.backend.GetNode(ctx, root, nodeID)
	if err != nil {
		return nil, err
	}

	serializedNode, err := node.MarshalBinary()
	if err != nil {
		return nil, err
	}

	return &pb.GetNodeResponse{Node: serializedNode}, nil
}

func (s *grpcServer) GetValue(ctx context.Context, req *pb.GetValueRequest) (*pb.GetValueResponse, error) {
	var root hash.Hash
	if err := root.UnmarshalBinary(req.GetRoot()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal root")
	}

	var id hash.Hash
	if err := id.UnmarshalBinary(req.GetId()); err != nil {
		return nil, errors.Wrap(err, "storage: failed to unmarshal id")
	}

	<-s.backend.Initialized()
	value, err := s.backend.GetValue(ctx, root, id)
	if err != nil {
		return nil, err
	}

	return &pb.GetValueResponse{Value: value}, nil
}

// NewGRPCServer intializes and registers a grpc storage server backed
// by the provided Backend.
func NewGRPCServer(srv *grpc.Server, b api.Backend) {
	s := &grpcServer{
		backend: b,
	}

	pb.RegisterStorageServer(srv, s)
}
