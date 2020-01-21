package supplementarysanity

import (
	"fmt"

	"github.com/tendermint/iavl"

	"github.com/oasislabs/oasis-core/go/common"
	"github.com/oasislabs/oasis-core/go/common/quantity"
	keymanagerState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/keymanager/state"
	registryState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/registry/state"
	roothashState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/roothash/state"
	stakingState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/staking/state"
	epochtime "github.com/oasislabs/oasis-core/go/epochtime/api"
	keymanager "github.com/oasislabs/oasis-core/go/keymanager/api"
	registry "github.com/oasislabs/oasis-core/go/registry/api"
	roothash "github.com/oasislabs/oasis-core/go/roothash/api"
	"github.com/oasislabs/oasis-core/go/roothash/api/block"
	staking "github.com/oasislabs/oasis-core/go/staking/api"
)

func checkEpochTime(state *iavl.MutableTree, now epochtime.EpochTime) error {
	if now == epochtime.EpochInvalid {
		return fmt.Errorf("current epoch is invalid")
	}

	// nothing to check yet
	return nil
}

func checkRegistry(state *iavl.MutableTree, now epochtime.EpochTime) error {
	st := registryState.NewMutableState(state)

	// Check entities.
	entities, err := st.SignedEntities()
	if err != nil {
		return fmt.Errorf("SignedEntities: %w", err)
	}
	_, err = registry.SanityCheckEntities(entities)
	if err != nil {
		return fmt.Errorf("SanityCheckEntities: %w", err)
	}

	// Check runtimes.
	runtimes, err := st.AllSignedRuntimes()
	if err != nil {
		return fmt.Errorf("AllSignedRuntimes: %w", err)
	}
	err = registry.SanityCheckRuntimes(runtimes)
	if err != nil {
		return fmt.Errorf("SanityCheckRuntimes: %w", err)
	}

	// Check nodes.
	// nodes, err := st.SignedNodes()
	// if err != nil {
	// 	return fmt.Errorf("SignedNodes: %w", err)
	// }

	/* // TODO
	err = registry.SanityCheckNodes(nodes, seenEntities, seenRuntimes)
	if err != nil {
		return fmt.Errorf("SanityCheckNodes: %w", err)
	}*/

	return nil
}

func checkRootHash(state *iavl.MutableTree, now epochtime.EpochTime) error {
	st := roothashState.NewMutableState(state)

	// Check blocks.
	runtimes := st.Runtimes()

	blocks := make(map[common.Namespace]*block.Block)
	for _, rt := range runtimes {
		blocks[rt.Runtime.ID] = rt.CurrentBlock
	}
	err := roothash.SanityCheckBlocks(blocks)
	if err != nil {
		return fmt.Errorf("SanityCheckBlocks: %w", err)
	}

	// nothing to check yet
	return nil
}

func checkStaking(state *iavl.MutableTree, now epochtime.EpochTime) error {
	st := stakingState.NewMutableState(state)

	parameters, err := st.ConsensusParameters()
	if err != nil {
		return fmt.Errorf("ConsensusParameters: %w", err)
	}

	totalSupply, err := st.TotalSupply()
	if err != nil {
		return fmt.Errorf("TotalSupply: %w", err)
	}
	if !totalSupply.IsValid() {
		return fmt.Errorf("total supply %v is invalid", totalSupply)
	}

	commonPool, err := st.CommonPool()
	if err != nil {
		return fmt.Errorf("CommonPool: %w", err)
	}
	if !commonPool.IsValid() {
		return fmt.Errorf("common pool %v is invalid", commonPool)
	}

	// Check if the total supply adds up (common pool + all balances in the ledger).
	// Check all commission schedules.
	var total quantity.Quantity
	accounts, err := st.Accounts()
	if err != nil {
		return fmt.Errorf("Accounts: %w", err)
	}
	for _, id := range accounts {
		err = staking.SanityCheckAccount(&total, parameters, now, id, st.Account(id))
		if err != nil {
			return fmt.Errorf("SanityCheckAccount %s: %w", id, err)
		}
	}

	totalFees, err := st.LastBlockFees()
	if err != nil {
		return fmt.Errorf("LastBlockFees: %w", err)
	}
	if !totalFees.IsValid() {
		return fmt.Errorf("common pool %v is invalid", commonPool)
	}

	_ = total.Add(commonPool)
	_ = total.Add(totalFees)
	if total.Cmp(totalSupply) != 0 {
		return fmt.Errorf("balances in accounts plus common pool (%s) does not add up to total supply (%s)", total.String(), totalSupply.String())
	}

	// All shares of all delegations for a given account must add up to account's Escrow.Active.TotalShares.
	delegationses, err := st.Delegations()
	if err != nil {
		return fmt.Errorf("Delegations: %w", err)
	}
	for acct, delegations := range delegationses {
		err = staking.SanityCheckDelegations(st.Account(acct), delegations)
		if err != nil {
			return fmt.Errorf("SanityCheckDelegations %s: %w", acct, err)
		}
	}

	// All shares of all debonding delegations for a given account must add up to account's Escrow.Debonding.TotalShares.
	debondingDelegationses, err := st.DebondingDelegations()
	if err != nil {
		return fmt.Errorf("DebondingDelegations: %w", err)
	}
	for acct, debondingDelegations := range debondingDelegationses {
		err := staking.SanityCheckDebondingDelegations(st.Account(acct), debondingDelegations)
		if err != nil {
			return fmt.Errorf("SanityCheckDebondingDelegations %s: %w", acct, err)
		}
	}

	// Check the above two invariants for each account as well.
	for _, id := range accounts {
		err := staking.SanityCheckAccountShares(st.Account(id), delegationses[id], debondingDelegationses[id])
		if err != nil {
			return fmt.Errorf("SanityCheckAccountShares %s: %w", id, err)
		}
	}

	return nil
}

func checkKeyManager(state *iavl.MutableTree, now epochtime.EpochTime) error {
	st := keymanagerState.NewMutableState(state)

	statuses, err := st.Statuses()
	if err != nil {
		return fmt.Errorf("Statuses: %w", err)
	}
	err = keymanager.SanityCheckStatuses(statuses)
	if err != nil {
		return fmt.Errorf("SanityCheckStatuses: %w", err)
	}

	return nil
}

func checkScheduler(*iavl.MutableTree, epochtime.EpochTime) error {
	// nothing to check yet
	return nil
}

func checkBeacon(*iavl.MutableTree, epochtime.EpochTime) error {
	// nothing to check yet
	return nil
}

func checkConsensus(*iavl.MutableTree, epochtime.EpochTime) error {
	// nothing to check yet
	return nil
}

func checkHalt(*iavl.MutableTree, epochtime.EpochTime) error {
	// nothing to check yet
	return nil
}
