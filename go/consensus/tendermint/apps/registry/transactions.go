package registry

import (
	"fmt"

	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/entity"
	"github.com/oasislabs/oasis-core/go/common/node"
	"github.com/oasislabs/oasis-core/go/consensus/tendermint/abci"
	"github.com/oasislabs/oasis-core/go/consensus/tendermint/api"
	registryState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/registry/state"
	stakingState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/staking/state"
	registry "github.com/oasislabs/oasis-core/go/registry/api"
	staking "github.com/oasislabs/oasis-core/go/staking/api"
)

func (app *registryApplication) registerEntity(
	ctx *abci.Context,
	state *registryState.MutableState,
	sigEnt *entity.SignedEntity,
) error {
	ent, err := registry.VerifyRegisterEntityArgs(app.logger, sigEnt, ctx.IsInitChain())
	if err != nil {
		return err
	}

	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters()
	if err != nil {
		app.logger.Error("RegisterEntity: failed to fetch consensus parameters",
			"err", err,
		)
		return err
	}
	if err = ctx.Gas().UseGas(1, registry.GasOpRegisterEntity, params.GasCosts); err != nil {
		return err
	}
	if err = ctx.Gas().UseGas(len(ent.Nodes), registry.GasOpRegisterNode, params.GasCosts); err != nil {
		return err
	}

	if !params.DebugBypassStake {
		if err = stakingState.EnsureSufficientStake(ctx, ent.ID, []staking.ThresholdKind{staking.KindEntity}); err != nil {
			app.logger.Error("RegisterEntity: Insufficent stake",
				"err", err,
				"id", ent.ID,
			)
			return err
		}
	}

	// Make sure the signer of the transaction matches the signer of the entity.
	// NOTE: If this is invoked during InitChain then there is no actual transaction
	//       and thus no transaction signer so we must skip this check.
	if !ctx.IsInitChain() && !sigEnt.Signature.PublicKey.Equal(ctx.TxSigner()) {
		return registry.ErrIncorrectTxSigner
	}

	state.SetEntity(ent, sigEnt)

	app.logger.Debug("RegisterEntity: registered",
		"entity", ent,
	)

	ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyEntityRegistered, cbor.Marshal(ent)))

	return nil
}

func (app *registryApplication) deregisterEntity(ctx *abci.Context, state *registryState.MutableState) error {
	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters()
	if err != nil {
		app.logger.Error("DeregisterEntity: failed to fetch consensus parameters",
			"err", err,
		)
		return err
	}
	if err = ctx.Gas().UseGas(1, registry.GasOpDeregisterEntity, params.GasCosts); err != nil {
		return err
	}

	id := ctx.TxSigner()

	// Prevent entity deregistration if there are any registered nodes.
	hasNodes, err := state.HasEntityNodes(id)
	if err != nil {
		app.logger.Error("DeregisterEntity: failed to check for nodes",
			"err", err,
		)
		return err
	}
	if hasNodes {
		app.logger.Error("DeregisterEntity: entity still has nodes",
			"entity_id", id,
		)
		return registry.ErrEntityHasNodes
	}

	removedEntity, err := state.RemoveEntity(id)
	if err != nil {
		return err
	}

	app.logger.Debug("DeregisterEntity: complete",
		"entity_id", id,
	)

	tagV := &EntityDeregistration{
		Entity: *removedEntity,
	}
	ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyEntityDeregistered, cbor.Marshal(tagV)))

	return nil
}

