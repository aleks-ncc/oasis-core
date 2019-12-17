package storage

import (
	"context"
	"errors"

	"github.com/oasislabs/oasis-core/go/common"
	"github.com/oasislabs/oasis-core/go/common/accessctl"
	"github.com/oasislabs/oasis-core/go/storage/api"
)

// storageService is the service exposed to external clients via gRPC.
type storageService struct {
	w *Worker

	debugRejectUpdates bool
}

func (s *storageService) AuthFunc(ctx context.Context, fullMethodName string, req interface{}) (context.Context, error) {
	// TODO: if all request implemented a Namespace() interface, this could be
	// extracted into a CheckAccessAllow method call. But in that case, endpoints
	// without any polices defined would fail, so we should refactor existing
	// policies to be explicitly defined for all endpoints (current readonly
	// endpoints don't have any policies defined, and access check is skipped).
	//
	// Also in that case this implementation could be moved into the
	// DynamicRuntimePolicyChecker struct, meaning all GRPC endpoints using it,
	// would automatically get the AuthFunc defined.
	switch r := req.(type) {
	case *api.ApplyRequest:
		return ctx, s.w.grpcPolicy.CheckAccessAllowed(ctx, accessctl.Action(fullMethodName), r.Namespace)
	case *api.ApplyBatchRequest:
		return ctx, s.w.grpcPolicy.CheckAccessAllowed(ctx, accessctl.Action(fullMethodName), r.Namespace)
	case *api.MergeRequest:
		return ctx, s.w.grpcPolicy.CheckAccessAllowed(ctx, accessctl.Action(fullMethodName), r.Namespace)
	case *api.MergeBatchRequest:
		return ctx, s.w.grpcPolicy.CheckAccessAllowed(ctx, accessctl.Action(fullMethodName), r.Namespace)
	case *api.GetDiffRequest:
		return ctx, s.w.grpcPolicy.CheckAccessAllowed(ctx, accessctl.Action(fullMethodName), r.StartRoot.Namespace)
	case *api.GetCheckpointRequest:
		return ctx, s.w.grpcPolicy.CheckAccessAllowed(ctx, accessctl.Action(fullMethodName), r.Root.Namespace)
	default:
		return ctx, nil
	}
}

func (s *storageService) checkUpdateAllowed(ctx context.Context, method string, ns common.Namespace) error {
	if s.debugRejectUpdates {
		return errors.New("storage: rejecting update operations")
	}
	if err := s.w.grpcPolicy.CheckAccessAllowed(ctx, accessctl.Action(method), ns); err != nil {
		return err
	}
	return nil
}

func (s *storageService) ensureInitialized(ctx context.Context) error {
	select {
	case <-s.Initialized():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *storageService) SyncGet(ctx context.Context, request *api.GetRequest) (*api.ProofResponse, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.SyncGet(ctx, request)
}

func (s *storageService) SyncGetPrefixes(ctx context.Context, request *api.GetPrefixesRequest) (*api.ProofResponse, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.SyncGetPrefixes(ctx, request)
}

func (s *storageService) SyncIterate(ctx context.Context, request *api.IterateRequest) (*api.ProofResponse, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.SyncIterate(ctx, request)
}

func (s *storageService) Apply(ctx context.Context, request *api.ApplyRequest) ([]*api.Receipt, error) {
	if err := s.checkUpdateAllowed(ctx, "Apply", request.Namespace); err != nil {
		return nil, err
	}
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.Apply(ctx, request)
}

func (s *storageService) ApplyBatch(ctx context.Context, request *api.ApplyBatchRequest) ([]*api.Receipt, error) {
	if err := s.checkUpdateAllowed(ctx, "ApplyBatch", request.Namespace); err != nil {
		return nil, err
	}
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.ApplyBatch(ctx, request)
}

func (s *storageService) Merge(ctx context.Context, request *api.MergeRequest) ([]*api.Receipt, error) {
	if err := s.checkUpdateAllowed(ctx, "Merge", request.Namespace); err != nil {
		return nil, err
	}
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.Merge(ctx, request)
}

func (s *storageService) MergeBatch(ctx context.Context, request *api.MergeBatchRequest) ([]*api.Receipt, error) {
	if err := s.checkUpdateAllowed(ctx, "MergeBatch", request.Namespace); err != nil {
		return nil, err
	}
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.MergeBatch(ctx, request)
}

func (s *storageService) GetDiff(ctx context.Context, request *api.GetDiffRequest) (api.WriteLogIterator, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.GetDiff(ctx, request)
}

func (s *storageService) GetCheckpoint(ctx context.Context, request *api.GetCheckpointRequest) (api.WriteLogIterator, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.w.commonWorker.Storage.GetCheckpoint(ctx, request)
}

func (s *storageService) Cleanup() {
}

func (s *storageService) Initialized() <-chan struct{} {
	return s.w.commonWorker.Storage.Initialized()
}
