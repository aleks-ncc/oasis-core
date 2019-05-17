// Package node implements the ekiden node.
package node

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/oasislabs/ekiden/go/beacon"
	beaconAPI "github.com/oasislabs/ekiden/go/beacon/api"
	"github.com/oasislabs/ekiden/go/client"
	"github.com/oasislabs/ekiden/go/common/crash"
	"github.com/oasislabs/ekiden/go/common/grpc"
	"github.com/oasislabs/ekiden/go/common/identity"
	"github.com/oasislabs/ekiden/go/common/logging"
	"github.com/oasislabs/ekiden/go/dummydebug"
	cmdCommon "github.com/oasislabs/ekiden/go/ekiden/cmd/common"
	"github.com/oasislabs/ekiden/go/ekiden/cmd/common/background"
	cmdGrpc "github.com/oasislabs/ekiden/go/ekiden/cmd/common/grpc"
	"github.com/oasislabs/ekiden/go/ekiden/cmd/common/metrics"
	"github.com/oasislabs/ekiden/go/ekiden/cmd/common/pprof"
	"github.com/oasislabs/ekiden/go/ekiden/cmd/common/tracing"
	"github.com/oasislabs/ekiden/go/epochtime"
	epochtimeAPI "github.com/oasislabs/ekiden/go/epochtime/api"
	"github.com/oasislabs/ekiden/go/genesis"
	"github.com/oasislabs/ekiden/go/ias"
	"github.com/oasislabs/ekiden/go/keymanager"
	"github.com/oasislabs/ekiden/go/registry"
	registryAPI "github.com/oasislabs/ekiden/go/registry/api"
	"github.com/oasislabs/ekiden/go/roothash"
	roothashAPI "github.com/oasislabs/ekiden/go/roothash/api"
	"github.com/oasislabs/ekiden/go/scheduler"
	schedulerAPI "github.com/oasislabs/ekiden/go/scheduler/api"
	"github.com/oasislabs/ekiden/go/staking"
	stakingAPI "github.com/oasislabs/ekiden/go/staking/api"
	"github.com/oasislabs/ekiden/go/storage"
	storageAPI "github.com/oasislabs/ekiden/go/storage/api"
	"github.com/oasislabs/ekiden/go/tendermint"
	"github.com/oasislabs/ekiden/go/tendermint/service"
	workerCommon "github.com/oasislabs/ekiden/go/worker/common"
	"github.com/oasislabs/ekiden/go/worker/compute"
	"github.com/oasislabs/ekiden/go/worker/registration"
	workerStorage "github.com/oasislabs/ekiden/go/worker/storage"
)

// Run runs the ekiden node.
func Run(cmd *cobra.Command, args []string) {
	// Re-register flags due to https://github.com/spf13/viper/issues/233.
	RegisterFlags(cmd)

	node, err := NewNode()
	if err != nil {
		return
	}
	defer node.Cleanup()

	node.Wait()
}

// Node is the ekiden node service.
//
// WARNING: This is exposed for the benefit of tests and the interface
// is not guaranteed to be stable.
type Node struct {
	svcMgr       *background.ServiceManager
	grpcInternal *grpc.Server
	grpcExternal *grpc.Server
	svcTmnt      service.TendermintService

	Genesis    genesis.Provider
	Identity   *identity.Identity
	Beacon     beaconAPI.Backend
	Epochtime  epochtimeAPI.Backend
	Registry   registryAPI.Backend
	RootHash   roothashAPI.Backend
	Scheduler  schedulerAPI.Backend
	Staking    stakingAPI.Backend
	Storage    storageAPI.Backend
	IAS        *ias.IAS
	Client     *client.Client
	KeyManager *keymanager.KeyManager

	ComputeWorker      *compute.Worker
	StorageWorker      *workerStorage.Storage
	WorkerRegistration *registration.Registration
}

// Cleanup cleans up after the node has terminated.
func (n *Node) Cleanup() {
	n.svcMgr.Cleanup()
}

