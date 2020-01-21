package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"sync"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/balancer/roundrobin"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/resolver"

	"github.com/oasislabs/oasis-core/go/common"
	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
	cmnGrpc "github.com/oasislabs/oasis-core/go/common/grpc"
	"github.com/oasislabs/oasis-core/go/common/grpc/resolver/manual"
	"github.com/oasislabs/oasis-core/go/common/identity"
	"github.com/oasislabs/oasis-core/go/common/logging"
	"github.com/oasislabs/oasis-core/go/common/node"
	registry "github.com/oasislabs/oasis-core/go/registry/api"
	scheduler "github.com/oasislabs/oasis-core/go/scheduler/api"
	storage "github.com/oasislabs/oasis-core/go/storage/api"
)

// DialOptionForNode creates a grpc.DialOption for communicating under the node's certificate.
func DialOptionForNode(ourCerts []tls.Certificate, node *node.Node) (grpc.DialOption, error) {
	certPool := x509.NewCertPool()
	for _, addr := range node.Committee.Addresses {
		nodeCert, err := addr.ParseCertificate()
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse node's address certificate")
		}
		certPool.AddCert(nodeCert)
	}

	creds := credentials.NewTLS(&tls.Config{
		Certificates: ourCerts,
		RootCAs:      certPool,
		ServerName:   identity.CommonName,
	})
	return grpc.WithTransportCredentials(creds), nil
}

// DialNode opens a grpc.ClientConn to a node.
func DialNode(node *node.Node, opts grpc.DialOption) (*grpc.ClientConn, func(), error) {
	manualResolver, address, cleanupCb := manual.NewManualResolver()

	conn, err := cmnGrpc.Dial(address, opts, grpc.WithBalancerName(roundrobin.Name)) //nolint: staticcheck
	if err != nil {
		cleanupCb()
		return nil, nil, errors.Wrap(err, "failed dialing node")
	}
	var resolverState resolver.State
	for _, addr := range node.Committee.Addresses {
		resolverState.Addresses = append(resolverState.Addresses, resolver.Address{Addr: addr.String()})
	}
	manualResolver.UpdateState(resolverState)

	return conn, cleanupCb, nil
}

type storageWatcher interface {
	getConnectedNodes() []*node.Node
	getClientStates() []clientState
	cleanup()
	initialized() <-chan struct{}
}

// debugWatcherState is a state with a fixed storage node.
type debugWatcherState struct {
	clientState *clientState
	initCh      chan struct{}
}

func (w *debugWatcherState) getConnectedNodes() []*node.Node {
	return []*node.Node{}
}

func (w *debugWatcherState) getClientStates() []clientState {
	return []clientState{*w.clientState}
}
func (w *debugWatcherState) cleanup() {
}
func (w *debugWatcherState) initialized() <-chan struct{} {
	return w.initCh
}

func newDebugWatcher(state *clientState) storageWatcher {
	initCh := make(chan struct{})
	close(initCh)
	return &debugWatcherState{
		initCh:      initCh,
		clientState: state,
	}
}

// watcherState contains storage watcher state.
type watcherState struct {
	sync.RWMutex

	logger *logging.Logger

	scheduler scheduler.Backend
	registry  registry.Backend

	runtimeID common.Namespace

	identity *identity.Identity

	registeredStorageNodes []*node.Node
	scheduledNodes         map[signature.PublicKey]bool
	clientStates           []*clientState

	initCh       chan struct{}
	signaledInit bool
}

// clientState contains information about a connected storage node.
type clientState struct {
	node              *node.Node
	client            storage.Backend
	conn              *grpc.ClientConn
	resolverCleanupCb func()
}

func (w *watcherState) cleanup() {
	w.Lock()
	defer w.Unlock()

	for _, clientState := range w.clientStates {
		if callBack := clientState.resolverCleanupCb; callBack != nil {
			callBack()
		}
		if clientState.conn != nil {
			clientState.conn.Close()
		}
	}
}

func (w *watcherState) initialized() <-chan struct{} {
	return w.initCh
}

func (w *watcherState) getConnectedNodes() []*node.Node {
	w.RLock()
	defer w.RUnlock()

	connectedNodes := []*node.Node{}
	for _, state := range w.clientStates {
		connectedNodes = append(connectedNodes, state.node)
	}
	return connectedNodes
}

