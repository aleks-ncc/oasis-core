// Package abci implements the tendermint ABCI application integration.
package abci

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tendermint/iavl"
	"github.com/tendermint/tendermint/abci/types"
	dbm "github.com/tendermint/tm-db"

	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/crypto/hash"
	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
	"github.com/oasislabs/oasis-core/go/common/errors"
	"github.com/oasislabs/oasis-core/go/common/logging"
	"github.com/oasislabs/oasis-core/go/common/pubsub"
	"github.com/oasislabs/oasis-core/go/common/quantity"
	"github.com/oasislabs/oasis-core/go/common/version"
	consensus "github.com/oasislabs/oasis-core/go/consensus/api"
	"github.com/oasislabs/oasis-core/go/consensus/api/transaction"
	"github.com/oasislabs/oasis-core/go/consensus/tendermint/api"
	"github.com/oasislabs/oasis-core/go/consensus/tendermint/db"
	epochtime "github.com/oasislabs/oasis-core/go/epochtime/api"
	genesis "github.com/oasislabs/oasis-core/go/genesis/api"
)

const (
	stateKeyGenesisDigest   = "OasisGenesisDigest"
	stateKeyGenesisRequest  = "OasisGenesisRequest"
	stateKeyInitChainEvents = "OasisInitChainEvents"

	metricsUpdateInterval = 10 * time.Second
)

var (
	abciSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "oasis_abci_db_size",
			Help: "Total size of the ABCI database (MiB)",
		},
	)
	abciCollectors = []prometheus.Collector{
		abciSize,
	}

	metricsOnce sync.Once

	errOversizedTx = fmt.Errorf("mux: oversized transaction")
)

// ApplicationConfig is the configuration for the consensus application.
type ApplicationConfig struct {
	DataDir         string
	Pruning         PruneConfig
	HaltEpochHeight epochtime.EpochTime
	MinGasPrice     uint64
}

// TransactionAuthHandler is the interface for ABCI applications that handle
// authenticating transactions (checking nonces and fees).
type TransactionAuthHandler interface {
	consensus.TransactionAuthHandler

	// AuthenticateTx authenticates the given transaction by making sure
	// that the nonce is correct and deducts any fees as specified.
	//
	// It may reject the transaction in case of incorrect nonces, insufficient
	// balance to pay fees or (only during CheckTx) if the gas price is too
	// low.
	//
	// The context may be modified to configure a gas accountant.
	AuthenticateTx(ctx *Context, tx *transaction.Transaction) error
}

// Application is the interface implemented by multiplexed Oasis-specific
// ABCI applications.
type Application interface {
	// Name returns the name of the Application.
	Name() string

	// ID returns the unique identifier of the application.
	ID() uint8

	// Methods returns the list of supported methods.
	Methods() []transaction.MethodName

	// Blessed returns true iff the Application should be considered
	// "blessed", and able to alter the validation set and handle the
	// access control related standard ABCI queries.
	//
	// Only one Application instance may be Blessed per multiplexer
	// instance.
	Blessed() bool

	// Dependencies returns the names of applications that the application
	// depends on.
	Dependencies() []string

	// QueryFactory returns an application-specific query factory that
	// can be used to construct new queries at specific block heights.
	QueryFactory() interface{}

	// OnRegister is the function that is called when the Application
	// is registered with the multiplexer instance.
	OnRegister(state *ApplicationState)

	// OnCleanup is the function that is called when the ApplicationServer
	// has been halted.
	OnCleanup()

	// ExecuteTx executes a transaction.
	ExecuteTx(*Context, *transaction.Transaction) error

	// ForeignExecuteTx delivers a transaction of another application for
	// processing.
	//
	// This can be used to run post-tx hooks when dependencies exist
	// between applications.
	ForeignExecuteTx(*Context, Application, *transaction.Transaction) error

	// InitChain initializes the blockchain with validators and other
	// info from TendermintCore.
	//
	// Note: Errors are irrecoverable and will result in a panic.
	InitChain(*Context, types.RequestInitChain, *genesis.Document) error

	// BeginBlock signals the beginning of a block.
	//
	// Returned tags will be added to the current block.
	//
	// Note: Errors are irrecoverable and will result in a panic.
	BeginBlock(*Context, types.RequestBeginBlock) error

	// EndBlock signals the end of a block, returning changes to the
	// validator set.
	//
	// Note: Errors are irrecoverable and will result in a panic.
	EndBlock(*Context, types.RequestEndBlock) (types.ResponseEndBlock, error)

	// FireTimer is called within BeginBlock before any other processing
	// takes place for each timer that should fire.
	//
	// Note: Errors are irrecoverable and will result in a panic.
	FireTimer(*Context, *Timer) error

	// Commit is omitted because Applications will work on a cache of
	// the state bound to the multiplexer.
}

