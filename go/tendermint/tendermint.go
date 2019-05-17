package tendermint

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	tmabci "github.com/tendermint/tendermint/abci/types"
	tmconfig "github.com/tendermint/tendermint/config"
	tmlog "github.com/tendermint/tendermint/libs/log"
	tmpubsub "github.com/tendermint/tendermint/libs/pubsub"
	tmnode "github.com/tendermint/tendermint/node"
	tmp2p "github.com/tendermint/tendermint/p2p"
	tmpex "github.com/tendermint/tendermint/p2p/pex"
	tmpriv "github.com/tendermint/tendermint/privval"
	tmproxy "github.com/tendermint/tendermint/proxy"
	tmcli "github.com/tendermint/tendermint/rpc/client"
	tmrpctypes "github.com/tendermint/tendermint/rpc/core/types"
	tmtypes "github.com/tendermint/tendermint/types"

	"github.com/oasislabs/ekiden/go/common"
	"github.com/oasislabs/ekiden/go/common/cbor"
	"github.com/oasislabs/ekiden/go/common/crypto/signature"
	"github.com/oasislabs/ekiden/go/common/identity"
	"github.com/oasislabs/ekiden/go/common/json"
	"github.com/oasislabs/ekiden/go/common/logging"
	"github.com/oasislabs/ekiden/go/common/pubsub"
	cmservice "github.com/oasislabs/ekiden/go/common/service"
	"github.com/oasislabs/ekiden/go/genesis"
	"github.com/oasislabs/ekiden/go/genesis/bootstrap"
	"github.com/oasislabs/ekiden/go/tendermint/abci"
	"github.com/oasislabs/ekiden/go/tendermint/api"
	"github.com/oasislabs/ekiden/go/tendermint/db/bolt"
	"github.com/oasislabs/ekiden/go/tendermint/internal/crypto"
	"github.com/oasislabs/ekiden/go/tendermint/service"
)

const (
	configDir = "config"

	cfgCoreListenAddress   = "tendermint.core.listen_address"
	cfgCoreExternalAddress = "tendermint.core.external_address"

	cfgConsensusTimeoutCommit      = "tendermint.consensus.timeout_commit"
	cfgConsensusSkipTimeoutCommit  = "tendermint.consensus.skip_timeout_commit"
	cfgConsensusEmptyBlockInterval = "tendermint.consensus.empty_block_interval"

	cfgABCIPruneStrategy = "tendermint.abci.prune.strategy"
	cfgABCIPruneNumKept  = "tendermint.abci.prune.num_kept"

	cfgP2PSeeds    = "tendermint.seeds"
	cfgP2PSeedMode = "tendermint.seed_mode"

	cfgLogDebug = "tendermint.log.debug"

	cfgDebugBootstrapNodeName   = "tendermint.debug.bootstrap.node_name"
	cfgDebugBootstrapQuerySeeds = "tendermint.debug.bootstrap.query_seeds"
	cfgDebugP2PAddrBookLenient  = "tendermint.debug.addr_book_lenient"
)

var (
	_ service.TendermintService = (*tendermintService)(nil)
)

type tendermintService struct {
	sync.Mutex

	cmservice.BaseBackgroundService

	ctx           context.Context
	mux           *abci.ApplicationServer
	node          *tmnode.Node
	client        tmcli.Client
	blockNotifier *pubsub.Broker

	genesis                  genesis.Provider
	nodeKey                  *signature.PrivateKey
	dataDir                  string
	isInitialized, isStarted bool
	startedCh                chan struct{}
	syncedCh                 chan struct{}

	startFn func() error
}

func (t *tendermintService) initialized() bool {
	t.Lock()
	defer t.Unlock()

	return t.isInitialized
}

func (t *tendermintService) started() bool {
	t.Lock()
	defer t.Unlock()

	return t.isStarted
}

func (t *tendermintService) Start() error {
	if t.started() {
		return errors.New("tendermint: service already started")
	}

	switch t.initialized() {
	case true:
		if err := t.mux.Start(); err != nil {
			return err
		}
		if err := t.startFn(); err != nil {
			return err
		}
		if err := t.node.Start(); err != nil {
			return errors.Wrap(err, "tendermint: failed to start service")
		}
		go t.syncWorker()
		go t.worker()
	case false:
		close(t.syncedCh)
	}

	t.Lock()
	t.isStarted = true
	t.Unlock()

	close(t.startedCh)

	return nil
}

