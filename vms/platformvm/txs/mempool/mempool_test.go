// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package mempool

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/codec/linearcodec"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/gas"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/utxo"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/vms/txs/mempool"
)

var avaxAssetID = ids.ID{1, 2, 3}

func newAVAXInput(txID ids.ID, amount uint64) *avax.TransferableInput {
	return &avax.TransferableInput{
		UTXOID: avax.UTXOID{
			TxID: txID,
		},
		Asset: avax.Asset{
			ID: avaxAssetID,
		},
		In: &secp256k1fx.TransferInput{
			Amt: amount,
		},
	}
}

func newTx(
	txID ids.ID,
	inputs []*avax.TransferableInput,
	outputAmount uint64,
) *txs.Tx {
	tx := avax.BaseTx{
		Ins: inputs,
	}

	if outputAmount > 0 {
		tx.Outs = []*avax.TransferableOutput{
			{
				Asset: avax.Asset{
					ID: avaxAssetID,
				},
				Out: &secp256k1fx.TransferOutput{
					Amt: outputAmount,
				},
			},
		}
	}

	return &txs.Tx{
		Unsigned: &txs.BaseTx{
			BaseTx: tx,
		},
		TxID: txID,
	}
}

// Txs should be prioritized by highest gas price during after Etna
func TestMempoolOrdering(t *testing.T) {
	require := require.New(t)

	weights := gas.Dimensions{gas.Bandwidth: 1}
	linearCodec := linearcodec.NewDefault()
	require.NoError(linearCodec.RegisterType(&secp256k1fx.TransferOutput{}))
	codecManager := codec.NewDefaultManager()
	require.NoError(codecManager.RegisterCodec(0, linearCodec))

	m, err := New(
		"",
		weights,
		1_000_000,
		avaxAssetID,
		prometheus.NewRegistry(),
	)
	require.NoError(err)

	lowTx := newTx(
		ids.GenerateTestID(),
		[]*avax.TransferableInput{newAVAXInput(ids.GenerateTestID(), 5)},
		4,
	)
	require.NoError(m.Add(lowTx))

	highTx := newTx(
		ids.GenerateTestID(),
		[]*avax.TransferableInput{newAVAXInput(ids.GenerateTestID(), 5)},
		1,
	)
	require.NoError(m.Add(highTx))

	gotTx, ok := m.Peek()
	require.True(ok)
	require.Equal(highTx, gotTx)
	m.Remove(gotTx.ID())

	gotTx, ok = m.Peek()
	require.True(ok)
	require.Equal(lowTx, gotTx)
}

