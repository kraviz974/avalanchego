// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/wallet/chain/p/wallet"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"
)

var _ wallet.Client = (*Client)(nil)

const chainAlias = "P"

func NewClient(
	c platformvm.Client,
	b wallet.Backend,
) *Client {
	return &Client{
		client:  c,
		backend: b,
	}
}

type Client struct {
	client  platformvm.Client
	backend wallet.Backend
}

func (c *Client) IssueTx(
	tx *txs.Tx,
	options ...common.Option,
) error {
	ops := common.NewOptions(options)
	ctx := ops.Context()
	startTime := time.Now()
	txID, err := c.client.IssueTx(ctx, tx.Bytes())
	issuanceDuration := time.Since(startTime)
	if err != nil {
		return err
	}

	if f := ops.PostIssuanceHandler(); f != nil {
		f(chainAlias, txID, issuanceDuration)
	}

	if ops.AssumeDecided() {
		return c.backend.AcceptTx(ctx, tx)
	}

	if uris := ops.VerificationURIs(); len(uris) > 0 {
		// Verify the transaction more extensively against the provided URIs
		if err := c.awaitTxAccepted(ctx, txID, uris, ops); err != nil {
			return err
		}
	} else {
		// Wait for acceptance with the wallet client
		if err := platformvm.AwaitTxAccepted(c.client, ctx, txID, ops.PollFrequency()); err != nil {
			return err
		}
	}

	totalDuration := time.Since(startTime)
	issuanceToConfirmationDuration := totalDuration - issuanceDuration

	if f := ops.PostConfirmationHandler(); f != nil {
		f(chainAlias, txID, totalDuration, issuanceToConfirmationDuration)
	}

	return c.backend.AcceptTx(ctx, tx)
}

// Verify the acceptance of the transaction on the provided URIs.
func (c *Client) awaitTxAccepted(ctx context.Context, txID ids.ID, uris []string, ops *common.Options) error {
	log := ops.Log()

	for _, uri := range uris {
		client := platformvm.NewClient(uri)
		if err := platformvm.AwaitTxAccepted(client, ctx, txID, ops.PollFrequency()); err != nil {
			return fmt.Errorf("failed to confirm P-chain transaction %s on %s: %w", txID, uri, err)
		}
		log.Info("confirmed transaction",
			zap.String("chainAlias", string(common.PChainAlias)),
			zap.Stringer("txID", txID),
			zap.String("uri", uri),
		)
	}
	log.Info("confirmed transaction",
		zap.String("chainAlias", string(common.PChainAlias)),
		zap.Stringer("txID", txID),
	)

	return nil
}