func (app *registryApplication) registerNode( // nolint: gocyclo
	ctx *abci.Context,
	state *registryState.MutableState,
	sigNode *node.SignedNode,
) error {
	if ctx.IsCheckOnly() {
		return nil
	}

	// Peek into the to-be-verified node to pull out the owning entity ID.
	var untrustedNode node.Node
	if err := cbor.Unmarshal(sigNode.Blob, &untrustedNode); err != nil {
		app.logger.Error("RegisterNode: failed to extract entity",
			"err", err,
			"signed_node", sigNode,
		)
		return err
	}
	untrustedEntity, err := state.Entity(untrustedNode.EntityID)
	if err != nil {
		app.logger.Error("RegisterNode: failed to query owning entity",
			"err", err,
			"signed_node", sigNode,
		)
		return err
	}

	params, err := state.ConsensusParameters()
	if err != nil {
		app.logger.Error("RegisterNode: failed to fetch consensus parameters",
			"err", err,
		)
		return err
	}
	newNode, paidRuntimes, err := registry.VerifyRegisterNodeArgs(
		params,
		app.logger,
		sigNode,
		untrustedEntity,
		ctx.Now(),
		ctx.IsInitChain(),
		state,
		state,
	)
	if err != nil {
		return err
	}

	// Charge gas for node registration if signed by entity. For node-signed
	// registrations, the gas charges are pre-paid by the entity.
	if sigNode.Signature.PublicKey.Equal(newNode.EntityID) {
		if err = ctx.Gas().UseGas(1, registry.GasOpRegisterNode, params.GasCosts); err != nil {
			return err
		}
	}

	// Make sure the signer of the transaction matches the signer of the node.
	// NOTE: If this is invoked during InitChain then there is no actual transaction
	//       and thus no transaction signer so we must skip this check.
	if !ctx.IsInitChain() && !sigNode.Signature.PublicKey.Equal(ctx.TxSigner()) {
		return registry.ErrIncorrectTxSigner
	}

	// Check runtime's whitelist.
	for _, nrt := range newNode.Runtimes {
		var rt *registry.Runtime
		rt, err = state.Runtime(nrt.ID)
		if err != nil {
			app.logger.Error("RegisterNode: failed to load runtime",
				"err", err,
				"runtime", nrt.ID,
			)
			return err
		}
		if rt.AdmissionPolicy.EntityWhitelist != nil && !rt.AdmissionPolicy.EntityWhitelist.Entities[newNode.EntityID] {
			app.logger.Error("RegisterNode: node's entity not in a runtime's whitelist",
				"entity", newNode.EntityID,
				"runtime", nrt.ID,
			)
			return registry.ErrForbidden
		}
	}

	// Re-check that the entity has at sufficient stake to still be an entity.
	var (
		stakeCache     *stakingState.StakeCache
		numEntityNodes int
	)
	if !params.DebugBypassStake {
		if stakeCache, err = stakingState.NewStakeCache(ctx); err != nil {
			app.logger.Error("RegisterNode: failed to instantiate stake cache",
				"err", err,
			)
			return err
		}

		if err = stakeCache.EnsureSufficientStake(newNode.EntityID, []staking.ThresholdKind{staking.KindEntity}); err != nil {
			app.logger.Error("RegisterNode: insufficient stake, entity no longer valid",
				"err", err,
				"id", newNode.EntityID,
			)
			return err
		}

		if numEntityNodes, err = state.NumEntityNodes(newNode.EntityID); err != nil {
			app.logger.Error("RegisterNode: failed to query existing nodes for entity",
				"err", err,
				"entity", newNode.EntityID,
			)
			return err
		}
	}

	// Ensure node is not expired. Even though the expiration in the current epoch is technically
	// not yet expired, we treat it as expired as it doesn't make sense to have a new node that will
	// immediately expire.
	epoch, err := app.state.GetEpoch(ctx.Ctx(), ctx.BlockHeight()+1)
	if err != nil {
		return err
	}
	if newNode.Expiration <= uint64(epoch) {
		return registry.ErrNodeExpired
	}

	// Check if node exists.
	existingNode, err := state.Node(newNode.ID)
	isNewNode := err == registry.ErrNoSuchNode
	isExpiredNode := err == nil && existingNode.IsExpired(uint64(epoch))
	if !isNewNode && err != nil {
		// Something went horribly wrong, and we failed to query the node.
		app.logger.Error("RegisterNode: failed to query node",
			"err", err,
			"new_node", newNode,
			"existing_node", existingNode,
			"entity", newNode.EntityID,
		)
		return registry.ErrInvalidArgument
	}

	// For each runtime the node registers for, require it to pay a maintenance fee for
	// each epoch the node is registered in.
	additionalEpochs := newNode.Expiration - uint64(epoch)
	if !isNewNode && !isExpiredNode {
		// Remaining epochs are credited so the node doesn't end up paying twice.
		// NOTE: This assumes that changing runtimes is not allowed as otherwise we
		//       would need to account this per-runtime.
		remainingEpochs := existingNode.Expiration - uint64(epoch)
		if additionalEpochs > remainingEpochs {
			additionalEpochs = additionalEpochs - remainingEpochs
		} else {
			additionalEpochs = 0
		}
	}
	feeCount := len(paidRuntimes) * int(additionalEpochs)
	if err = ctx.Gas().UseGas(feeCount, registry.GasOpRuntimeEpochMaintenance, params.GasCosts); err != nil {
		return err
	}

	// Create a new state checkpoint and rollback in case we fail.
	var ok bool
	sc := ctx.NewStateCheckpoint()
	defer func() {
		if !ok {
			sc.Rollback()
		}
		sc.Close()
	}()

	if isNewNode || isExpiredNode {
		// Check that the entity has enough stake for this node registration.
		if !params.DebugBypassStake {
			if err = stakeCache.EnsureNodeRegistrationStake(newNode.EntityID, numEntityNodes+1); err != nil {
				app.logger.Error("RegisterNode: insufficient stake for new node",
					"err", err,
					"entity", newNode.EntityID,
				)
				return err
			}
		}

		// Node doesn't exist (or is expired). Create node.
		if err = state.SetNode(newNode, sigNode); err != nil {
			app.logger.Error("RegisterNode: failed to create node",
				"err", err,
				"node", newNode,
				"entity", newNode.EntityID,
			)
			return registry.ErrBadEntityForNode
		}

		var status *registry.NodeStatus
		if existingNode != nil {
			// Node exists but is expired, fetch existing status.
			if status, err = state.NodeStatus(newNode.ID); err != nil {
				app.logger.Error("RegisterNode: failed to get node status",
					"err", err,
				)
				return registry.ErrInvalidArgument
			}

			// Reset expiration processed flag as the node is live again.
			status.ExpirationProcessed = false
		} else {
			// Node doesn't exist, create empty status.
			status = &registry.NodeStatus{}
		}

		if err = state.SetNodeStatus(newNode.ID, status); err != nil {
			app.logger.Error("RegisterNode: failed to set node status",
				"err", err,
			)
			return registry.ErrInvalidArgument
		}
	} else {
		// Check that the entity has enough stake for the existing node
		// registrations.
		if !params.DebugBypassStake {
			if err = stakeCache.EnsureNodeRegistrationStake(newNode.EntityID, numEntityNodes); err != nil {
				app.logger.Error("RegisterNode: insufficient stake for existing nodes",
					"err", err,
					"entity", newNode.EntityID,
				)
				return err
			}
		}

		// The node already exists, validate and update the node's entry.
		if err = registry.VerifyNodeUpdate(app.logger, existingNode, newNode); err != nil {
			app.logger.Error("RegisterNode: failed to verify node update",
				"err", err,
				"new_node", newNode,
				"existing_node", existingNode,
				"entity", newNode.EntityID,
			)
			return err
		}
		if err = state.SetNode(newNode, sigNode); err != nil {
			app.logger.Error("RegisterNode: failed to update node",
				"err", err,
				"node", newNode,
				"entity", newNode.EntityID,
			)
			return registry.ErrBadEntityForNode
		}
	}

	// If a runtime was previously suspended and this node now paid maintenance
	// fees for it, resume the runtime.
	for _, rt := range paidRuntimes {
		err := state.ResumeRuntime(rt.ID)
		switch err {
		case nil:
			app.logger.Debug("RegisterNode: resumed runtime",
				"runtime_id", rt.ID,
			)

			ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyRuntimeRegistered, cbor.Marshal(rt)))
		case registry.ErrNoSuchRuntime:
			// Runtime was not suspended.
		default:
			app.logger.Error("RegisterNode: failed to resume suspended runtime",
				"err", err,
				"runtime_id", rt.ID,
			)
			return fmt.Errorf("failed to resume suspended runtime %s: %w", rt.ID, err)
		}
	}

	ok = true

	app.logger.Debug("RegisterNode: registered",
		"node", newNode,
		"roles", newNode.Roles,
	)

	ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyNodeRegistered, cbor.Marshal(newNode)))

	return nil
}

