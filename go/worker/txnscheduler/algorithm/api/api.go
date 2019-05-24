// Package api implements the transaction scheduler algorithm API.
package api

import (
	"github.com/oasislabs/ekiden/go/common/crypto/hash"
	"github.com/oasislabs/ekiden/go/common/crypto/signature"
	"github.com/oasislabs/ekiden/go/common/runtime"
)

// Algorithm defines an algorithm for scheduling incoming transaction
type Algorithm interface {
	// Initialize initializes the internal transaction scheduler state.
	Initialize() error

	// ScheduleTx attempts to schedule a batch of transactions for the given runtime.
	ScheduleTx(runtimeID signature.PublicKey, txs runtime.Batch) (ScheduleResult, error)
}

// ScheduleResult is the result of ScheduleTx containing scheduled and not-scheduled transaction batches
type ScheduleResult struct {
	Scheduled   []ScheduledBatch
	Unscheduled runtime.Batch
}

// ScheduledBatch contains scheduled batch for a specific committee
type ScheduledBatch struct {
	CommitteeID hash.Hash
	Batch       runtime.Batch
}