func TestMempoolAdd(t *testing.T) {
	tests := []struct {
		name           string
		weights        gas.Dimensions
		maxGasCapacity gas.Gas
		prevTxs        []*txs.Tx
		tx             *txs.Tx
		wantErr        error
		wantTxIDs      []ids.ID
	}{
		{
			name: "dropped - AdvanceTimeTx",
			tx: &txs.Tx{
				Unsigned: &txs.AdvanceTimeTx{},
			},
			wantErr: utxo.ErrUnsupportedTxType,
		},
		{
			name: "dropped - RewardValidatorTx",
			tx: &txs.Tx{
				Unsigned: &txs.RewardValidatorTx{},
			},
			wantErr: utxo.ErrUnsupportedTxType,
		},
		{
			name: "dropped - no input AVAX",
			tx: newTx(
				ids.GenerateTestID(),
				[]*avax.TransferableInput{newAVAXInput(ids.GenerateTestID(), 0)},
				0,
			),
			wantErr: errMissingConsumedAVAX,
		},
		{
			name:    "dropped - no gas",
			weights: gas.Dimensions{},
			tx: newTx(
				ids.GenerateTestID(),
				[]*avax.TransferableInput{newAVAXInput(ids.GenerateTestID(), 10)},
				0,
			),
			wantErr: errNoGasUsed,
		},
		{
			name:           "conflict - lower paying tx is not added",
			weights:        gas.Dimensions{1, 1, 1, 1},
			maxGasCapacity: 200,
			prevTxs: []*txs.Tx{
				newTx(
					ids.ID{1},
					[]*avax.TransferableInput{newAVAXInput(ids.Empty, 2)},
					0,
				),
			},
			tx: newTx(
				ids.ID{2},
				[]*avax.TransferableInput{newAVAXInput(ids.Empty, 1)},
				0,
			),
			wantErr: ErrGasCapacityExceeded,
			wantTxIDs: []ids.ID{
				{1},
			},
		},
		{
			name:           "conflict - equal paying tx is not added",
			weights:        gas.Dimensions{1, 1, 1, 1},
			maxGasCapacity: 200,
			prevTxs: []*txs.Tx{
				newTx(
					ids.ID{1},
					[]*avax.TransferableInput{newAVAXInput(ids.Empty, 2)},
					0,
				),
			},
			tx: newTx(
				ids.ID{2},
				[]*avax.TransferableInput{newAVAXInput(ids.Empty, 2)},
				0,
			),
			wantErr: ErrGasCapacityExceeded,
			wantTxIDs: []ids.ID{
				{1},
			},
		},
		{
			name:           "conflict - higher paying tx is added",
			weights:        gas.Dimensions{1, 1, 1, 1},
			maxGasCapacity: 200,
			prevTxs: []*txs.Tx{
				newTx(
					ids.ID{1},
					[]*avax.TransferableInput{newAVAXInput(ids.Empty, 1)},
					0,
				),
			},
			tx: newTx(
				ids.ID{2},
				[]*avax.TransferableInput{newAVAXInput(ids.Empty, 10)},
				0,
			),
			wantTxIDs: []ids.ID{
				{2},
			},
		},
		{
			name:           "evict - higher paying tx without conflicts is added",
			weights:        gas.Dimensions{1, 1, 1, 1},
			maxGasCapacity: 200,
			prevTxs: []*txs.Tx{
				newTx(
					ids.ID{1},
					[]*avax.TransferableInput{newAVAXInput(ids.GenerateTestID(), 5)},
					0,
				),
			},
			tx: newTx(
				ids.ID{2},
				[]*avax.TransferableInput{newAVAXInput(ids.GenerateTestID(), 10)},
				0,
			),
			wantTxIDs: []ids.ID{
				{2},
			},
		},
		{
			name:           "evict - higher paying tx conflicts with multiple txs",
			weights:        gas.Dimensions{1, 1, 1, 1},
			maxGasCapacity: 500,
			prevTxs: []*txs.Tx{
				newTx(
					ids.ID{1},
					[]*avax.TransferableInput{newAVAXInput(ids.ID{1, 2, 3}, 1)},
					0,
				),
				newTx(
					ids.ID{2},
					[]*avax.TransferableInput{newAVAXInput(ids.ID{4, 5, 6}, 10)},
					0,
				),
			},
			tx: newTx(
				ids.ID{3},
				[]*avax.TransferableInput{
					newAVAXInput(ids.ID{1, 2, 3}, 2),
					newAVAXInput(ids.ID{4, 5, 6}, 2),
				},
				0,
			),
			wantErr: mempool.ErrConflictsWithOtherTx,
			wantTxIDs: []ids.ID{
				{1},
				{2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			linearCodec := linearcodec.NewDefault()
			require.NoError(linearCodec.RegisterType(&secp256k1fx.TransferOutput{}))
			codecManager := codec.NewDefaultManager()
			require.NoError(codecManager.RegisterCodec(0, linearCodec))

			m, err := New(
				"",
				tt.weights,
				tt.maxGasCapacity,
				avaxAssetID,
				prometheus.NewRegistry(),
			)
			require.NoError(err)

			for _, tx := range tt.prevTxs {
				require.NoError(m.Add(tx))
			}

			err = m.Add(tt.tx)
			require.ErrorIs(err, tt.wantErr)

			for _, wantTxID := range tt.wantTxIDs {
				_, ok := m.Get(wantTxID)
				require.True(ok)
			}

			require.Equal(len(tt.wantTxIDs), m.Len())
		})
	}
}

func TestMempool_Remove(t *testing.T) {
	tests := []struct {
		name         string
		txs          []*txs.Tx
		txIDToRemove ids.ID
		wantRemove   bool
	}{
		{
			name:         "remove tx not in mempool - empty",
			txIDToRemove: ids.GenerateTestID(),
		},
		{
			name: "remove tx not in mempool - populated",
			txs: []*txs.Tx{
				{
					Unsigned: &txs.BaseTx{
						BaseTx: avax.BaseTx{
							Ins: []*avax.TransferableInput{
								{
									UTXOID: avax.UTXOID{
										TxID: ids.GenerateTestID(),
									},
									Asset: avax.Asset{
										ID: avaxAssetID,
									},
									In: &secp256k1fx.TransferInput{
										Amt: 1,
									},
								},
							},
						},
					},
					TxID: ids.GenerateTestID(),
				},
			},
			txIDToRemove: ids.GenerateTestID(),
		},
		{
			name: "remove tx in mempool",
			txs: []*txs.Tx{
				{
					Unsigned: &txs.BaseTx{
						BaseTx: avax.BaseTx{
							Ins: []*avax.TransferableInput{
								{
									UTXOID: avax.UTXOID{
										TxID: ids.GenerateTestID(),
									},
									Asset: avax.Asset{
										ID: avaxAssetID,
									},
									In: &secp256k1fx.TransferInput{
										Amt: 1,
									},
								},
							},
						},
					},
					TxID: ids.ID{1, 2, 3},
				},
			},
			txIDToRemove: ids.ID{1, 2, 3},
			wantRemove:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			m, err := New(
				"",
				gas.Dimensions{1, 1, 1, 1},
				1_000_000,
				avaxAssetID,
				prometheus.NewRegistry(),
			)
			require.NoError(err)

			for _, tx := range tt.txs {
				require.NoError(m.Add(tx))
			}

			m.Remove(tt.txIDToRemove)
			_, gotOk := m.Get(tt.txIDToRemove)
			require.False(gotOk)
		})
	}
}