func (app *registryApplication) unfreezeNode(
	ctx *abci.Context,
	state *registryState.MutableState,
	unfreeze *registry.UnfreezeNode,
) error {
	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	params, err := state.ConsensusParameters()
	if err != nil {
		app.logger.Error("UnfreezeNode: failed to fetch consensus parameters",
			"err", err,
		)
		return err
	}
	if err = ctx.Gas().UseGas(1, registry.GasOpUnfreezeNode, params.GasCosts); err != nil {
		return err
	}

	// Fetch node descriptor.
	node, err := state.Node(unfreeze.NodeID)
	if err != nil {
		app.logger.Error("UnfreezeNode: failed to fetch node",
			"err", err,
			"node_id", unfreeze.NodeID,
		)
		return err
	}
	// Make sure that the unfreeze request was signed by the owning entity.
	if !ctx.TxSigner().Equal(node.EntityID) {
		return registry.ErrBadEntityForNode
	}

	// Fetch node status.
	status, err := state.NodeStatus(unfreeze.NodeID)
	if err != nil {
		app.logger.Error("UnfreezeNode: failed to fetch node status",
			"err", err,
			"node_id", unfreeze.NodeID,
			"entity_id", node.EntityID,
		)
		return err
	}

	// Ensure if we can actually unfreeze.
	epoch, err := app.state.GetEpoch(ctx.Ctx(), ctx.BlockHeight()+1)
	if err != nil {
		return err
	}
	if status.FreezeEndTime > epoch {
		return registry.ErrNodeCannotBeUnfrozen
	}

	// Reset frozen status.
	status.Unfreeze()
	if err = state.SetNodeStatus(node.ID, status); err != nil {
		return err
	}

	app.logger.Debug("UnfreezeNode: unfrozen",
		"node_id", node.ID,
	)

	ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyNodeUnfrozen, cbor.Marshal(node.ID)))

	return nil
}