func (t *tendermintService) Quit() <-chan struct{} {
	if !t.started() {
		return make(chan struct{})
	}

	return t.node.Quit()
}

func (t *tendermintService) Stop() {
	if !t.initialized() || !t.started() {
		return
	}

	if err := t.node.Stop(); err != nil {
		t.Logger.Error("Error on stopping node", err)
	}

	t.mux.Stop()
	t.node.Wait()
}

func (t *tendermintService) Started() <-chan struct{} {
	return t.startedCh
}

func (t *tendermintService) Synced() <-chan struct{} {
	return t.syncedCh
}

func (t *tendermintService) RegisterGenesisHook(hook func()) {
	if !t.initialized() {
		return
	}

	t.mux.RegisterGenesisHook(hook)
}

func (t *tendermintService) BroadcastTx(tag byte, tx interface{}) error {
	message := cbor.Marshal(tx)
	data := append([]byte{tag}, message...)

	response, err := t.client.BroadcastTxCommit(data)
	if err != nil {
		return errors.Wrap(err, "broadcast tx: commit failed")
	}

	if response.CheckTx.Code != api.CodeOK.ToInt() {
		return fmt.Errorf("broadcast tx: check tx failed: %s", response.CheckTx.Info)
	}
	if response.DeliverTx.Code != api.CodeOK.ToInt() {
		return fmt.Errorf("broadcast tx: deliver tx failed: %s", response.DeliverTx.Info)
	}

	return nil
}

func (t *tendermintService) Query(path string, query interface{}, height int64) ([]byte, error) {
	var data []byte
	if query != nil {
		data = cbor.Marshal(query)
	}

	// We submit queries directly to our application instance as going through
	// tendermint's local client enforces a global mutex for all application
	// requests, blocking queries from within the application itself.
	//
	// This is safe to do as long as all application query handlers only access
	// state through the immutable tree.
	request := tmabci.RequestQuery{
		Data:   data,
		Path:   path,
		Height: height,
		Prove:  false,
	}
	response := t.mux.Mux().Query(request)

	if response.GetCode() != api.CodeOK.ToInt() {
		return nil, fmt.Errorf("query: failed (code=%s)", api.Code(response.GetCode()))
	}

	return response.GetValue(), nil
}

func (t *tendermintService) Subscribe(subscriber string, query tmpubsub.Query) (tmtypes.Subscription, error) {
	// Note: The tendermint documentation claims using SubscribeUnbuffered can
	// freeze the server, however, the buffered Subscribe can drop events, and
	// force-unsubscribe the channel if processing takes too long.

	subFn := func() (tmtypes.Subscription, error) {
		return t.node.EventBus().SubscribeUnbuffered(t.ctx, subscriber, query)
	}

	if t.started() {
		return subFn()
	}

	// The node doesn't exist until it's started since, creating the node
	// triggers replay, InitChain, and etc.
	t.Logger.Debug("Subscribe: node not available yet, blocking",
		"subscriber", subscriber,
		"query", query,
	)

	// XXX/yawning: As far as I can tell just blocking here is safe as
	// ever single consumer of the API subscribes from a go routine.
	<-t.startedCh

	return subFn()
}

func (t *tendermintService) Unsubscribe(subscriber string, query tmpubsub.Query) error {
	if t.started() {
		return t.node.EventBus().Unsubscribe(t.ctx, subscriber, query)
	}

	return errors.New("tendermint: unsubscribe called with no backing service")
}

func (t *tendermintService) IsSeed() bool {
	// XXX: Probably should properly check and not rely on the flag.
	return viper.GetBool(cfgP2PSeedMode)
}

func (t *tendermintService) Pruner() abci.StatePruner {
	return t.mux.Pruner()
}

func (t *tendermintService) RegisterApplication(app abci.Application) error {
	if err := t.ForceInitialize(); err != nil {
		return err
	}
	if t.started() {
		return errors.New("tendermint: service already started")
	}

	return t.mux.Register(app)
}

func (t *tendermintService) ForceInitialize() error {
	t.Lock()
	defer t.Unlock()

	var err error
	if !t.isInitialized {
		t.Logger.Debug("Initializing tendermint local node.")
		err = t.lazyInit()
	}

	return err
}

