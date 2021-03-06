package committee

import (
	"github.com/oasislabs/oasis-core/go/common/accessctl"
	"github.com/oasislabs/oasis-core/go/worker/common/committee"
)

// Define storage access policies for all the relevant committees and node
// groups.
var (
	executorCommitteePolicy = &committee.AccessPolicy{
		Actions: []accessctl.Action{
			"Apply",
			"ApplyBatch",
		},
	}
	txnSchedulerCommitteePolicy = &committee.AccessPolicy{
		Actions: []accessctl.Action{
			"Apply",
			"ApplyBatch",
		},
	}
	mergeCommitteePolicy = &committee.AccessPolicy{
		Actions: []accessctl.Action{
			"Merge",
			"MergeBatch",
		},
	}
	// NOTE: GetDiff/GetCheckpoint need to be accessible to all storage nodes,
	// not just the ones in the current storage committee so that new nodes can
	// sync-up.
	storageNodesPolicy = &committee.AccessPolicy{
		Actions: []accessctl.Action{
			"GetDiff",
			"GetCheckpoint",
		},
	}
)
