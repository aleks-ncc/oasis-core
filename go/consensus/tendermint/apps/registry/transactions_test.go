package registry

import (
	"crypto/rand"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tendermint/iavl"
	dbm "github.com/tendermint/tm-db"

	"github.com/oasislabs/oasis-core/go/common"
	"github.com/oasislabs/oasis-core/go/common/crypto/signature"
	memorySigner "github.com/oasislabs/oasis-core/go/common/crypto/signature/signers/memory"
	"github.com/oasislabs/oasis-core/go/common/crypto/tls"
	"github.com/oasislabs/oasis-core/go/common/entity"
	"github.com/oasislabs/oasis-core/go/common/identity"
	"github.com/oasislabs/oasis-core/go/common/logging"
	"github.com/oasislabs/oasis-core/go/common/node"
	"github.com/oasislabs/oasis-core/go/consensus/tendermint/abci"
	registryState "github.com/oasislabs/oasis-core/go/consensus/tendermint/apps/registry/state"
	"github.com/oasislabs/oasis-core/go/registry/api"
)

func TestAdmissionPolicy(t *testing.T) {
	require.NoError(t, logging.Initialize(os.Stdout, logging.FmtJSON, logging.LevelDebug, nil), "logging.Initialize") // %%%
	db := dbm.NewMemDB()
	tree := iavl.NewMutableTree(db, 128)
	state := registryState.NewMutableState(tree)
	state.SetConsensusParameters(&api.ConsensusParameters{
		DebugAllowUnroutableAddresses: true,
		DebugBypassStake:              true,
	})
	app := registryApplication{
		logger: logging.GetLogger("tendermint/registry"),
		state:  nil,
	}

	ctx := abci.NewContext(abci.ContextInitChain, time.Now(), nil)
	inEntitySigner, err := memorySigner.NewSigner(rand.Reader)
	require.NoError(t, err, "memorySigner.NewSigner in entity")
	inEntity, err := entity.SignEntity(inEntitySigner, api.RegisterGenesisEntitySignatureContext, &entity.Entity{
		ID:                     inEntitySigner.Public(),
		AllowEntitySignedNodes: true,
	})
	require.NoError(t, app.registerEntity(ctx, state, inEntity), "app.registerEntity in")
	outEntitySigner, err := memorySigner.NewSigner(rand.Reader)
	require.NoError(t, err, "memorySigner.NewSigner out entity")
	outEntity, err := entity.SignEntity(outEntitySigner, api.RegisterGenesisEntitySignatureContext, &entity.Entity{
		ID:                     outEntitySigner.Public(),
		AllowEntitySignedNodes: true,
	})
	require.NoError(t, app.registerEntity(ctx, state, outEntity), "app.registerEntity out")

	anyNodeNS, err := common.NewNamespace([24]byte{'a', 'n'}, 0)
	require.NoError(t, err, "common.NewNamespace any node")
	anyNodeRT, err := api.SignRuntime(inEntitySigner, api.RegisterGenesisRuntimeSignatureContext, &api.Runtime{
		ID: anyNodeNS,
		Executor: api.ExecutorParameters{
			GroupSize: 1,
		},
		Merge: api.MergeParameters{
			GroupSize: 1,
		},
		TxnScheduler: api.TxnSchedulerParameters{
			GroupSize: 1,
		},
		Storage: api.StorageParameters{
			GroupSize: 1,
		},
		AdmissionPolicy: api.RuntimeAdmissionPolicy{
			AnyNode: &api.AnyNodeRuntimeAdmissionPolicy{},
		},
	})
	require.NoError(t, err, "api.SignRuntime any node")
	require.NoError(t, app.registerRuntime(ctx, state, anyNodeRT), "app.registerRuntime any node")
	entityWhitelistNS, err := common.NewNamespace([24]byte{'e', 'w'}, 0)
	require.NoError(t, err, "common.NewNamespace entity whitelist")
	entityWhitelistRT, err := api.SignRuntime(inEntitySigner, api.RegisterGenesisRuntimeSignatureContext, &api.Runtime{
		ID: entityWhitelistNS,
		Executor: api.ExecutorParameters{
			GroupSize: 1,
		},
		Merge: api.MergeParameters{
			GroupSize: 1,
		},
		TxnScheduler: api.TxnSchedulerParameters{
			GroupSize: 1,
		},
		Storage: api.StorageParameters{
			GroupSize: 1,
		},
		AdmissionPolicy: api.RuntimeAdmissionPolicy{
			EntityWhitelist: &api.EntityWhitelistRuntimeAdmissionPolicy{
				Entities: map[signature.PublicKey]bool{
					inEntitySigner.Public(): true,
				},
			},
		},
	})
	require.NoError(t, err, "api.SignRuntime entity whitelist")
	require.NoError(t, app.registerRuntime(ctx, state, entityWhitelistRT), "app.registerRuntime entity whitelist")
	referenceTree := tree.ImmutableTree

	fakeCert, err := tls.Generate(identity.CommonName)
	require.NoError(t, err, "tls.Generate")

	tests := []struct {
		name      string
		runtime   common.Namespace
		entity    signature.Signer
		permitted bool
	}{
		{
			"any node, any",
			anyNodeNS,
			inEntitySigner,
			true,
		},
		{
			"entity whitelist, in",
			entityWhitelistNS,
			inEntitySigner,
			true,
		},
		{
			"entity whitelist, out",
			entityWhitelistNS,
			outEntitySigner,
			false,
		},
	}
	for _, tt := range tests {
		testTree := iavl.NewMutableTree(db, 128)
		testTree.ImmutableTree = referenceTree
		signedNode, err := node.SignNode(tt.entity, api.RegisterGenesisNodeSignatureContext, &node.Node{
			EntityID: tt.entity.Public(),
			Committee: node.CommitteeInfo{
				Certificate: fakeCert.Certificate[0],
				Addresses: []node.Address{
					{
						TCPAddr: net.TCPAddr{
							IP:   net.IPv4(127, 0, 0, 1),
							Port: 26656,
							Zone: "",
						},
					},
				},
			},
			P2P: node.P2PInfo{
				ID: [32]byte{'p'},
				Addresses: []node.Address{
					{
						TCPAddr: net.TCPAddr{
							IP:   net.IPv4(127, 0, 0, 1),
							Port: 26657,
							Zone: "",
						},
					},
				},
			},
			Consensus: node.ConsensusInfo{
				ID: [32]byte{'c'},
			},
			Runtimes: []*node.Runtime{&node.Runtime{ID: tt.runtime}},
			Roles:    node.RoleComputeWorker,
		})
		require.NoError(t, err, "%s: node.SignNode", tt.name)
		err = app.registerNode(ctx, registryState.NewMutableState(testTree), signedNode)
		if tt.permitted {
			require.NoError(t, err, "%s: app.registerNode", tt.name)
		} else {
			require.Error(t, err, "%s: app.registerNode", tt.name)
		}
	}
}
