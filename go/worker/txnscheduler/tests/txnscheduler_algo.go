package tests

import (
	"sync"

	"github.com/oasislabs/ekiden/go/common/crypto/signature"
	"github.com/oasislabs/ekiden/go/common/runtime"
	"github.com/oasislabs/ekiden/go/worker/txnscheduler/algorithm/api"
	"github.com/oasislabs/ekiden/go/worker/txnscheduler/algorithm/registry"
	"github.com/oasislabs/ekiden/go/worker/txnscheduler/algorithm/trivial"
)

var globalTestTxnSchedulerAlgorithm *testTxnSchedulerAlgorithm

func init() {
	globalTestTxnSchedulerAlgorithm = newTestTxnSchedulerAlgorithm()

	registry.Register("testing_algorithm", func() (api.Algorithm, error) {
		return globalTestTxnSchedulerAlgorithm, nil
	})
}

// A test transaction scheduler algorithm
type testTxnSchedulerAlgorithm struct {
	api.Algorithm

	scheduleTxOverride func(signature.PublicKey, runtime.Batch) (api.ScheduleResult, error)

	// An instance of the trivial algorithm is used as the default
	trivialAlgo *trivial.Trivial
	mut         *sync.Mutex
}

func newTestTxnSchedulerAlgorithm() *testTxnSchedulerAlgorithm {
	trivialAlgo := &trivial.Trivial{}
	_ = trivialAlgo.Initialize()

	algo := &testTxnSchedulerAlgorithm{
		trivialAlgo: trivialAlgo,
		mut:         &sync.Mutex{},
	}
	algo.Reset()
	return algo
}

func (t *testTxnSchedulerAlgorithm) Initialize() error {
	return nil
}

func (t *testTxnSchedulerAlgorithm) ScheduleTx(runtimeID signature.PublicKey, txs runtime.Batch) (api.ScheduleResult, error) {
	t.mut.Lock()
	res, err := t.scheduleTxOverride(runtimeID, txs)
	t.mut.Unlock()
	return res, err
}

// ResetTestTxnScheduler resets the global test transaction scheduler.
func ResetTestTxnScheduler() {
	globalTestTxnSchedulerAlgorithm.Reset()
}

func (t *testTxnSchedulerAlgorithm) Reset() {
	t.SetScheduleTxOverride(func(runtimeID signature.PublicKey, txs runtime.Batch) (api.ScheduleResult, error) {
		return t.trivialAlgo.ScheduleTx(runtimeID, txs)
	})
}

// SetTestTxnSchedulerScheduleTxOverride sets the global test transaction scheduler
// ScheduleTx method.
func SetTestTxnSchedulerScheduleTxOverride(scheduleTxOverride func(signature.PublicKey, runtime.Batch) (api.ScheduleResult, error)) {
	globalTestTxnSchedulerAlgorithm.SetScheduleTxOverride(scheduleTxOverride)
}

func (t *testTxnSchedulerAlgorithm) SetScheduleTxOverride(scheduleTxOverride func(signature.PublicKey, runtime.Batch) (api.ScheduleResult, error)) {
	t.mut.Lock()
	t.scheduleTxOverride = scheduleTxOverride
	t.mut.Unlock()
}
