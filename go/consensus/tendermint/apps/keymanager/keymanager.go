package keymanager

import (
	"bytes"
	"encoding/hex"

	"github.com/pkg/errors"
	"github.com/tendermint/tendermint/abci/types"
	"golang.org/x/crypto/sha3"

	"github.com/oasislabs/oasis-core/go/common/cbor"
	"github.com/oasislabs/oasis-core/go/common/node"
	"github.com/oasislabs/oasis-core/go/consensus/api/transaction"
	"github.com/oasislabs/oasis-core/go/consensus/tendermint/abci"
	tmapi "github.com/oasislabs/oasis-core/go/consensus/tendermint/api"
	keymanagerState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/keymanager/state"
	registryapp "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/registry"
	registryState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/registry/state"
	epochtime "github.com/oasislabs/oasis-core/go/epochtime/api"
	"github.com/oasislabs/oasis-core/go/keymanager/api"
	registry "github.com/oasislabs/oasis-core/go/registry/api"
)

var emptyHashSha3 = sha3.Sum256(nil)

type keymanagerApplication struct {
	state *abci.ApplicationState
}

func (app *keymanagerApplication) Name() string {
	return AppName
}

func (app *keymanagerApplication) ID() uint8 {
	return AppID
}

func (app *keymanagerApplication) Methods() []transaction.MethodName {
	return nil
}

func (app *keymanagerApplication) Blessed() bool {
	return false
}

func (app *keymanagerApplication) Dependencies() []string {
	return []string{registryapp.AppName}
}

func (app *keymanagerApplication) OnRegister(state *abci.ApplicationState) {
	app.state = state
}

func (app *keymanagerApplication) OnCleanup() {}

func (app *keymanagerApplication) BeginBlock(ctx *abci.Context, request types.RequestBeginBlock) error {
	if changed, epoch := app.state.EpochChanged(ctx); changed {
		return app.onEpochChange(ctx, epoch)
	}
	return nil
}

func (app *keymanagerApplication) ExecuteTx(ctx *abci.Context, tx *transaction.Transaction) error {
	// TODO: Add policy support.
	return errors.New("tendermint/keymanager: transactions not supported yet")
}

func (app *keymanagerApplication) ForeignExecuteTx(ctx *abci.Context, other abci.Application, tx *transaction.Transaction) error {
	return nil
}

func (app *keymanagerApplication) EndBlock(ctx *abci.Context, request types.RequestEndBlock) (types.ResponseEndBlock, error) {
	return types.ResponseEndBlock{}, nil
}

func (app *keymanagerApplication) FireTimer(ctx *abci.Context, timer *abci.Timer) error {
	return errors.New("tendermint/keymanager: unexpected timer")
}

func (app *keymanagerApplication) onEpochChange(ctx *abci.Context, epoch epochtime.EpochTime) error {
	// Query the runtime and node lists.
	regState := registryState.NewMutableState(ctx.State())
	runtimes, _ := regState.Runtimes()
	nodes, _ := regState.Nodes()
	registry.SortNodeList(nodes)

	// Recalculate all the key manager statuses.
	//
	// Note: This assumes that once a runtime is registered, it never expires.
	var toEmit []*api.Status
	state := keymanagerState.NewMutableState(ctx.State())
	for _, rt := range runtimes {
		if rt.Kind != registry.KindKeyManager {
			continue
		}

		var forceEmit bool
		oldStatus, err := state.Status(rt.ID)
		if err != nil {
			// This is fatal, as it suggests state corruption.
			ctx.Logger().Error("failed to query key manager status",
				"id", rt.ID,
				"err", err,
			)
			return errors.Wrap(err, "failed to query key manager status")
		}
		if oldStatus == nil {
			// This must be a new key manager runtime.
			forceEmit = true
			oldStatus = &api.Status{
				ID: rt.ID,
			}
		}

		newStatus := app.generateStatus(ctx, rt, oldStatus, nodes)
		if forceEmit || !bytes.Equal(cbor.Marshal(oldStatus), cbor.Marshal(newStatus)) {
			ctx.Logger().Debug("status updated",
				"id", newStatus.ID,
				"is_initialized", newStatus.IsInitialized,
				"is_secure", newStatus.IsSecure,
				"checksum", hex.EncodeToString(newStatus.Checksum),
				"nodes", newStatus.Nodes,
			)

			// Set, enqueue for emit.
			state.SetStatus(newStatus)
			toEmit = append(toEmit, newStatus)
		}
	}

	// Note: It may be a good idea to sweep statuses that don't have runtimes,
	// but as runtime registrations last forever, so this shouldn't be possible.

	// Emit the update event if required.
	if len(toEmit) > 0 {
		ctx.EmitEvent(tmapi.NewEventBuilder(app.Name()).Attribute(KeyStatusUpdate, cbor.Marshal(toEmit)))
	}

	return nil
}