// ApplicationServer implements a tendermint ABCI application + socket server,
// that multiplexes multiple Oasis-specific "applications".
type ApplicationServer struct {
	mux         *abciMux
	quitChannel chan struct{}
	cleanupOnce sync.Once
}

// Start starts the ApplicationServer.
func (a *ApplicationServer) Start() error {
	if a.mux.state.timeSource == nil {
		return fmt.Errorf("mux: timeSource not defined")
	}
	return a.mux.checkDependencies()
}

// Stop stops the ApplicationServer.
func (a *ApplicationServer) Stop() {
	close(a.quitChannel)
}

// Quit returns a channel which is closed when the ApplicationServer is
// stopped.
func (a *ApplicationServer) Quit() <-chan struct{} {
	return a.quitChannel
}

// Cleanup cleans up the state of an ApplicationServer instance.
func (a *ApplicationServer) Cleanup() {
	a.cleanupOnce.Do(func() {
		a.mux.doCleanup()
	})
}

// Mux retrieve the abci Mux (or tendermint application) served by this server.
func (a *ApplicationServer) Mux() types.Application {
	return a.mux
}

// Register registers an Oasis application with the ABCI multiplexer.
//
// All registration must be done before Start is called.  ABCI operations
// that act on every single app (InitChain, BeginBlock, EndBlock) will be
// called in name lexicographic order. Checks that applications named in
// deps are already registered.
func (a *ApplicationServer) Register(app Application) error {
	return a.mux.doRegister(app)
}

// RegisterGenesisHook registers a function to be called when the
// consensus backend is initialized from genesis (e.g., on fresh
// start).
func (a *ApplicationServer) RegisterGenesisHook(hook func()) {
	a.mux.registerGenesisHook(hook)
}

// RegisterHaltHook registers a function to be called when the
// consensus Halt epoch height is reached.
func (a *ApplicationServer) RegisterHaltHook(hook func(ctx context.Context, blockHeight int64, epoch epochtime.EpochTime)) {
	a.mux.registerHaltHook(hook)
}

// Pruner returns the ABCI state pruner.
func (a *ApplicationServer) Pruner() StatePruner {
	return a.mux.state.statePruner
}

// SetEpochtime sets the mux epochtime.
//
// Epochtime must be set before the multiplexer can be used.
func (a *ApplicationServer) SetEpochtime(epochTime epochtime.Backend) error {
	if a.mux.state.timeSource != nil {
		return fmt.Errorf("mux: epochtime already configured")
	}

	a.mux.state.timeSource = epochTime
	return nil
}

// SetTransactionAuthHandler configures the transaction auth handler for the
// ABCI multiplexer.
func (a *ApplicationServer) SetTransactionAuthHandler(handler TransactionAuthHandler) error {
	if a.mux.state.txAuthHandler != nil {
		return fmt.Errorf("mux: transaction fee handler already configured")
	}

	a.mux.state.txAuthHandler = handler
	return nil
}

// TransactionAuthHandler returns the configured handler for authenticating
// transactions.
func (a *ApplicationServer) TransactionAuthHandler() TransactionAuthHandler {
	return a.mux.state.txAuthHandler
}

// WatchInvalidatedTx adds a watcher for when/if the transaction with given
// hash becomes invalid due to a failed re-check.
func (a *ApplicationServer) WatchInvalidatedTx(txHash hash.Hash) (<-chan error, pubsub.ClosableSubscription, error) {
	return a.mux.watchInvalidatedTx(txHash)
}

// EstimateGas calculates the amount of gas required to execute the given transaction.
func (a *ApplicationServer) EstimateGas(caller signature.PublicKey, tx *transaction.Transaction) (transaction.Gas, error) {
	return a.mux.EstimateGas(caller, tx)
}