func (t *tendermintService) GetBlock(height int64) (*tmtypes.Block, error) {
	if t.client == nil {
		panic("client not available yet")
	}

	result, err := t.client.Block(&height)
	if err != nil {
		return nil, errors.Wrap(err, "tendermint: block query failed")
	}

	return result.Block, nil
}

func (t *tendermintService) GetBlockResults(height int64) (*tmrpctypes.ResultBlockResults, error) {
	result, err := t.client.BlockResults(&height)
	if err != nil {
		return nil, errors.Wrap(err, "tendermint: block results query failed")
	}

	return result, nil
}

func (t *tendermintService) WatchBlocks() (<-chan *tmtypes.Block, *pubsub.Subscription) {
	typedCh := make(chan *tmtypes.Block)
	sub := t.blockNotifier.Subscribe()
	sub.Unwrap(typedCh)

	return typedCh, sub
}

func (t *tendermintService) NodeKey() *signature.PublicKey {
	pk := t.nodeKey.Public()
	return &pk
}

func (t *tendermintService) lazyInit() error {
	if t.isInitialized {
		return nil
	}

	var err error

	// Create Tendermint application mux.
	var pruneCfg abci.PruneConfig
	pruneStrat := viper.GetString(cfgABCIPruneStrategy)
	if err = pruneCfg.Strategy.FromString(pruneStrat); err != nil {
		return err
	}
	pruneNumKept := int64(viper.GetInt(cfgABCIPruneNumKept))
	pruneCfg.NumKept = pruneNumKept

	t.mux, err = abci.NewApplicationServer(t.ctx, t.dataDir, &pruneCfg)
	if err != nil {
		return err
	}

	// Tendermint needs the on-disk directories to be present when
	// launched like this, so create the relevant sub-directories
	// under the ekiden DataDir.
	tendermintDataDir := filepath.Join(t.dataDir, "tendermint")
	if err = initDataDir(tendermintDataDir); err != nil {
		return err
	}

	// Create Tendermint node.
	tenderConfig := tmconfig.DefaultConfig()
	_ = viper.Unmarshal(&tenderConfig)
	tenderConfig.SetRoot(tendermintDataDir)
	timeoutCommit := viper.GetDuration(cfgConsensusTimeoutCommit)
	emptyBlockInterval := viper.GetDuration(cfgConsensusEmptyBlockInterval)
	tenderConfig.Consensus.TimeoutCommit = timeoutCommit
	tenderConfig.Consensus.SkipTimeoutCommit = viper.GetBool(cfgConsensusSkipTimeoutCommit)
	tenderConfig.Consensus.CreateEmptyBlocks = true
	tenderConfig.Consensus.CreateEmptyBlocksInterval = emptyBlockInterval
	tenderConfig.Instrumentation.Prometheus = true
	tenderConfig.Instrumentation.PrometheusListenAddr = ""
	tenderConfig.TxIndex.Indexer = "null"
	tenderConfig.P2P.ListenAddress = viper.GetString(cfgCoreListenAddress)
	tenderConfig.P2P.ExternalAddress = viper.GetString(cfgCoreExternalAddress)
	tenderConfig.P2P.AllowDuplicateIP = true // HACK: e2e tests need this.
	tenderConfig.P2P.SeedMode = viper.GetBool(cfgP2PSeedMode)
	// Seed Ids need to be Lowecase as p2p/transport.go:MultiplexTransport.upgrade()
	// uses a case sensitive string comparision to validate public keys
	// Since Seeds is expected to be in comma-delimited id@host:port format,
	// lowercasing the whole string is ok.
	tenderConfig.P2P.Seeds = strings.ToLower(viper.GetString(cfgP2PSeeds))
	tenderConfig.P2P.AddrBookStrict = !viper.GetBool(cfgDebugP2PAddrBookLenient)
	tenderConfig.RPC.ListenAddress = ""

	tendermintPV := tmpriv.LoadOrGenFilePV(tenderConfig.PrivValidatorKeyFile(), tenderConfig.PrivValidatorStateFile())
	tenderValIdent := crypto.PrivateKeyToTendermint(t.nodeKey)
	if !tenderValIdent.Equals(tendermintPV.Key.PrivKey) {
		// The private validator must have been just generated.  Force
		// it to use the oasis identity rather than the new key.
		t.Logger.Debug("fixing up tendermint private validator identity")
		tendermintPV.Key.PrivKey = tenderValIdent
		tendermintPV.Key.PubKey = tenderValIdent.PubKey()
		tendermintPV.Key.Address = tendermintPV.Key.PubKey.Address()
		tendermintPV.Save()
	}

	tmGenDoc, err := t.getGenesis(tenderConfig)
	if err != nil {
		t.Logger.Error("failed to obtain genesis document",
			"err", err,
		)
		return err
	}
	tenderminGenesisProvider := func() (*tmtypes.GenesisDoc, error) {
		return tmGenDoc, nil
	}

	// HACK: tmnode.NewNode() triggers block replay and or ABCI chain
	// initialization, instead of t.node.Start().  This is a problem
	// because at the time that lazyInit() is called, none of the ABCI
	// applications are registered.
	//
	// Defer actually initializing the node till after everything
	// else is setup.
	t.startFn = func() error {
		t.node, err = tmnode.NewNode(tenderConfig,
			tendermintPV,
			&tmp2p.NodeKey{PrivKey: crypto.PrivateKeyToTendermint(t.nodeKey)},
			tmproxy.NewLocalClientCreator(t.mux.Mux()),
			tenderminGenesisProvider,
			bolt.BoltDBProvider,
			tmnode.DefaultMetricsProvider(tenderConfig.Instrumentation),
			newLogAdapter(!viper.GetBool(cfgLogDebug)),
		)
		if err != nil {
			return errors.Wrap(err, "tendermint: failed to create node")
		}
		t.client = tmcli.NewLocal(t.node)

		return nil
	}

	t.isInitialized = true

	return nil
}

