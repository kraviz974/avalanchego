// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package e2e

import (
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/tests"
	"github.com/ava-labs/avalanchego/tests/fixture/tmpnet"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"
)

// TODO(marun) Find a more appropriate location for this
type AddEphemeralNodeFunc func(tc tests.TestContext, flags tmpnet.FlagsMap) *tmpnet.Node

// TODO(marun) What else does a test need? e.g. node URIs?
type APITestFunction func(
	tc tests.TestContext,
	networkID uint32,
	wallet primary.Wallet,
	ownerAddress ids.ShortID,
	nodeURI string,
	addEphemeralNode AddEphemeralNodeFunc,
)

// ExecuteAPITest executes a test primary dependency is being able to access the API of one or
// more avalanchego nodes.
func ExecuteAPITest(apiTest APITestFunction) {
	tc := NewTestContext()
	env := GetEnv(tc)
	keychain := env.NewKeychain()
	nodeURI := env.GetRandomNodeURI()
	wallet := NewWalletWithLog(tc, keychain, nodeURI, tc.Log())
	uris := make([]string, len(env.URIs))
	for i, uri := range env.URIs {
		uris[i] = uri.URI
	}
	wallet = primary.NewWalletWithOptions(
		wallet,
		common.WithVerificationURIs(uris),
	)
	networkID := env.GetNetwork().NetworkID
	apiTest(tc, networkID, *wallet, keychain.Keys[0].Address(), nodeURI.URI, AddEphemeralNode)
	_ = CheckBootstrapIsPossible(tc, env.GetNetwork())
}