// NewApplicationServer returns a new ApplicationServer, using the provided
// directory to persist state.
func NewApplicationServer(ctx context.Context, cfg *ApplicationConfig) (*ApplicationServer, error) {
	metricsOnce.Do(func() {
		prometheus.MustRegister(abciCollectors...)
	})

	mux, err := newABCIMux(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &ApplicationServer{
		mux:         mux,
		quitChannel: make(chan struct{}),
	}, nil
}

type abciMux struct {
	sync.RWMutex
	types.BaseApplication

	logger *logging.Logger
	state  *ApplicationState

	appsByName     map[string]Application
	appsByMethod   map[transaction.MethodName]Application
	appsByLexOrder []Application
	appBlessed     Application

	lastBeginBlock int64
	currentTime    time.Time
	maxTxSize      uint64
	maxBlockGas    transaction.Gas

	genesisHooks []func()
	haltHooks    []func(context.Context, int64, epochtime.EpochTime)

	// invalidatedTxs maps transaction hashes (hash.Hash) to a subscriber
	// waiting for that transaction to become invalid.
	invalidatedTxs sync.Map
}

type invalidatedTxSubscription struct {
	mux      *abciMux
	txHash   hash.Hash
	resultCh chan<- error
}

func (s *invalidatedTxSubscription) Close() {
	if s.mux == nil {
		return
	}
	s.mux.invalidatedTxs.Delete(s.txHash)
	s.mux = nil
}

func (mux *abciMux) watchInvalidatedTx(txHash hash.Hash) (<-chan error, pubsub.ClosableSubscription, error) {
	resultCh := make(chan error)
	sub := &invalidatedTxSubscription{
		mux:      mux,
		txHash:   txHash,
		resultCh: resultCh,
	}

	if _, exists := mux.invalidatedTxs.LoadOrStore(txHash, sub); exists {
		return nil, nil, fmt.Errorf("mux: transaction already exists")
	}

	return resultCh, sub, nil
}

func (mux *abciMux) registerGenesisHook(hook func()) {
	mux.Lock()
	defer mux.Unlock()

	mux.genesisHooks = append(mux.genesisHooks, hook)
}

func (mux *abciMux) registerHaltHook(hook func(context.Context, int64, epochtime.EpochTime)) {
	mux.Lock()
	defer mux.Unlock()

	mux.haltHooks = append(mux.haltHooks, hook)
}

func (mux *abciMux) Info(req types.RequestInfo) types.ResponseInfo {
	return types.ResponseInfo{
		AppVersion:       version.ConsensusProtocol.ToU64(),
		LastBlockHeight:  mux.state.BlockHeight(),
		LastBlockAppHash: mux.state.BlockHash(),
	}
}

func (mux *abciMux) InitChain(req types.RequestInitChain) types.ResponseInitChain {
	mux.logger.Debug("InitChain",
		"req", req,
	)

	// Sanity-check the genesis application state.
	st, err := parseGenesisAppState(req)
	if err != nil {
		mux.logger.Error("failed to unmarshal genesis application state",
			"err", err,
		)
		panic("mux: invalid genesis application state")
	}

	if mux.maxTxSize = st.Consensus.Parameters.MaxTxSize; mux.maxTxSize == 0 {
		mux.logger.Warn("maximum transaction size enforcement is disabled")
	}
	if mux.maxBlockGas = transaction.Gas(st.Consensus.Parameters.MaxBlockGas); mux.maxBlockGas == 0 {
		mux.logger.Warn("maximum block gas enforcement is disabled")
	}

	b, _ := json.Marshal(st)
	mux.logger.Debug("Genesis ABCI application state",
		"state", string(b),
	)

	mux.currentTime = st.Time

	// Stick the digest of the genesis block (the RequestInitChain) into
	// the state.
	//
	// This serves to keep bad things from happening if absolutely
	// nothing writes to the state till the Commit() call, along with
	// clearly separating chain instances based on the initialization
	// state, forever.
	tmp := bytes.NewBuffer(nil)
	_ = types.WriteMessage(&req, tmp)
	genesisDigest := sha512.Sum512_256(tmp.Bytes())
	mux.state.deliverTxTree.Set([]byte(stateKeyGenesisDigest), genesisDigest[:])

	resp := mux.BaseApplication.InitChain(req)

	// HACK: The state is only updated iff validators or consensus parameters
	// are returned.
	//
	// See: tendermint/consensus/replay.go (Handshaker.ReplayBlocks)
	if len(resp.Validators) == 0 && resp.ConsensusParams == nil {
		resp.ConsensusParams = req.ConsensusParams
	}

	// Dispatch registered genesis hooks.
	func() {
		mux.RLock()
		defer mux.RUnlock()

		mux.logger.Debug("Dispatching genesis hooks")

		for _, hook := range mux.genesisHooks {
			hook()
		}

		mux.logger.Debug("Genesis hook dispatch complete")
	}()

	// TODO: remove stateKeyGenesisRequest here, see oasis-core#2426
	b, _ = req.Marshal()
	mux.state.deliverTxTree.Set([]byte(stateKeyGenesisRequest), b)
	mux.state.checkTxTree.Set([]byte(stateKeyGenesisRequest), b)

	// Call InitChain() on all applications.
	mux.logger.Debug("InitChain: initializing applications")

	ctx := NewContext(ContextInitChain, mux.currentTime, mux.state)
	defer ctx.Close()

	for _, app := range mux.appsByLexOrder {
		mux.logger.Debug("InitChain: calling InitChain on application",
			"app", app.Name(),
		)

		if err = app.InitChain(ctx, req, st); err != nil {
			mux.logger.Error("InitChain: fatal error in application",
				"err", err,
				"app", app.Name(),
			)
			panic("mux: InitChain: fatal error in application: '" + app.Name() + "': " + err.Error())
		}
	}

	mux.logger.Debug("InitChain: initializing of applications complete", "num_collected_events", len(ctx.GetEvents()))

	// Since returning emitted events doesn't work for InitChain() response yet,
	// we store those and return them in BeginBlock().
	evBinary := cbor.Marshal(ctx.GetEvents())
	mux.state.deliverTxTree.Set([]byte(stateKeyInitChainEvents), evBinary)

	return resp
}

func (s *ApplicationState) inHaltEpoch(ctx *Context) bool {
	blockHeight := s.BlockHeight()

	currentEpoch, err := s.GetEpoch(ctx.Ctx(), blockHeight+1)
	if err != nil {
		s.logger.Error("inHaltEpoch: failed to get epoch",
			"err", err,
			"block_height", blockHeight+1,
		)
		return false
	}
	s.haltMode = currentEpoch == s.haltEpochHeight
	return s.haltMode
}

func (s *ApplicationState) afterHaltEpoch(ctx *Context) bool {
	blockHeight := s.BlockHeight()

	currentEpoch, err := s.GetEpoch(ctx.Ctx(), blockHeight+1)
	if err != nil {
		s.logger.Error("afterHaltEpoch: failed to get epoch",
			"err", err,
			"block_height", blockHeight,
		)
		return false
	}

	return currentEpoch > s.haltEpochHeight
}

func (mux *abciMux) BeginBlock(req types.RequestBeginBlock) types.ResponseBeginBlock {
	blockHeight := mux.state.BlockHeight()
	mux.logger.Debug("BeginBlock",
		"req", req,
		"block_height", blockHeight,
	)

	// 99% sure this is a protocol violation.
	if mux.lastBeginBlock == blockHeight {
		panic("mux: redundant BeginBlock")
	}
	mux.lastBeginBlock = blockHeight
	mux.currentTime = req.Header.Time

	// Create empty block context.
	mux.state.blockCtx = NewBlockContext()
	if mux.maxBlockGas > 0 {
		mux.state.blockCtx.Set(GasAccountantKey{}, NewGasAccountant(mux.maxBlockGas))
	} else {
		mux.state.blockCtx.Set(GasAccountantKey{}, NewNopGasAccountant())
	}
	// Create BeginBlock context.
	ctx := NewContext(ContextBeginBlock, mux.currentTime, mux.state)
	defer ctx.Close()

	switch mux.state.haltMode {
	case false:
		if !mux.state.inHaltEpoch(ctx) {
			break
		}
		// On transition, trigger halt hooks.
		mux.logger.Info("BeginBlock: halt mode transition, emitting empty blocks.",
			"block_height", blockHeight,
			"epoch", mux.state.haltEpochHeight,
		)
		mux.logger.Debug("Dispatching halt hooks")
		for _, hook := range mux.haltHooks {
			hook(mux.state.ctx, blockHeight, mux.state.haltEpochHeight)
		}
		mux.logger.Debug("Halt hook dispatch complete")
		return types.ResponseBeginBlock{}
	case true:
		if !mux.state.afterHaltEpoch(ctx) {
			return types.ResponseBeginBlock{}
		}

		mux.logger.Info("BeginBlock: after halt epoch, halting",
			"block_height", blockHeight,
		)
		// XXX: there is no way to stop tendermint consensus other than
		// triggering a panic. Once possible, we should stop the consensus
		// layer here and gracefully shutdown the node.
		panic("tendermint: after halt epoch, halting")
	}

	// Dispatch BeginBlock to all applications.
	for _, app := range mux.appsByLexOrder {
		if err := app.BeginBlock(ctx, req); err != nil {
			mux.logger.Error("BeginBlock: fatal error in application",
				"err", err,
				"app", app.Name(),
			)
			panic("mux: BeginBlock: fatal error in application: '" + app.Name() + "': " + err.Error())
		}
	}

	response := mux.BaseApplication.BeginBlock(req)

	// Collect and return events from the application's BeginBlock calls.
	response.Events = ctx.GetEvents()

	// During the first block, also collect and prepend application events
	// generated during InitChain to BeginBlock events.
	if mux.state.BlockHeight() == 0 {
		_, evBinary := mux.state.deliverTxTree.Get([]byte(stateKeyInitChainEvents))
		if evBinary != nil {
			var events []types.Event
			_ = cbor.Unmarshal(evBinary, &events)

			response.Events = append(events, response.Events...)

			mux.state.deliverTxTree.Remove([]byte(stateKeyInitChainEvents))
		}
	}

	return response
}

func (mux *abciMux) decodeTx(ctx *Context, rawTx []byte) (*transaction.Transaction, *transaction.SignedTransaction, error) {
	if mux.state.haltMode {
		ctx.Logger().Debug("executeTx: in halt, rejecting all transactions")
		return nil, nil, fmt.Errorf("halt mode, rejecting all transactions")
	}

	if mux.maxTxSize > 0 && uint64(len(rawTx)) > mux.maxTxSize {
		// This deliberately avoids logging the rawTx since spamming the
		// logs is also bad.
		ctx.Logger().Error("received oversized transaction",
			"tx_size", len(rawTx),
		)
		return nil, nil, errOversizedTx
	}

	// Unmarshal envelope and verify transaction.
	var sigTx transaction.SignedTransaction
	if err := cbor.Unmarshal(rawTx, &sigTx); err != nil {
		ctx.Logger().Error("failed to unmarshal signed transaction",
			"tx", base64.StdEncoding.EncodeToString(rawTx),
		)
		return nil, nil, err
	}
	var tx transaction.Transaction
	if err := sigTx.Open(&tx); err != nil {
		ctx.Logger().Error("failed to verify transaction signature",
			"tx", base64.StdEncoding.EncodeToString(rawTx),
		)
		return nil, nil, err
	}
	if err := tx.SanityCheck(); err != nil {
		ctx.Logger().Error("bad transaction",
			"tx", base64.StdEncoding.EncodeToString(rawTx),
		)
		return nil, nil, err
	}

	return &tx, &sigTx, nil
}

func (mux *abciMux) processTx(ctx *Context, tx *transaction.Transaction) error {
	// Pass the transaction through the fee handler if configured.
	if txAuthHandler := mux.state.txAuthHandler; txAuthHandler != nil {
		if err := txAuthHandler.AuthenticateTx(ctx, tx); err != nil {
			ctx.Logger().Debug("failed to authenticate transaction",
				"tx", tx,
				"tx_signer", ctx.TxSigner(),
				"method", tx.Method,
				"err", err,
			)
			return err
		}
	}

	// Route to correct handler.
	app := mux.appsByMethod[tx.Method]
	if app == nil {
		ctx.Logger().Error("unknown method",
			"tx", tx,
			"method", tx.Method,
		)
		return fmt.Errorf("mux: unknown method: %s", tx.Method)
	}

	ctx.Logger().Debug("dispatching",
		"app", app.Name(),
		"tx", tx,
	)

	if err := app.ExecuteTx(ctx, tx); err != nil {
		return err
	}

	// Run ForeignDeliverTx on all other applications so they can
	// run their post-tx hooks.
	for _, foreignApp := range mux.appsByLexOrder {
		if foreignApp == app {
			continue
		}

		if err := foreignApp.ForeignExecuteTx(ctx, app, tx); err != nil {
			return err
		}
	}

	return nil
}

func (mux *abciMux) executeTx(ctx *Context, rawTx []byte) error {
	tx, sigTx, err := mux.decodeTx(ctx, rawTx)
	if err != nil {
		return err
	}

	// Set authenticated transaction signer.
	ctx.SetTxSigner(sigTx.Signature.PublicKey)

	return mux.processTx(ctx, tx)
}

func (mux *abciMux) EstimateGas(caller signature.PublicKey, tx *transaction.Transaction) (transaction.Gas, error) {
	// As opposed to other transaction dispatch entry points (CheckTx/DeliverTx), this method can
	// be called in parallel to the consensus layer and to other invocations.
	//
	// For simulation mode, time will be filled in by NewContext from last block time.
	ctx := NewContext(ContextSimulateTx, time.Time{}, mux.state)
	defer ctx.Close()

	ctx.SetTxSigner(caller)

	// Ignore any errors that occurred during simulation as we only need to estimate gas even if the
	// transaction seems like it will fail.
	_ = mux.processTx(ctx, tx)

	return ctx.Gas().GasUsed(), nil
}

func (mux *abciMux) CheckTx(req types.RequestCheckTx) types.ResponseCheckTx {
	ctx := NewContext(ContextCheckTx, mux.currentTime, mux.state)
	defer ctx.Close()

	if err := mux.executeTx(ctx, req.Tx); err != nil {
		module, code := errors.Code(err)

		if req.Type == types.CheckTxType_Recheck {
			// This is a re-check and the transaction just failed validation. Since
			// the mempool provides no way of getting notified when a previously
			// valid transaction becomes invalid, handle this here.

			// XXX: The Tendermint mempool should have provisions for this instead
			//      of us hacking our way through this here.
			var txHash hash.Hash
			txHash.FromBytes(req.Tx)

			if item, exists := mux.invalidatedTxs.Load(txHash); exists {
				// Notify subscriber.
				sub := item.(*invalidatedTxSubscription)
				select {
				case sub.resultCh <- err:
				default:
				}
				close(sub.resultCh)

				mux.invalidatedTxs.Delete(txHash)
			}
		}

		return types.ResponseCheckTx{
			Codespace: module,
			Code:      code,
			Log:       err.Error(),
			GasWanted: int64(ctx.Gas().GasWanted()),
			GasUsed:   int64(ctx.Gas().GasUsed()),
		}
	}

	return types.ResponseCheckTx{
		Code:      types.CodeTypeOK,
		GasWanted: int64(ctx.Gas().GasWanted()),
		GasUsed:   int64(ctx.Gas().GasUsed()),
	}
}

func (mux *abciMux) DeliverTx(req types.RequestDeliverTx) types.ResponseDeliverTx {
	ctx := NewContext(ContextDeliverTx, mux.currentTime, mux.state)
	defer ctx.Close()

	if err := mux.executeTx(ctx, req.Tx); err != nil {
		module, code := errors.Code(err)

		return types.ResponseDeliverTx{
			Codespace: module,
			Code:      code,
			Log:       err.Error(),
			GasWanted: int64(ctx.Gas().GasWanted()),
			GasUsed:   int64(ctx.Gas().GasUsed()),
		}
	}

	return types.ResponseDeliverTx{
		Code:      types.CodeTypeOK,
		Data:      cbor.Marshal(ctx.Data()),
		Events:    ctx.GetEvents(),
		GasWanted: int64(ctx.Gas().GasWanted()),
		GasUsed:   int64(ctx.Gas().GasUsed()),
	}
}

func (mux *abciMux) EndBlock(req types.RequestEndBlock) types.ResponseEndBlock {
	mux.logger.Debug("EndBlock",
		"req", req,
		"block_height", mux.state.BlockHeight(),
	)

	if mux.state.haltMode {
		mux.logger.Debug("EndBlock: in halt, emitting empty block")
		return types.ResponseEndBlock{}
	}

	ctx := NewContext(ContextEndBlock, mux.currentTime, mux.state)
	defer ctx.Close()

	// Fire all application timers first.
	for _, app := range mux.appsByLexOrder {
		if err := fireTimers(ctx, app); err != nil {
			mux.logger.Error("EndBlock: fatal error during timer fire",
				"err", err,
				"app", app.Name(),
			)
			panic("mux: EndBlock: fatal error in application: '" + app.Name() + "': " + err.Error())
		}
	}

	// Dispatch EndBlock to all applications.
	resp := mux.BaseApplication.EndBlock(req)
	for _, app := range mux.appsByLexOrder {
		newResp, err := app.EndBlock(ctx, req)
		if err != nil {
			mux.logger.Error("EndBlock: fatal error in application",
				"err", err,
				"app", app.Name(),
			)
			panic("mux: EndBlock: fatal error in application: '" + app.Name() + "': " + err.Error())
		}
		if app.Blessed() {
			resp = newResp
		}
	}

	// Update tags.
	resp.Events = ctx.GetEvents()

	// Clear block context.
	mux.state.blockCtx = nil

	return resp
}

func (mux *abciMux) Commit() types.ResponseCommit {
	if err := mux.state.doCommit(mux.currentTime); err != nil {
		mux.logger.Error("Commit failed",
			"err", err,
		)

		// There appears to be no way to indicate to the caller that
		// this failed.
		panic(err)
	}

	mux.logger.Debug("Commit",
		"block_height", mux.state.BlockHeight(),
		"block_hash", hex.EncodeToString(mux.state.BlockHash()),
	)

	return types.ResponseCommit{Data: mux.state.BlockHash()}
}

func (mux *abciMux) doCleanup() {
	mux.state.doCleanup()

	for _, v := range mux.appsByLexOrder {
		v.OnCleanup()
	}
}

func (mux *abciMux) doRegister(app Application) error {
	name := app.Name()
	if mux.appsByName[name] != nil {
		return fmt.Errorf("mux: application already registered: '%s'", name)
	}
	if app.Blessed() {
		// Enforce the 1 blessed app limitation.
		if mux.appBlessed != nil {
			return fmt.Errorf("mux: blessed application already exists")
		}
		mux.appBlessed = app
	}

	mux.appsByName[name] = app
	for _, m := range app.Methods() {
		if _, exists := mux.appsByMethod[m]; exists {
			return fmt.Errorf("mux: method already registered: %s", m)
		}
		mux.appsByMethod[m] = app
	}
	mux.rebuildAppLexOrdering() // Inefficient but not a lot of apps.

	app.OnRegister(mux.state)
	mux.logger.Debug("Registered new application",
		"app", app.Name(),
	)

	return nil
}

func (mux *abciMux) rebuildAppLexOrdering() {
	numApps := len(mux.appsByName)
	appOrder := make([]string, 0, numApps)
	for name := range mux.appsByName {
		appOrder = append(appOrder, name)
	}
	sort.Strings(appOrder)

	mux.appsByLexOrder = make([]Application, 0, numApps)
	for _, name := range appOrder {
		mux.appsByLexOrder = append(mux.appsByLexOrder, mux.appsByName[name])
	}
}

func (mux *abciMux) checkDependencies() error {
	var missingDeps [][2]string
	for neededFor, app := range mux.appsByName {
		for _, dep := range app.Dependencies() {
			if _, ok := mux.appsByName[dep]; !ok {
				missingDeps = append(missingDeps, [2]string{dep, neededFor})
			}
		}
	}
	if missingDeps != nil {
		return fmt.Errorf("mux: missing dependencies %v", missingDeps)
	}
	return nil
}

func newABCIMux(ctx context.Context, cfg *ApplicationConfig) (*abciMux, error) {
	state, err := newApplicationState(ctx, cfg)
	if err != nil {
		return nil, err
	}

	mux := &abciMux{
		logger:         logging.GetLogger("abci-mux"),
		state:          state,
		appsByName:     make(map[string]Application),
		appsByMethod:   make(map[transaction.MethodName]Application),
		lastBeginBlock: -1,
	}

	mux.logger.Debug("ABCI multiplexer initialized",
		"block_height", state.BlockHeight(),
		"block_hash", hex.EncodeToString(state.BlockHash()),
	)

	return mux, nil
}

// ApplicationState is the overall past, present and future state
// of all multiplexed applications.
type ApplicationState struct {
	logger *logging.Logger

	ctx           context.Context
	db            dbm.DB
	deliverTxTree *iavl.MutableTree
	checkTxTree   *iavl.MutableTree
	statePruner   StatePruner

	blockLock   sync.RWMutex
	blockHash   []byte
	blockHeight int64
	blockTime   time.Time
	blockCtx    *BlockContext

	txAuthHandler TransactionAuthHandler

	timeSource epochtime.Backend

	haltMode        bool
	haltEpochHeight epochtime.EpochTime

	minGasPrice quantity.Quantity

	metricsCloseCh  chan struct{}
	metricsClosedCh chan struct{}
}

// BlockHeight returns the last committed block height.
func (s *ApplicationState) BlockHeight() int64 {
	s.blockLock.RLock()
	defer s.blockLock.RUnlock()

	return s.blockHeight
}

// BlockHash returns the last committed block hash.
func (s *ApplicationState) BlockHash() []byte {
	s.blockLock.RLock()
	defer s.blockLock.RUnlock()

	return append([]byte{}, s.blockHash...)
}

// BlockContext returns the current block context which can be used
// to store intermediate per-block results.
//
// This method must only be called from BeginBlock/DeliverTx/EndBlock
// and calls from anywhere else will cause races.
func (s *ApplicationState) BlockContext() *BlockContext {
	return s.blockCtx
}

// DeliverTxTree returns the versioned tree to be used by queries
// to view comitted data, and transactions to build the next version.
func (s *ApplicationState) DeliverTxTree() *iavl.MutableTree {
	return s.deliverTxTree
}

// CheckTxTree returns the state tree to be used for modifications
// inside CheckTx (mempool connection) calls.
//
// This state is never persisted.
func (s *ApplicationState) CheckTxTree() *iavl.MutableTree {
	return s.checkTxTree
}

// GetBaseEpoch returns the base epoch.
func (s *ApplicationState) GetBaseEpoch() (epochtime.EpochTime, error) {
	return s.timeSource.GetBaseEpoch(s.ctx)
}

// GetEpoch returns current epoch at block height.
func (s *ApplicationState) GetEpoch(ctx context.Context, blockHeight int64) (epochtime.EpochTime, error) {
	return s.timeSource.GetEpoch(ctx, blockHeight)
}

// EpochChanged returns true iff the current epoch has changed since the
// last block.  As a matter of convenience, the current epoch is returned.
func (s *ApplicationState) EpochChanged(ctx *Context) (bool, epochtime.EpochTime) {
	blockHeight := s.BlockHeight()
	if blockHeight == 0 {
		return false, epochtime.EpochInvalid
	} else if blockHeight == 1 {
		// There is no block before the first block. For historic reasons, this is defined as not
		// having had a transition.
		currentEpoch, err := s.timeSource.GetEpoch(ctx.Ctx(), blockHeight)
		if err != nil {
			s.logger.Error("EpochChanged: failed to get current epoch",
				"err", err,
			)
			return false, epochtime.EpochInvalid
		}
		return false, currentEpoch
	}

	previousEpoch, err := s.timeSource.GetEpoch(ctx.Ctx(), blockHeight)
	if err != nil {
		s.logger.Error("EpochChanged: failed to get previous epoch",
			"err", err,
		)
		return false, epochtime.EpochInvalid
	}
	currentEpoch, err := s.timeSource.GetEpoch(ctx.Ctx(), blockHeight+1)
	if err != nil {
		s.logger.Error("EpochChanged: failed to get current epoch",
			"err", err,
		)
		return false, epochtime.EpochInvalid
	}

	if previousEpoch == currentEpoch {
		return false, currentEpoch
	}

	s.logger.Debug("EpochChanged: epoch transition detected",
		"prev_epoch", previousEpoch,
		"epoch", currentEpoch,
	)

	return true, currentEpoch
}

// Genesis returns the ABCI genesis state.
func (s *ApplicationState) Genesis() *genesis.Document {
	_, b := s.checkTxTree.Get([]byte(stateKeyGenesisRequest))

	var req types.RequestInitChain
	if err := req.Unmarshal(b); err != nil {
		s.logger.Error("Genesis: corrupted defered genesis state",
			"err", err,
		)
		panic("Genesis: invalid defered genesis application state")
	}

	st, err := parseGenesisAppState(req)
	if err != nil {
		s.logger.Error("failed to unmarshal genesis application state",
			"err", err,
			"state", req.AppStateBytes,
		)
		panic("Genesis: invalid genesis application state")
	}

	return st
}

// MinGasPrice returns the configured minimum gas price.
func (s *ApplicationState) MinGasPrice() *quantity.Quantity {
	return &s.minGasPrice
}

func (s *ApplicationState) doCommit(now time.Time) error {
	// Save the new version of the persistent tree.
	blockHash, blockHeight, err := s.deliverTxTree.SaveVersion()
	if err == nil {
		s.blockLock.Lock()
		s.blockHash = blockHash
		s.blockHeight = blockHeight
		s.blockTime = now
		s.blockLock.Unlock()

		// Reset CheckTx state to latest version. This is safe because
		// Tendermint holds a lock on the mempool for commit.
		//
		// WARNING: deliverTxTree and checkTxTree do not share internal
		// state beyond the backing database.  The `LoadVersion`
		// implementation MUST be written in a way to avoid relying on
		// cached metadata.
		//
		// This makes the upstream `LazyLoadVersion` and `LoadVersion`
		// unsuitable for our use case.
		_, cerr := s.checkTxTree.LoadVersion(blockHeight)
		if cerr != nil {
			panic(cerr)
		}

		// Prune the iavl state according to the specified strategy.
		s.statePruner.Prune(s.blockHeight)
	}

	return err
}

func (s *ApplicationState) doCleanup() {
	if s.db != nil {
		// Don't close the DB out from under the metrics worker.
		close(s.metricsCloseCh)
		<-s.metricsClosedCh

		s.db.Close()
		s.db = nil
	}
}

func (s *ApplicationState) updateMetrics() error {
	var dbSize int64

	switch m := s.db.(type) {
	case api.SizeableDB:
		var err error
		if dbSize, err = m.Size(); err != nil {
			s.logger.Error("Size",
				"err", err,
			)
			return err
		}
	default:
		return fmt.Errorf("state: unsupported DB for metrics")
	}

	abciSize.Set(float64(dbSize) / 1024768.0)

	return nil
}

func (s *ApplicationState) metricsWorker() {
	defer close(s.metricsClosedCh)

	// Update the metrics once on initialization.
	if err := s.updateMetrics(); err != nil {
		// If this fails, don't bother trying again, it's most likely
		// an unsupported DB backend.
		s.logger.Warn("metrics not available",
			"err", err,
		)
		return
	}

	t := time.NewTicker(metricsUpdateInterval)
	defer t.Stop()

	for {
		select {
		case <-s.metricsCloseCh:
			return
		case <-t.C:
			_ = s.updateMetrics()
		}
	}
}

func newApplicationState(ctx context.Context, cfg *ApplicationConfig) (*ApplicationState, error) {
	db, err := db.New(filepath.Join(cfg.DataDir, "abci-mux-state"), false)
	if err != nil {
		return nil, err
	}

	// Figure out the latest version/hash if any, and use that
	// as the block height/hash.
	deliverTxTree := iavl.NewMutableTree(db, 128)
	blockHeight, err := deliverTxTree.Load()
	if err != nil {
		db.Close()
		return nil, err
	}
	blockHash := deliverTxTree.Hash()

	checkTxTree := iavl.NewMutableTree(db, 128)
	checkTxBlockHeight, err := checkTxTree.Load()
	if err != nil {
		db.Close()
		return nil, err
	}

	if blockHeight != checkTxBlockHeight || !bytes.Equal(blockHash, checkTxTree.Hash()) {
		db.Close()
		return nil, fmt.Errorf("state: inconsistent trees")
	}

	statePruner, err := newStatePruner(&cfg.Pruning, deliverTxTree, blockHeight)
	if err != nil {
		db.Close()
		return nil, err
	}

	var minGasPrice quantity.Quantity
	if err = minGasPrice.FromInt64(int64(cfg.MinGasPrice)); err != nil {
		return nil, fmt.Errorf("state: invalid minimum gas price: %w", err)
	}

	s := &ApplicationState{
		logger:          logging.GetLogger("abci-mux/state"),
		ctx:             ctx,
		db:              db,
		deliverTxTree:   deliverTxTree,
		checkTxTree:     checkTxTree,
		statePruner:     statePruner,
		blockHash:       blockHash,
		blockHeight:     blockHeight,
		haltEpochHeight: cfg.HaltEpochHeight,
		minGasPrice:     minGasPrice,
		metricsCloseCh:  make(chan struct{}),
		metricsClosedCh: make(chan struct{}),
	}
	go s.metricsWorker()

	return s, nil
}

func parseGenesisAppState(req types.RequestInitChain) (*genesis.Document, error) {
	var st genesis.Document
	if err := json.Unmarshal(req.AppStateBytes, &st); err != nil {
		return nil, err
	}

	return &st, nil
}