// genesisToTendermint converts the Ekiden genesis block to tendermint's format.
func genesisToTendermint(d *genesis.Document) (*tmtypes.GenesisDoc, error) {
	// NOTE: The AppState MUST be encoded as JSON since its type is json.RawMessage
	//       which requires it to be valid JSON. It may appear to work until you
	//       try to restore from an existing data directory.
	doc := tmtypes.GenesisDoc{
		ChainID:         "0xa515",
		GenesisTime:     d.Time,
		ConsensusParams: tmtypes.DefaultConsensusParams(),
		AppState:        json.Marshal(d),
	}

	var tmValidators []tmtypes.GenesisValidator
	for _, v := range d.Validators {
		pk := crypto.PublicKeyToTendermint(&v.PubKey)
		validator := tmtypes.GenesisValidator{
			Address: pk.Address(),
			PubKey:  pk,
			Power:   v.Power,
			Name:    v.Name,
		}
		tmValidators = append(tmValidators, validator)
	}

	doc.Validators = tmValidators

	return &doc, nil
}

func (t *tendermintService) getGenesis(tenderConfig *tmconfig.Config) (*tmtypes.GenesisDoc, error) {
	if bs, ok := t.genesis.(*bootstrap.Provider); ok {
		t.Logger.Warn("The bootstrap provisioning server is NOT FOR PRODUCTION USE.")

		var (
			nodeAddr   = viper.GetString(cfgCoreExternalAddress)
			nodeName   = viper.GetString(cfgDebugBootstrapNodeName)
			querySeeds = viper.GetBool(cfgDebugBootstrapQuerySeeds)
			err        error
		)

		if nodeName != "" {
			t.Logger.Debug("registering as a validator with the bootstrap server",
				"node_name", nodeName,
			)

			// Register as a validator node with the bootstrap server.
			if err = common.IsAddrPort(nodeAddr); err != nil {
				return nil, errors.Wrap(err, "tendermint: malformed bootstrap validator node address")
			}
			if err = common.IsFQDN(nodeName); err != nil {
				return nil, errors.Wrap(err, "tendermint: malformed bootstrap validator node name")
			}

			validator := &genesis.Validator{
				PubKey:      t.nodeKey.Public(),
				Name:        common.NormalizeFQDN(nodeName),
				CoreAddress: nodeAddr,
			}
			if err = bs.RegisterValidator(validator); err != nil {
				return nil, errors.Wrap(err, "tendermint: validator bootstrap failed")
			}

			t.Logger.Debug("registered as a validator")
		}

		// Register itself as a seed node to the bootstrap server.
		if t.IsSeed() {
			t.Logger.Debug("registering as a seed node with the bootstrap server")

			if err = common.IsAddrPort(nodeAddr); err != nil {
				return nil, errors.Wrap(err, "tendermint: malformed bootstrap seed node address")
			}

			seed := &bootstrap.SeedNode{
				PubKey:      t.nodeKey.Public(),
				CoreAddress: nodeAddr,
			}
			if err = bs.RegisterSeed(seed); err != nil {
				return nil, errors.Wrap(err, "tendermint: seed bootstrap failed")
			}

			t.Logger.Debug("registered as a seed node")
		}
		// Query seed nodes from the bootstrap server
		if querySeeds {
			t.Logger.Debug("querying seeds")
			seeds, err := bs.GetSeeds()
			if err != nil {
				return nil, errors.Wrap(err, "tendermint: getting bootstrap seeds failed")
			}
			for _, seed := range seeds {
				tmPub := crypto.PublicKeyToTendermint(&seed.PubKey)
				seedIDLower := strings.ToLower(tmPub.Address().String())
				seedID := fmt.Sprintf("%s@%s", seedIDLower, seed.CoreAddress)

				tenderConfig.P2P.Seeds = tenderConfig.P2P.Seeds + "," + seedID
				tenderConfig.P2P.Seeds = strings.TrimLeft(tenderConfig.P2P.Seeds, ",")
			}
			t.Logger.Debug("done querying seeds", "seeds", tenderConfig.P2P.Seeds)
		}
	}

	doc, err := t.genesis.GetGenesisDocument()
	if err != nil {
		return nil, errors.Wrap(err, "tendermint: failed to get genesis doc")
	}

	tmGenDoc, err := genesisToTendermint(doc)
	if err != nil {
		return nil, errors.Wrap(err, "tendermint: failed to create genesis doc")
	}

	if t.IsSeed() {
		// Add validators to seed nodes address books
		//
		// For extra fun, p2p/transport.go:MultiplexTransport.upgrade() uses a case
		// sensitive string comparision to validate public keys.
		var addrs []*tmp2p.NetAddress
		for _, v := range doc.Validators {
			vPubKey := crypto.PublicKeyToTendermint(&v.PubKey)
			vPkAddrHex := strings.ToLower(vPubKey.Address().String())
			vAddr := vPkAddrHex + "@" + v.CoreAddress

			if v.PubKey.Equal(t.nodeKey.Public()) {
				// This validator entry is the current node, set the
				// node name to that specified in the genesis document.
				tenderConfig.Moniker = v.Name
				continue
			}

			tmVAddr, err := tmp2p.NewNetAddressString(vAddr)
			if err != nil {
				return nil, errors.Wrap(err, "tendermint: failed to reformat genesis validator address")
			}

			addrs = append(addrs, tmVAddr)
		}

		addrBook := tmpex.NewAddrBook(tenderConfig.P2P.AddrBookFile(), tenderConfig.P2P.AddrBookStrict)
		if err := addrBook.Start(); err != nil {
			return nil, errors.Wrap(err, "tendermint: failed to open address book")
		}
		defer func() {
			// Can't pass tmn.NewNode() an existing address book.
			// Make sure to call Save as the address book may otherwise not be saved
			// due to the way Stop/Quit are broken in the address book implementation.
			addrBook.Save()
			ch := addrBook.Quit()
			_ = addrBook.Stop()
			<-ch
		}()

		// Add our address to the address book, just so we can add the genesis
		// nodes.  This is somewhat silly, but there isn't any error-checking
		// done with AddOurAddress, so using the P2P ListenAddress as the IP/port
		// is totally fine.
		valPubKey := t.nodeKey.Public()
		ourPubKey := crypto.PublicKeyToTendermint(&valPubKey)
		ourLaddr, err := common.GetHostPort(tenderConfig.P2P.ListenAddress)
		if err != nil {
			return nil, errors.Wrap(err, "tendermint: failed to parse p2p listen address")
		}

		ourPkAddrHex := strings.ToLower(ourPubKey.Address().String())
		ourAddr, err := tmp2p.NewNetAddressString(ourPkAddrHex + "@" + ourLaddr)
		if err != nil {
			return nil, errors.Wrap(err, "tendermint: failed to generate our address")
		}
		addrBook.AddOurAddress(ourAddr)

		// Populate the address book with the genesis validators.
		for _, v := range addrs {
			// Remove the address first as otherwise Tendermint's address book
			// may not actually add the new address.
			addrBook.RemoveAddress(v)

			if err = addrBook.AddAddress(v, ourAddr); err != nil {
				return nil, errors.Wrap(err, "tendermint: failed to add genesis validator to address book")
			}
		}
	}

	// HACK: Certain test cases use TimeoutCommit < 1 sec, and care about the
	// BFT view of time pulling ahead.
	timeoutCommit := viper.GetDuration(cfgConsensusTimeoutCommit)
	tmGenDoc.ConsensusParams.Block.TimeIotaMs = int64(timeoutCommit / time.Millisecond)

	return tmGenDoc, nil
}

