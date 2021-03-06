package e2e

import (
	"github.com/oasislabs/oasis-core/go/oasis-test-runner/log"
	"github.com/oasislabs/oasis-core/go/oasis-test-runner/oasis"
	"github.com/oasislabs/oasis-core/go/oasis-test-runner/scenario"
)

var (
	// Sentry is the Tendermint Sentry node scenario.
	Sentry scenario.Scenario = newSentryImpl()

	ValidatorExtraLogWatcherHandlerFactories = []log.WatcherHandlerFactory{
		oasis.LogAssertPeerExchangeDisabled(),
	}
)

type sentryImpl struct {
	basicImpl
}

func newSentryImpl() scenario.Scenario {
	return &sentryImpl{
		basicImpl: *newBasicImpl("sentry", "simple-keyvalue-client", nil),
	}
}

func (s *sentryImpl) Fixture() (*oasis.NetworkFixture, error) {
	f, err := s.basicImpl.Fixture()
	if err != nil {
		return nil, err
	}

	// Provision sentry nodes and validators with the following topology:
	//
	//                          +----------+
	//                     +--->| Sentry 0 |
	// +-------------+     |    +----------+
	// | Validator 0 +<----+    +----------+
	// |             +<-------->+ Sentry 1 |
	// +-------------+          +----------+
	//
	// +-------------+
	// | Validator 1 +<----+
	// +-------------+     |    +----------+
	// +-------------+     +--->+ Sentry 2 |
	// | Validator 2 +<-------->+          |
	// +-------------+          +----------+

	f.Sentries = []oasis.SentryFixture{
		oasis.SentryFixture{
			Validators: []int{0},
		},
		oasis.SentryFixture{
			Validators: []int{0},
		},
		oasis.SentryFixture{
			Validators: []int{1, 2},
		},
	}
	f.Validators = []oasis.ValidatorFixture{
		oasis.ValidatorFixture{
			Entity:                     1,
			LogWatcherHandlerFactories: ValidatorExtraLogWatcherHandlerFactories,
			Sentries:                   []int{0, 1},
		},
		oasis.ValidatorFixture{
			Entity:                     1,
			LogWatcherHandlerFactories: ValidatorExtraLogWatcherHandlerFactories,
			Sentries:                   []int{2},
		},
		oasis.ValidatorFixture{
			Entity:                     1,
			LogWatcherHandlerFactories: ValidatorExtraLogWatcherHandlerFactories,
			Sentries:                   []int{2},
		},
	}

	return f, nil
}
