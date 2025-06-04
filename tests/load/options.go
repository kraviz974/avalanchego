// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package load

import (
	"context"
	"time"

	"github.com/ava-labs/libevm/core/types"

	"github.com/ava-labs/avalanchego/ids"
)

// IssuanceReceipt is the information known after issuing a transaction.
type IssuanceReceipt struct {
	// ID of the issued transaction
	TxID ids.ID
	// The time from initiation to issuance
	Duration time.Duration
}

// ConfirmationReceipt is the information known after issuing and confirming a
// transaction.
type ConfirmationReceipt struct {
	// ID of the issued transaction
	TxID ids.ID
	// Receipt of the issued transaction
	Receipt *types.Receipt

	// The time from initiation to confirmation
	TotalDuration time.Duration
	// The time from issuance to confirmation. It does not include the duration
	// of issuance.
	ConfirmationDuration time.Duration
}

type Option func(*Options)

type Options struct {
	ctx context.Context

	pingFrequency time.Duration

	issuanceHandler     func(IssuanceReceipt)
	confirmationHandler func(ConfirmationReceipt)
}

func NewOptions(ops []Option) *Options {
	o := &Options{}
	o.applyOptions(ops)
	return o
}

func (o *Options) applyOptions(ops []Option) {
	for _, op := range ops {
		op(o)
	}
}

func (o *Options) Context() context.Context {
	if o.ctx != nil {
		return o.ctx
	}
	return context.Background()
}

func (o *Options) IssuanceHandler() func(IssuanceReceipt) {
	return o.issuanceHandler
}

func (o *Options) ConfirmationHandler() func(ConfirmationReceipt) {
	return o.confirmationHandler
}

func (o *Options) PingFrequency() time.Duration {
	return o.pingFrequency
}

func WithContext(ctx context.Context) Option {
	return func(o *Options) {
		o.ctx = ctx
	}
}

func WithPingFrequency(pingFrequency time.Duration) Option {
	return func(o *Options) {
		o.pingFrequency = pingFrequency
	}
}

func WithIssuanceHandler(f func(IssuanceReceipt)) Option {
	return func(o *Options) {
		o.issuanceHandler = f
	}
}

func WithConfirmationHandler(f func(ConfirmationReceipt)) Option {
	return func(o *Options) {
		o.confirmationHandler = f
	}
}