func (t *tendermintService) syncWorker() {
	checkSyncFn := func() (isSyncing bool, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errors.New("tendermint: node disappeared, terminated?")
			}
		}()

		return t.node.ConsensusReactor().FastSync(), nil
	}

	for {
		select {
		case <-t.node.Quit():
			return
		case <-time.After(1 * time.Second):
			isSyncing, err := checkSyncFn()
			if err != nil {
				t.Logger.Error("Failed to poll FastSync",
					"err", err,
				)
				return
			}
			if !isSyncing {
				t.Logger.Info("Tendermint Node finished fast-sync")
				close(t.syncedCh)
				return
			}
		}
	}
}

func (t *tendermintService) worker() {
	// Subscribe to other events here as needed, no need to spawn additional
	// workers.
	sub, err := t.Subscribe("tendermint/worker", tmtypes.EventQueryNewBlock)
	if err != nil {
		t.Logger.Error("worker: failed to subscribe to new block events",
			"err", err,
		)
		return
	}
	defer t.Unsubscribe("tendermint/worker", tmtypes.EventQueryNewBlock) // nolint:errcheck

	for {
		select {
		case <-t.node.Quit():
			return
		case <-sub.Cancelled():
			return
		case v := <-sub.Out():
			ev := v.Data().(tmtypes.EventDataNewBlock)
			t.blockNotifier.Broadcast(ev.Block)
		}
	}
}