func TestMempool_RemoveConflicts(t *testing.T) {
	tests := []struct {
		name              string
		txs               []*txs.Tx
		conflictsToRemove set.Set[ids.ID]
		wantTxs           []ids.ID
	}{
		{
			name: "remove conflicts not in mempool - empty",
		},
		{
			name: "remove conflicts not in mempool - populated",
			txs: []*txs.Tx{
				{
					Unsigned: &txs.BaseTx{
						BaseTx: avax.BaseTx{
							Ins: []*avax.TransferableInput{
								{
									UTXOID: avax.UTXOID{
										TxID: ids.ID{1},
									},
									Asset: avax.Asset{
										ID: avaxAssetID,
									},
									In: &secp256k1fx.TransferInput{
										Amt: 1,
									},
								},
							},
						},
					},
					TxID: ids.ID{2},
				},
				{
					Unsigned: &txs.BaseTx{
						BaseTx: avax.BaseTx{
							Ins: []*avax.TransferableInput{
								{
									UTXOID: avax.UTXOID{
										TxID: ids.ID{3},
									},
									Asset: avax.Asset{
										ID: avaxAssetID,
									},
									In: &secp256k1fx.TransferInput{
										Amt: 1,
									},
								},
							},
						},
					},
					TxID: ids.ID{4},
				},
			},
			conflictsToRemove: set.Of[ids.ID](
				ids.ID{1}.Prefix(0),
				ids.ID{3}.Prefix(0),
			),
		},
		{
			name: "remove conflicts not in mempool - populated",
			txs: []*txs.Tx{
				{
					Unsigned: &txs.BaseTx{
						BaseTx: avax.BaseTx{
							Ins: []*avax.TransferableInput{
								{
									UTXOID: avax.UTXOID{
										TxID: ids.ID{1},
									},
									Asset: avax.Asset{
										ID: avaxAssetID,
									},
									In: &secp256k1fx.TransferInput{
										Amt: 1,
									},
								},
							},
						},
					},
					TxID: ids.ID{2},
				},
				{
					Unsigned: &txs.BaseTx{
						BaseTx: avax.BaseTx{
							Ins: []*avax.TransferableInput{
								{
									UTXOID: avax.UTXOID{
										TxID: ids.ID{3},
									},
									Asset: avax.Asset{
										ID: avaxAssetID,
									},
									In: &secp256k1fx.TransferInput{
										Amt: 1,
									},
								},
							},
						},
					},
					TxID: ids.ID{4},
				},
			},
			conflictsToRemove: set.Of[ids.ID](ids.ID{1}.Prefix(0)),
			wantTxs:           []ids.ID{{4}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			m, err := New(
				"",
				gas.Dimensions{1, 1, 1, 1},
				1_000_000,
				avaxAssetID,
				prometheus.NewRegistry(),
			)
			require.NoError(err)

			for _, tx := range tt.txs {
				require.NoError(m.Add(tx))
			}

			m.RemoveConflicts(tt.conflictsToRemove)

			require.Equal(len(tt.wantTxs), m.Len())
			for _, wantTxID := range tt.wantTxs {
				_, ok := m.Get(wantTxID)
				require.True(ok)
			}
		})
	}
}

func TestMempool_Drop(t *testing.T) {
	errFoo := errors.New("foo")

	tests := []struct {
		name       string
		droppedTxs map[ids.ID]error
		tx         ids.ID
		wantErr    error
	}{
		{
			name: "tx is dropped",
			droppedTxs: map[ids.ID]error{
				ids.Empty: errFoo,
			},
			tx:      ids.Empty,
			wantErr: errFoo,
		},
		{
			name: "tx not dropped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			m, err := New(
				"",
				gas.Dimensions{1, 1, 1, 1},
				1_000_000,
				ids.Empty,
				prometheus.NewRegistry(),
			)
			require.NoError(err)

			for txID, dropReason := range tt.droppedTxs {
				m.MarkDropped(txID, dropReason)
			}

			err = m.GetDropReason(tt.tx)
			require.ErrorIs(err, tt.wantErr)
		})
	}
}