// Stop gracefully terminates the node.
func (n *Node) Stop() {
	n.svcMgr.Stop()
}

// Wait waits for the node to gracefully terminate.  Callers MUST
// call Cleanup() after wait returns.
func (n *Node) Wait() {
	n.svcMgr.Wait()
}

func (n *Node) initBackends() error {
	dataDir := cmdCommon.DataDir()

	var err error

	// Initialize the various backends.
	if n.Epochtime, err = epochtime.New(n.svcMgr.Ctx, n.svcTmnt); err != nil {
		return err
	}
	if n.Beacon, err = beacon.New(n.svcMgr.Ctx, n.Epochtime, n.svcTmnt); err != nil {
		return err
	}
	if n.Registry, err = registry.New(n.svcMgr.Ctx, n.Epochtime, n.svcTmnt); err != nil {
		return err
	}
	n.svcMgr.RegisterCleanupOnly(n.Registry, "registry backend")
	if n.Staking, err = staking.New(n.svcMgr.Ctx, n.svcTmnt); err != nil {
		return err
	}
	n.svcMgr.RegisterCleanupOnly(n.Staking, "staking backend")
	if n.Scheduler, err = scheduler.New(n.svcMgr.Ctx, n.Epochtime, n.Registry, n.Beacon, n.svcTmnt); err != nil {
		return err
	}
	n.svcMgr.RegisterCleanupOnly(n.Scheduler, "scheduler backend")

	if n.Storage, err = storage.New(n.svcMgr.Ctx, dataDir, n.Epochtime, n.Scheduler, n.Registry, n.Identity.NodeKey); err != nil {
		return err
	}
	n.svcMgr.RegisterCleanupOnly(n.Storage, "storage backend")
	if n.RootHash, err = roothash.New(n.svcMgr.Ctx, dataDir, n.Epochtime, n.Scheduler, n.Registry, n.Beacon, n.svcTmnt); err != nil {
		return err
	}
	n.svcMgr.RegisterCleanupOnly(n.RootHash, "roothash backend")

	// Initialize and register the internal gRPC services.
	grpcSrv := n.grpcInternal.Server()
	registry.NewGRPCServer(grpcSrv, n.Registry)
	roothash.NewGRPCServer(grpcSrv, n.RootHash)
	scheduler.NewGRPCServer(grpcSrv, n.Scheduler)
	storage.NewGRPCServer(grpcSrv, n.Storage)
	dummydebug.NewGRPCServer(grpcSrv, n.Epochtime, n.Registry)

	cmdCommon.Logger().Debug("backends initialized")

	return nil
}

func (n *Node) initAndStartWorkers(logger *logging.Logger) error {
	dataDir := cmdCommon.DataDir()

	var err error

	workerCommonCfg, err := workerCommon.NewConfig()
	if err != nil {
		return err
	}

	// Create externally-accessible gRPC server.
	n.grpcExternal, err = grpc.NewServerTCP("external", workerCommonCfg.ClientPort, n.Identity.TLSCertificate)
	if err != nil {
		return err
	}
	n.svcMgr.Register(n.grpcExternal)

	// Initialize the worker registration.
	n.WorkerRegistration, err = registration.New(
		dataDir,
		n.Epochtime,
		n.Registry,
		n.Identity,
		n.svcTmnt,
		workerCommonCfg,
	)
	if err != nil {
		return err
	}
	n.svcMgr.Register(n.WorkerRegistration)

	// Initialize the storage worker.
	n.StorageWorker, err = workerStorage.New(
		n.Epochtime,
		n.Storage,
		n.grpcExternal,
		n.WorkerRegistration,
		n.svcTmnt,
		n.Genesis,
	)
	if err != nil {
		return err
	}
	n.svcMgr.Register(n.StorageWorker)

	// Initialize the compute worker.
	n.ComputeWorker, err = compute.New(
		dataDir,
		n.IAS,
		n.grpcExternal,
		n.Identity,
		n.Storage,
		n.RootHash,
		n.Registry,
		n.Epochtime,
		n.Scheduler,
		n.svcTmnt,
		n.KeyManager,
		n.WorkerRegistration,
		workerCommonCfg,
	)
	if err != nil {
		return err
	}
	n.svcMgr.Register(n.ComputeWorker)

	// Start the storage worker.
	if err = n.StorageWorker.Start(); err != nil {
		return err
	}

	// Start the compute worker.
	if err = n.ComputeWorker.Start(); err != nil {
		return err
	}

	// Start the worker registration service.
	if err = n.WorkerRegistration.Start(); err != nil {
		return err
	}

	// Only start the external gRPC server if any workers enabled
	if n.StorageWorker.Enabled() || n.ComputeWorker.Enabled() {
		if err = n.grpcExternal.Start(); err != nil {
			logger.Error("failed to start external gRPC server",
				"err", err,
			)
			return err
		}
	}

	return nil
}

