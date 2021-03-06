package api

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/oasislabs/oasis-core/go/common/quantity"
)

func TestConsensusParameters(t *testing.T) {
	require := require.New(t)

	// Default consensus parameters.
	var emptyParams ConsensusParameters
	require.NoError(emptyParams.SanityCheck(), "default consensus parameters should be valid")

	// Valid thresholds.
	validThresholds := ConsensusParameters{
		Thresholds: map[ThresholdKind]quantity.Quantity{
			KindEntity: *quantity.NewQuantity(),
		},
	}
	require.NoError(validThresholds.SanityCheck(), "consensus parameters with valid thresholds should be valid")

	// NOTE: There is currently no way to construct invalid thresholds.
}