func (app *registryApplication) registerRuntime(
	ctx *abci.Context,
	state *registryState.MutableState,
	sigRt *registry.SignedRuntime,
) error {
	params, err := state.ConsensusParameters()
	if err != nil {
		app.logger.Error("RegisterRuntime: failed to fetch consensus parameters",
			"err", err,
		)
		return err
	}

	rt, err := registry.VerifyRegisterRuntimeArgs(params, app.logger, sigRt, ctx.IsInitChain())
	if err != nil {
		return err
	}

	if rt.Kind == registry.KindCompute {
		if err = registry.VerifyRegisterComputeRuntimeArgs(app.logger, rt, state); err != nil {
			return err
		}
	}

	if ctx.IsCheckOnly() {
		return nil
	}

	// Charge gas for this transaction.
	if err = ctx.Gas().UseGas(1, registry.GasOpRegisterRuntime, params.GasCosts); err != nil {
		return err
	}

	// Make sure the signer of the transaction matches the signer of the runtime.
	// NOTE: If this is invoked during InitChain then there is no actual transaction
	//       and thus no transaction signer so we must skip this check.
	if !ctx.IsInitChain() && !sigRt.Signature.PublicKey.Equal(ctx.TxSigner()) {
		return registry.ErrIncorrectTxSigner
	}

	// If TEE is required, check if runtime provided at least one enclave ID.
	if rt.TEEHardware != node.TEEHardwareInvalid {
		switch rt.TEEHardware {
		case node.TEEHardwareIntelSGX:
			var vi registry.VersionInfoIntelSGX
			if err = cbor.Unmarshal(rt.Version.TEE, &vi); err != nil {
				return err
			}
			if len(vi.Enclaves) == 0 {
				return registry.ErrNoEnclaveForRuntime
			}
		}
	}

	// Make sure the runtime doesn't exist yet.
	var suspended bool
	existingRt, err := state.SignedRuntime(rt.ID)
	switch err {
	case nil:
	case registry.ErrNoSuchRuntime:
		// Make sure the runtime isn't suspended.
		existingRt, err = state.SignedSuspendedRuntime(rt.ID)
		switch err {
		case nil:
			suspended = true
		case registry.ErrNoSuchRuntime:
		default:
			return fmt.Errorf("failed to fetch suspended runtime: %w", err)
		}
	default:
		return fmt.Errorf("failed to fetch runtime: %w", err)
	}
	// If there is an existing runtime, verify update.
	if existingRt != nil {
		err = registry.VerifyRuntimeUpdate(app.logger, existingRt, sigRt, rt)
		if err != nil {
			return err
		}
	}

	if err = state.SetRuntime(rt, sigRt, suspended); err != nil {
		app.logger.Error("RegisterRuntime: failed to create runtime",
			"err", err,
			"runtime", rt,
			"entity", sigRt.Signature.PublicKey,
		)
		return registry.ErrBadEntityForRuntime
	}

	if !suspended {
		app.logger.Debug("RegisterRuntime: registered",
			"runtime", rt,
		)

		ctx.EmitEvent(api.NewEventBuilder(app.Name()).Attribute(KeyRuntimeRegistered, cbor.Marshal(rt)))
	}

	return nil
}