// New creates a new Tendermint service.
func New(ctx context.Context, dataDir string, identity *identity.Identity, genesis genesis.Provider) service.TendermintService {
	return &tendermintService{
		BaseBackgroundService: *cmservice.NewBaseBackgroundService("tendermint"),
		blockNotifier:         pubsub.NewBroker(false),
		nodeKey:               identity.NodeKey,
		genesis:               genesis,
		ctx:                   ctx,
		dataDir:               dataDir,
		startedCh:             make(chan struct{}),
		syncedCh:              make(chan struct{}),
	}
}

func initDataDir(dataDir string) error {
	subDirs := []string{
		configDir,
		"data", // Required by `tendermint/privval/FilePV.Save()`.
	}

	if err := common.Mkdir(dataDir); err != nil {
		return err
	}

	for _, subDir := range subDirs {
		if err := common.Mkdir(filepath.Join(dataDir, subDir)); err != nil {
			return err
		}
	}

	return nil
}

type logAdapter struct {
	*logging.Logger

	baseLogger    *logging.Logger
	suppressDebug bool

	keyVals []interface{}
}

func (a *logAdapter) With(keyvals ...interface{}) tmlog.Logger {
	// Tendermint uses `module` like ekiden does, and to add insult to
	// injury will cave off child loggers with subsequence calls to
	// `With()`, resulting in multiple `module` keys.
	//
	// Do the right thing by:
	//  * Prefixing the `module` values with `tendermint:`
	//  * Coallece the multiple `module` values.
	//
	// This is more convoluted than it needs to be because the kit-log
	// prefix vector is private.

	findModule := func(vec []interface{}) (string, int) {
		for i, v := range vec {
			if i&1 != 0 {
				continue
			}

			k := v.(string)
			if k != "module" {
				continue
			}
			if i+1 > len(vec) {
				panic("With(): tendermint core logger, missing 'module' value")
			}

			vv := vec[i+1].(string)

			return vv, i + 1
		}
		return "", -1
	}

	parentMod, parentIdx := findModule(a.keyVals)

	childKeyVals := append([]interface{}{}, a.keyVals...)
	childMod, childIdx := findModule(keyvals)
	if childIdx < 0 {
		// "module" was not specified for this child, use the one belonging
		// to the parent.
		if parentIdx < 0 {
			// This should *NEVER* happen, if it does, it means that tendermint
			// called `With()` on the base logAdapter without setting a module.
			panic("With(): tendermint core logger, no sensible parent 'module'")
		}
		childKeyVals = append(childKeyVals, keyvals...)
	} else if parentIdx < 0 {
		// No parent logger, this must be a child of the base logAdapter.
		keyvals[childIdx] = "tendermint:" + childMod
		childKeyVals = append(childKeyVals, keyvals...)
	} else {
		// Append the child's module to the parent's.
		childKeyVals[parentIdx] = parentMod + "/" + childMod
		for i, v := range keyvals {
			// And omit the non-re=written key/value from the those passed to
			// the kit-log logger.
			if i != childIdx-1 && i != childIdx {
				childKeyVals = append(childKeyVals, v)
			}
		}
	}

	return &logAdapter{
		Logger:        a.baseLogger.With(childKeyVals...),
		baseLogger:    a.baseLogger,
		suppressDebug: a.suppressDebug,
		keyVals:       childKeyVals,
	}
}

