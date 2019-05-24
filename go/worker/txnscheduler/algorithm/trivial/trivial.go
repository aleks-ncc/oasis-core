// Package trivial implements a trivial transaction scheduling algorithm
package trivial

import (
	"github.com/oasislabs/ekiden/go/common/crypto/signature"
	"github.com/oasislabs/ekiden/go/common/runtime"
	"github.com/oasislabs/ekiden/go/worker/txnscheduler/algorithm/api"
)

// Trivial is a trivial transaction scheduling algorithm
type Trivial struct {
}

// Initialize initializes trivial scheduling algorithm
func (s *Trivial) Initialize() error {
	return nil
}

// ScheduleTx schedules transactions
func (s *Trivial) ScheduleTx(runtimeID signature.PublicKey, txs runtime.Batch) (api.ScheduleResult, error) {
	// XXX: No notion of multiple committees yet, therefore just schedule the whole batch.
	return api.ScheduleResult{
		Scheduled: []api.ScheduledBatch{
			api.ScheduledBatch{Batch: txs},
		},
		Unscheduled: runtime.Batch{},
	}, nil
}
