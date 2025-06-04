// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package load

import (
	"context"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/ava-labs/avalanchego/utils/logging"
)

type Orchestrator struct {
	wallets  []*Sender
	builders []Builder
	tracker  *Tracker
	log      logging.Logger
}

func NewOrchestrator(
	wallets []*Sender,
	builders []Builder,
	tracker *Tracker,
	log logging.Logger,
) *Orchestrator {
	return &Orchestrator{
		wallets:  wallets,
		builders: builders,
		tracker:  tracker,
		log:      log,
	}
}

func (g *Orchestrator) Run(ctx context.Context) error {
	g.log.Debug("starting run")
	issuanceF := func(i IssuanceReceipt) {
		g.tracker.LogIssuance(i)
	}

	confirmationF := func(c ConfirmationReceipt) {
		g.tracker.LogConfirmation(c)
	}

	eg, cctx := errgroup.WithContext(ctx)

	for i := range g.wallets {
		eg.Go(func() error {
			for {
				select {
				case <-cctx.Done():
					return nil
				default:
				}

				if err := g.wallets[i].SendTx(
					ctx,
					g.builders[i],
					WithContext(cctx),
					WithPingFrequency(500*time.Millisecond),
					WithIssuanceHandler(issuanceF),
					WithConfirmationHandler(confirmationF),
				); err != nil {
					return err
				}
			}
		})
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	prevTotalGasUsed := g.tracker.TotalGasUsed()
	prevTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		currTotalGasUsed := g.tracker.TotalGasUsed()
		currTime := time.Now()

		gps := computeTPS(prevTotalGasUsed, currTotalGasUsed, currTime.Sub(prevTime))
		g.log.Info("stats", zap.Uint64("gps", gps))

		prevTime = currTime
		prevTotalGasUsed = currTotalGasUsed
	}
}

func computeTPS(initial uint64, final uint64, duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}

	numTxs := final - initial
	tps := float64(numTxs) / duration.Seconds()

	return uint64(tps)
}