// NewNode initializes and launches the ekiden node service.
//
// WARNING: This will misbehave iff cmd != RootCommand().  This is exposed
// for the benefit of tests and the interface is not guaranteed to be stable.
func NewNode() (*Node, error) {
	logger := cmdCommon.Logger()

	node := &Node{
		svcMgr: background.NewServiceManager(logger),
	}

	var startOk bool
	defer func() {
		if !startOk {
			node.svcMgr.Stop()
			node.Cleanup()
		}
	}()

	if err := cmdCommon.Init(); err != nil {
		// Common stuff like logger not correcty initialized. Print to stderr
		_, _ = fmt.Fprintln(os.Stderr, err)
		return nil, err
	}

	dataDir := cmdCommon.DataDir()
	if dataDir == "" {
		logger.Error("data directory not configured")
		return nil, errors.New("data directory not configured")
	}

	// Load configured values for all registered crash points.
	crash.LoadViperArgValues()

	var err error

	// Generate/Load the node identity.
	node.Identity, err = identity.LoadOrGenerate(dataDir)
	if err != nil {
		logger.Error("failed to load/generate identity",
			"err", err,
		)
		return nil, err
	}

	logger.Info("loaded/generated node identity",
		"public_key", node.Identity.NodeKey.Public(),
	)

	// Initialize the tracing client.
	tracingSvc, err := tracing.New("ekiden-node")
	if err != nil {
		logger.Error("failed to initialize tracing",
			"err", err,
		)
		return nil, err
	}
	node.svcMgr.RegisterCleanupOnly(tracingSvc, "tracing")

	// Initialize the internal gRPC server.
	// Depends on global tracer.
	node.grpcInternal, err = cmdGrpc.NewServerLocal()
	if err != nil {
		logger.Error("failed to initialize gRPC server",
			"err", err,
		)
		return nil, err
	}
	node.svcMgr.Register(node.grpcInternal)

	// Initialize the metrics server.
	metrics, err := metrics.New(node.svcMgr.Ctx)
	if err != nil {
		logger.Error("failed to initialize metrics server",
			"err", err,
		)
		return nil, err
	}
	node.svcMgr.Register(metrics)

	// Initialize the profiling server.
	profiling, err := pprof.New(node.svcMgr.Ctx)
	if err != nil {
		logger.Error("failed to initialize pprof server",
			"err", err,
		)
		return nil, err
	}
	node.svcMgr.Register(profiling)

	// Start the profiling server.
	if err = profiling.Start(); err != nil {
		logger.Error("failed to start pprof server",
			"err", err,
		)
		return nil, err
	}

	// Initialize the genesis provider.
	genesis, err := genesis.New(node.Identity)
	if err != nil {
		logger.Error("failed to initialize the genesis provider",
			"err", err,
		)
		return nil, err
	}
	node.Genesis = genesis

	// Initialize tendermint.
	node.svcTmnt = tendermint.New(node.svcMgr.Ctx, dataDir, node.Identity, node.Genesis)
	node.svcMgr.Register(node.svcTmnt)

	if node.svcTmnt.IsSeed() {
		// Tendermint nodes in seed mode crawl the network for
		// peers. In case of incoming connections seed node will
		// share some of the peers and immediately disconnect.
		// Because of that only start Tendermint service in case
		// were operating as it would be useless running a full
		// node.
		logger.Info("starting tendermint seed node")

		// Ensure that tendermint service is started even if no
		// backends require it.
		if err = node.svcTmnt.ForceInitialize(); err != nil {
			logger.Error("failed to initialize tendermint service",
				"err", err,
			)
			return nil, err
		}

		// Start the tendermint service.
		if err = node.svcTmnt.Start(); err != nil {
			logger.Error("failed to start tendermint service",
				"err", err,
			)
			return nil, err
		}

		startOk = true

		return node, nil
	}

	logger.Info("starting ekiden node")

	// Initialize the various node backends.
	if err = node.initBackends(); err != nil {
		logger.Error("failed to initialize backends",
			"err", err,
		)
		return nil, err
	}

	// Initialize the IAS proxy client.
	node.IAS, err = ias.New(node.Identity)
	if err != nil {
		logger.Error("failed to initialize IAS proxy client",
			"err", err,
		)
		return nil, err
	}

	// Initialize the key manager service.
	node.KeyManager, err = keymanager.New(
		cmdCommon.DataDir(),
		node.IAS,
		node.Identity,
		node.Storage,
	)
	if err != nil {
		logger.Error("failed to initialize key manager",
			"err", err,
		)
		return nil, err
	}
	node.svcMgr.Register(node.KeyManager)

	// Initialize the client.
	node.Client, err = client.New(
		node.svcMgr.Ctx,
		cmdCommon.DataDir(),
		node.RootHash,
		node.Storage,
		node.Scheduler,
		node.Registry,
		node.svcTmnt,
		node.KeyManager,
	)
	if err != nil {
		return nil, err
	}
	node.svcMgr.RegisterCleanupOnly(node.Client, "client service")
	client.NewGRPCServer(node.grpcInternal.Server(), node.Client)

	// Start metric server.
	if err = metrics.Start(); err != nil {
		logger.Error("failed to start metric server",
			"err", err,
		)
		return nil, err
	}

	// Initialize and Start ekiden workers
	if err = node.initAndStartWorkers(logger); err != nil {
		logger.Error("failed to initialize workers",
			"err", err,
		)
		return nil, err
	}

	// Start the tendermint service.
	//
	// Note: This will only start the node if it is required by
	// one of the backends.
	if err = node.svcTmnt.Start(); err != nil {
		logger.Error("failed to start tendermint service",
			"err", err,
		)
		return nil, err
	}

	// Start the internal gRPC server.
	if err = node.grpcInternal.Start(); err != nil {
		logger.Error("failed to start internal gRPC server",
			"err", err,
		)
		return nil, err
	}

	// Start the key manager service.
	if err = node.KeyManager.Start(); err != nil {
		logger.Error("failed to start key manager service",
			"err", err,
		)
		return nil, err
	}

	logger.Info("initialization complete: ready to serve")
	startOk = true

	return node, nil
}

// RegisterFlags registers the flags used by the node command.
func RegisterFlags(cmd *cobra.Command) {
	// Backend initialization flags.
	for _, v := range []func(*cobra.Command){
		metrics.RegisterFlags,
		tracing.RegisterFlags,
		cmdGrpc.RegisterServerLocalFlags,
		pprof.RegisterFlags,
		genesis.RegisterFlags,
		beacon.RegisterFlags,
		epochtime.RegisterFlags,
		registry.RegisterFlags,
		roothash.RegisterFlags,
		scheduler.RegisterFlags,
		staking.RegisterFlags,
		storage.RegisterFlags,
		tendermint.RegisterFlags,
		ias.RegisterFlags,
		keymanager.RegisterFlags,
		client.RegisterFlags,
		compute.RegisterFlags,
		registration.RegisterFlags,
		workerCommon.RegisterFlags,
		workerStorage.RegisterFlags,
		crash.RegisterFlags,
	} {
		v(cmd)
	}
}