func (a *logAdapter) Info(msg string, keyvals ...interface{}) {
	a.Logger.Info(msg, keyvals...)
}

func (a *logAdapter) Error(msg string, keyvals ...interface{}) {
	a.Logger.Error(msg, keyvals...)
}

func (a *logAdapter) Debug(msg string, keyvals ...interface{}) {
	if !a.suppressDebug {
		a.Logger.Debug(msg, keyvals...)
	}
}

func newLogAdapter(suppressDebug bool) tmlog.Logger {
	// Need an extra level of unwinding because the Debug wrapper
	// exists.
	//
	// This might be able to be replaced with the per-module log
	// level instead.
	return &logAdapter{
		Logger:        logging.GetLoggerEx("tendermint:base", 1),
		baseLogger:    logging.GetLoggerEx("", 1), // Tendermint sets the module, repeatedly.
		suppressDebug: suppressDebug,
	}
}

// RegisterFlags registers the configuration flags with the provided
// command.
func RegisterFlags(cmd *cobra.Command) {
	if !cmd.Flags().Parsed() {
		cmd.Flags().String(cfgCoreListenAddress, "tcp://0.0.0.0:26656", "tendermint core listen address")
		cmd.Flags().String(cfgCoreExternalAddress, "", "tendermint address advertised to other nodes")
		cmd.Flags().Duration(cfgConsensusTimeoutCommit, 1*time.Second, "tendermint commit timeout")
		cmd.Flags().Bool(cfgConsensusSkipTimeoutCommit, false, "skip tendermint commit timeout")
		cmd.Flags().Duration(cfgConsensusEmptyBlockInterval, 0*time.Second, "tendermint empty block interval")
		cmd.Flags().String(cfgABCIPruneStrategy, abci.PruneDefault, "ABCI state pruning strategy")
		cmd.Flags().Int64(cfgABCIPruneNumKept, 3600, "ABCI state versions kept (when applicable)")
		cmd.Flags().Bool(cfgP2PSeedMode, false, "run the tendermint node in seed mode")
		cmd.Flags().String(cfgP2PSeeds, "", "comma-delimited id@host:port tendermint seed nodes")
		cmd.Flags().Bool(cfgLogDebug, false, "enable tendermint debug logs (very verbose)")
		cmd.Flags().String(cfgDebugBootstrapNodeName, "", "debug bootstrap validator node name")
		cmd.Flags().Bool(cfgDebugBootstrapQuerySeeds, false, "if true, query bootstrap server for seed nodes")
		cmd.Flags().Bool(cfgDebugP2PAddrBookLenient, false, "allow non-routable addresses")
	}

	for _, v := range []string{
		cfgCoreListenAddress,
		cfgCoreExternalAddress,
		cfgConsensusTimeoutCommit,
		cfgConsensusSkipTimeoutCommit,
		cfgConsensusEmptyBlockInterval,
		cfgABCIPruneStrategy,
		cfgABCIPruneNumKept,
		cfgP2PSeedMode,
		cfgP2PSeeds,
		cfgLogDebug,
		cfgDebugBootstrapNodeName,
		cfgDebugBootstrapQuerySeeds,
		cfgDebugP2PAddrBookLenient,
	} {
		viper.BindPFlag(v, cmd.Flags().Lookup(v)) // nolint: errcheck
	}
}
