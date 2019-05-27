// Package registry provides a transaction scheduler registry registry that can be used to instantiate different transaction scheduler algorithms
package registry

import (
	"fmt"

	"github.com/oasislabs/ekiden/go/worker/txnscheduler/algorithm/api"
	"github.com/oasislabs/ekiden/go/worker/txnscheduler/algorithm/trivial"
)

// AlgorithmFactory is a factory function type to create a new Algorithm.
type AlgorithmFactory func() (api.Algorithm, error)

var globalAlgorithmRegistry map[string]AlgorithmFactory

func init() {
	// Initialize the global algorithm registry.
	globalAlgorithmRegistry = make(map[string]AlgorithmFactory)

	Register("trivial", func() (api.Algorithm, error) {
		return &trivial.Trivial{}, nil
	})
}

// Register registers a new algorithm and a factory function to make a new
// instance.
func Register(name string, newAlgorithm AlgorithmFactory) {
	globalAlgorithmRegistry[name] = newAlgorithm
}

// NewAlgorithm returns a new algorithm instance based on the registred
// algorithms.
func NewAlgorithm(name string) (api.Algorithm, error) {
	factory, ok := globalAlgorithmRegistry[name]
	if !ok {
		return nil, fmt.Errorf(`invalid txn scheduler algorithm "%s"`, name)
	}
	return factory()
}
