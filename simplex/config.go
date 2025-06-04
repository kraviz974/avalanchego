// Copyright (C) 2019-2025, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package simplex

import (
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/engine/snowman/block"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/logging"
)

type ValidatorInfo interface {
	GetValidatorIDs(subnetID ids.ID) []ids.NodeID
	GetValidator(subnetID ids.ID, nodeID ids.NodeID) (*validators.Validator, bool)
}

// Config wraps all the parameters needed for a simplex engine
type Config struct {
	Ctx         SimplexChainContext
	Log         logging.Logger
	Validators  ValidatorInfo
	SignBLS     func(msg []byte) (*bls.Signature, error)
	VM          block.ChainVM
	GenesisData []byte

	DB database.Database
}

type SimplexChainContext struct {
	NodeID   ids.NodeID
	ChainID  ids.ID
	SubnetID ids.ID
}