func (w *watcherState) getClientStates() []clientState {
	w.RLock()
	defer w.RUnlock()
	clientStates := []clientState{}
	for _, state := range w.clientStates {
		clientStates = append(clientStates, *state)
	}
	return clientStates
}
func (w *watcherState) updateStorageNodeConnections() {
	// XXX: This lock blocks requests to nodes for this runtime.
	w.Lock()
	defer w.Unlock()

	w.logger.Debug("updating connections to storage nodes")

	nodeList := []*node.Node{}
	for _, node := range w.registeredStorageNodes {
		if w.scheduledNodes[node.ID] {
			nodeList = append(nodeList, node)
		}
	}

	// TODO: Should we only update connections if keys or addresses have changed?

	// Clean-up previous resolvers and connections.
	for _, states := range w.clientStates {
		if cleanup := states.resolverCleanupCb; cleanup != nil {
			cleanup()
		}
		if states.conn != nil {
			states.conn.Close()
		}
	}
	w.clientStates = nil

	connClientStates := []*clientState{}
	numConnNodes := 0

	// Connect to nodes.
	for _, node := range nodeList {
		// But prevent connecting to self.
		if w.identity != nil && node.ID.Equal(w.identity.NodeSigner.Public()) {
			continue
		}

		var err error
		opts, err := DialOptionForNode([]tls.Certificate{*w.identity.TLSCertificate}, node)
		if err != nil {
			w.logger.Error("failed to get GRPC dial options for storage committee member",
				"member", node,
				"err", err,
			)
			continue
		}

		if len(node.Committee.Addresses) == 0 {
			w.logger.Error("cannot update connection, storage committee member does not have any addresses",
				"member", node,
			)
			continue
		}

		conn, cleanupCb, err := DialNode(node, opts)
		if err != nil {
			w.logger.Error("cannot update connection",
				"node", node,
				"err", err,
			)
			continue
		}

		numConnNodes++
		connClientStates = append(connClientStates, &clientState{
			node:              node,
			client:            storage.NewStorageClient(conn),
			conn:              conn,
			resolverCleanupCb: cleanupCb,
		})
		w.logger.Debug("storage node connection updated",
			"node", node,
		)
	}
	if numConnNodes == 0 {
		w.logger.Error("failed to connect to any of the storage committee members",
			"nodes", nodeList,
		)
		return
	}

	if !w.signaledInit {
		w.signaledInit = true
		close(w.initCh)
	}

	// Update client state.
	w.clientStates = connClientStates
}

func (w *watcherState) updateRegisteredStorageNodes(nodes []*node.Node) {
	storageNodes := []*node.Node{}
	for _, n := range nodes {
		if n.HasRoles(node.RoleStorageWorker) {
			storageNodes = append(storageNodes, n)
		}
	}

	w.Lock()
	defer w.Unlock()
	w.registeredStorageNodes = storageNodes
}

func (w *watcherState) updateScheduledNodes(nodes []*scheduler.CommitteeNode) {
	scheduledStorageNodes := make(map[signature.PublicKey]bool)
	for _, n := range nodes {
		if n.Role == scheduler.Worker {
			scheduledStorageNodes[n.PublicKey] = true
		}
	}

	w.Lock()
	defer w.Unlock()
	w.scheduledNodes = scheduledStorageNodes
}

func (w *watcherState) watch(ctx context.Context) {
	committeeCh, sub, err := w.scheduler.WatchCommittees(ctx)
	if err != nil {
		w.logger.Error("failed to watch committees",
			"err", err,
		)
		return
	}
	defer sub.Close()

	nodeListCh, nodeListSub, err := w.registry.WatchNodeList(ctx)
	if err != nil {
		w.logger.Error("failed to watch node lists",
			"err", err,
		)
		return
	}
	defer nodeListSub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case nl := <-nodeListCh:
			if nl == nil {
				continue
			}
			w.logger.Debug("got new storage node list",
				"nodes", nl.Nodes,
			)

			w.updateRegisteredStorageNodes(nl.Nodes)

			// Update storage node connections for the runtime.
			w.updateStorageNodeConnections()

			w.logger.Debug("updated connections to all nodes")
		case committee := <-committeeCh:
			if committee.RuntimeID != w.runtimeID {
				continue
			}
			if committee.Kind != scheduler.KindStorage {
				continue
			}

			w.logger.Debug("worker: storage committee for epoch",
				"committee", committee,
				"epoch", committee.ValidFor,
				"kind", committee.Kind,
			)

			if len(committee.Members) == 0 {
				w.logger.Warn("worker: received empty storage committee")
				continue
			}

			// Update connection if watching the runtime.
			w.updateScheduledNodes(committee.Members)

			// Update storage node connections for the runtime.
			w.updateStorageNodeConnections()

			w.logger.Debug("updated connections to nodes")
		}
	}
}

func newWatcher(
	ctx context.Context,
	runtimeID common.Namespace,
	identity *identity.Identity,
	schedulerBackend scheduler.Backend,
	registryBackend registry.Backend,
) storageWatcher {
	logger := logging.GetLogger("storage/client/watcher").With("runtime_id", runtimeID.String())

	watcher := &watcherState{
		initCh:                 make(chan struct{}),
		logger:                 logger,
		runtimeID:              runtimeID,
		identity:               identity,
		scheduler:              schedulerBackend,
		registry:               registryBackend,
		registeredStorageNodes: []*node.Node{},
		scheduledNodes:         make(map[signature.PublicKey]bool),
		clientStates:           []*clientState{},
	}

	go watcher.watch(ctx)

	return watcher
}