func (app *keymanagerApplication) generateStatus(ctx *abci.Context, kmrt *registry.Runtime, oldStatus *api.Status, nodes []*node.Node) *api.Status {
	status := &api.Status{
		ID:            kmrt.ID,
		IsInitialized: oldStatus.IsInitialized,
		IsSecure:      oldStatus.IsSecure,
		Checksum:      oldStatus.Checksum,
		Policy:        oldStatus.Policy,
	}

	var rawPolicy []byte
	if status.Policy != nil {
		rawPolicy = cbor.Marshal(status.Policy)
	}
	policyHash := sha3.Sum256(rawPolicy)

	for _, n := range nodes {
		if !n.HasRoles(node.RoleKeyManager) {
			continue
		}

		var nodeRt *node.Runtime
		for _, rt := range n.Runtimes {
			if rt.ID.Equal(&kmrt.ID) {
				nodeRt = rt
				break
			}
		}
		if nodeRt == nil {
			continue
		}

		var teeOk bool
		if nodeRt.Capabilities.TEE == nil {
			teeOk = kmrt.TEEHardware == node.TEEHardwareInvalid
		} else {
			teeOk = kmrt.TEEHardware == nodeRt.Capabilities.TEE.Hardware
		}
		if !teeOk {
			ctx.Logger().Error("TEE hardware mismatch",
				"id", kmrt.ID,
				"node_id", n.ID,
			)
			continue
		}

		initResponse, err := api.VerifyExtraInfo(ctx.Logger(), kmrt, nodeRt, ctx.Now())
		if err != nil {
			ctx.Logger().Error("failed to validate ExtraInfo",
				"err", err,
				"id", kmrt.ID,
				"node_id", n.ID,
			)
			continue
		}

		var nodePolicyHash [api.ChecksumSize]byte
		switch len(initResponse.PolicyChecksum) {
		case 0:
			nodePolicyHash = emptyHashSha3
		case api.ChecksumSize:
			copy(nodePolicyHash[:], initResponse.PolicyChecksum)
		default:
			ctx.Logger().Error("failed to parse policy checksum",
				"err", err,
				"id", kmrt.ID,
				"node_id", n.ID,
			)
			continue
		}
		if policyHash != nodePolicyHash {
			ctx.Logger().Error("Policy checksum mismatch for runtime",
				"id", kmrt.ID,
				"node_id", n.ID,
			)
			continue
		}

		if status.IsInitialized {
			// Already initialized.  Check to see if it should be added to
			// the node list.
			if initResponse.IsSecure != status.IsSecure {
				ctx.Logger().Error("Security status mismatch for runtime",
					"id", kmrt.ID,
					"node_id", n.ID,
				)
				continue
			}
			if !bytes.Equal(initResponse.Checksum, status.Checksum) {
				ctx.Logger().Error("Checksum mismatch for runtime",
					"id", kmrt.ID,
					"node_id", n.ID,
				)
				continue
			}
		} else {
			// Not initialized.  The first node gets to be the source
			// of truth, every other node will sync off it.

			// Allow false -> true transitions, but not the reverse, so that
			// it is possible to set the security status in the genesis block.
			if initResponse.IsSecure != status.IsSecure && !initResponse.IsSecure {
				ctx.Logger().Error("Security status mismatch for runtime",
					"id", kmrt.ID,
					"node_id", n.ID,
				)
				continue
			}
			status.IsSecure = initResponse.IsSecure
			status.IsInitialized = true
			status.Checksum = initResponse.Checksum
		}

		status.Nodes = append(status.Nodes, n.ID)
	}

	return status
}

// New constructs a new keymanager application instance.
func New() abci.Application {
	return &keymanagerApplication{}
}
