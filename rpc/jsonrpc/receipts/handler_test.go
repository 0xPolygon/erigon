// Copyright 2015 The go-ethereum Authors
// (original work)
// Copyright 2024 The Erigon Authors
// (modifications)
// This file is part of Erigon.
//
// Erigon is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Erigon is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with Erigon. If not, see <http://www.gnu.org/licenses/>.

package receipts_test

import (
	"math"
	"math/big"
	"testing"
	"time"

	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/common/empty"
	"github.com/erigontech/erigon-lib/crypto"
	"github.com/erigontech/erigon-lib/gointerfaces/sentryproto"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon/core"
	"github.com/erigontech/erigon/db/kv"
	"github.com/erigontech/erigon/db/rawdb"
	"github.com/erigontech/erigon/db/state"
	"github.com/erigontech/erigon/execution/chain"
	"github.com/erigontech/erigon/execution/chain/params"
	"github.com/erigontech/erigon/execution/rlp"
	"github.com/erigontech/erigon/execution/stages/mock"
	"github.com/erigontech/erigon/execution/types"
	"github.com/erigontech/erigon/node/direct"
	"github.com/erigontech/erigon/p2p/protocols/eth"
	"github.com/erigontech/erigon/rpc/jsonrpc/receipts"
)

var (
	// testKey is a private key to use for funding a tester account.
	testKey, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")

	// testAddr is the Ethereum address of the tester account.
	testAddr = crypto.PubkeyToAddress(testKey.PublicKey)
)

func TestGetBlockHeaders(t *testing.T) {
	// Create a batch of tests for various scenarios
	limit := uint64(100)
	backend := mockWithGenerator(t, int(limit), nil)
	tx, err := backend.DB.BeginTemporalRo(backend.Ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	// Create a "random" unknown hash for testing
	var unknown common.Hash
	for i := range unknown {
		unknown[i] = byte(i)
	}

	var blocks []*types.Block
	for i := uint64(0); i < limit; i++ {
		block, err := backend.BlockReader.BlockByNumber(backend.Ctx, tx, i)
		require.NoError(t, err)
		blocks = append(blocks, block)
	}

	currentBlock, err := backend.BlockReader.CurrentBlock(tx)
	require.NoError(t, err)

	getHashes := func(from, limit uint64) (hashes []common.Hash) {
		for i := uint64(0); i < limit; i++ {
			hashes = append(hashes, blocks[from-1-i].Hash())
		}
		return hashes
	}

	tests := []struct {
		query  *eth.GetBlockHeadersPacket // The query to execute for header retrieval
		expect []common.Hash              // The hashes of the block whose headers are expected
	}{
		// A single random block should be retrievable by hash
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Hash: blocks[limit/2].Hash()}, Amount: 1},
			[]common.Hash{blocks[limit/2].Hash()},
		},
		// A single random block should be retrievable by number
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: limit / 2}, Amount: 1},
			[]common.Hash{blocks[limit/2].Hash()},
		},
		// Multiple headers should be retrievable in both directions
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: limit / 2}, Amount: 3},
			[]common.Hash{
				blocks[limit/2].Hash(),
				blocks[limit/2+1].Hash(),
				blocks[limit/2+2].Hash(),
			},
		},
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: limit / 2}, Amount: 3, Reverse: true},
			[]common.Hash{
				blocks[limit/2].Hash(),
				blocks[limit/2-1].Hash(),
				blocks[limit/2-2].Hash(),
			},
		},
		// Multiple headers with skip lists should be retrievable
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: limit / 2}, Skip: 3, Amount: 3},
			[]common.Hash{
				blocks[limit/2].Hash(),
				blocks[limit/2+4].Hash(),
				blocks[limit/2+8].Hash(),
			},
		},
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: limit / 2}, Skip: 3, Amount: 3, Reverse: true},
			[]common.Hash{
				blocks[limit/2].Hash(),
				blocks[limit/2-4].Hash(),
				blocks[limit/2-8].Hash(),
			},
		},
		// The chain endpoints should be retrievable
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 0}, Amount: 1},
			[]common.Hash{blocks[0].Hash()},
		},
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: currentBlock.Number().Uint64()}, Amount: 1},
			[]common.Hash{currentBlock.Hash()},
		},
		{ // If the peer requests a bit into the future, we deliver what we have
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: currentBlock.Number().Uint64()}, Amount: 10},
			[]common.Hash{currentBlock.Hash()},
		},
		// Ensure protocol limits are honored
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: currentBlock.Number().Uint64() - 1}, Amount: limit + 10, Reverse: true},
			getHashes(currentBlock.Number().Uint64(), limit),
		},
		// Check that requesting more than available is handled gracefully
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: currentBlock.Number().Uint64() - 4}, Skip: 3, Amount: 3},
			[]common.Hash{
				blocks[currentBlock.Number().Uint64()-4].Hash(),
				currentBlock.Hash(),
			},
		},
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 4}, Skip: 3, Amount: 3, Reverse: true},
			[]common.Hash{
				blocks[4].Hash(),
				blocks[0].Hash(),
			},
		},
		// Check that requesting more than available is handled gracefully, even if mid skip
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: currentBlock.Number().Uint64() - 4}, Skip: 2, Amount: 3},
			[]common.Hash{
				blocks[currentBlock.Number().Uint64()-4].Hash(),
				blocks[currentBlock.Number().Uint64()-1].Hash(),
			},
		}, {
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 4}, Skip: 2, Amount: 3, Reverse: true},
			[]common.Hash{
				blocks[4].Hash(),
				blocks[1].Hash(),
			},
		},
		// Check a corner case where requesting more can iterate past the endpoints
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 2}, Amount: 5, Reverse: true},
			[]common.Hash{
				blocks[2].Hash(),
				blocks[1].Hash(),
				blocks[0].Hash(),
			},
		},
		// Check a corner case where skipping causes overflow with reverse=false
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 1}, Amount: 2, Reverse: false, Skip: math.MaxUint64 - 1},
			[]common.Hash{
				blocks[1].Hash(),
			},
		},
		// Check a corner case where skipping causes overflow with reverse=true
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 1}, Amount: 2, Reverse: true, Skip: math.MaxUint64 - 1},
			[]common.Hash{
				blocks[1].Hash(),
			},
		},
		// Check another corner case where skipping causes overflow with reverse=false
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 1}, Amount: 2, Reverse: false, Skip: math.MaxUint64},
			[]common.Hash{
				blocks[1].Hash(),
			},
		},
		// Check another corner case where skipping causes overflow with reverse=true
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: 1}, Amount: 2, Reverse: true, Skip: math.MaxUint64},
			[]common.Hash{
				blocks[1].Hash(),
			},
		},
		// Check a corner case where skipping overflow loops back into the chain start
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Hash: blocks[3].Hash()}, Amount: 2, Reverse: false, Skip: math.MaxUint64 - 1},
			[]common.Hash{
				blocks[3].Hash(),
			},
		},
		// Check a corner case where skipping overflow loops back to the same header
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Hash: blocks[1].Hash()}, Amount: 2, Reverse: false, Skip: math.MaxUint64},
			[]common.Hash{
				blocks[1].Hash(),
			},
		},
		// Check that non existing headers aren't returned
		{
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Hash: unknown}, Amount: 1},
			[]common.Hash{},
		}, {
			&eth.GetBlockHeadersPacket{Origin: eth.HashOrNumber{Number: currentBlock.Number().Uint64() + 1}, Amount: 1},
			[]common.Hash{},
		},
	}
	for i, tt := range tests {
		_ = i
		var expectedHeaders []*types.Header
		for _, hash := range tt.expect {
			block, err := backend.BlockReader.BlockByHash(backend.Ctx, tx, hash)
			require.NoError(t, err)
			expectedHeaders = append(expectedHeaders, block.Header())
		}
		backend.StreamWg.Wait()
		backend.ReceiveWg.Add(1)
		encodedMessage, err := rlp.EncodeToBytes(eth.GetBlockHeadersPacket66{RequestId: 1, GetBlockHeadersPacket: tt.query})
		require.NoError(t, err)
		for _, err = range backend.Send(&sentryproto.InboundMessage{Id: eth.ToProto[direct.ETH68][eth.GetBlockHeadersMsg], Data: encodedMessage, PeerId: backend.PeerId}) {
			require.NoError(t, err)
		}
		expect, err := rlp.EncodeToBytes(eth.BlockHeadersPacket66{RequestId: 1, BlockHeadersPacket: expectedHeaders})
		require.NoError(t, err)
		backend.ReceiveWg.Wait()
		sentMessage := backend.SentMessage(i)
		require.Equal(t, eth.ToProto[backend.SentryClient.Protocol()][eth.BlockHeadersMsg], sentMessage.Id)
		require.Equal(t, expect, sentMessage.Data)
	}
}

func TestGetBlockReceipts(t *testing.T) {
	// Define three accounts to simulate transactions with
	acc1Key, _ := crypto.HexToECDSA("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
	acc2Key, _ := crypto.HexToECDSA("49a7b37aa6f6645917e7b807e9d1c00d4fa71f18343b0d4122a4d2df64dd6fee")
	acc1Addr := crypto.PubkeyToAddress(acc1Key.PublicKey)
	acc2Addr := crypto.PubkeyToAddress(acc2Key.PublicKey)

	signer := types.LatestSignerForChainID(nil)
	// Create a chain generator with some simple transactions (blatantly stolen from @fjl/chain_markets_test)
	generator := func(i int, block *core.BlockGen) {
		switch i {
		case 0:
			// In block 1, the test bank sends account #1 some ether.
			tx, _ := types.SignTx(types.NewTransaction(block.TxNonce(testAddr), acc1Addr, uint256.NewInt(10000), params.TxGas, nil, nil), *signer, testKey)
			block.AddTx(tx)
		case 1:
			// In block 2, the test bank sends some more ether to account #1.
			// acc1Addr passes it on to account #2.
			tx1, _ := types.SignTx(types.NewTransaction(block.TxNonce(testAddr), acc1Addr, uint256.NewInt(1000), params.TxGas, nil, nil), *signer, testKey)
			tx2, _ := types.SignTx(types.NewTransaction(block.TxNonce(acc1Addr), acc2Addr, uint256.NewInt(1000), params.TxGas, nil, nil), *signer, acc1Key)
			block.AddTx(tx1)
			block.AddTx(tx2)
		case 2:
			// Block 3 is empty but was mined by account #2.
			block.SetCoinbase(acc2Addr)
			block.SetExtra([]byte("yeehaw"))
		case 3:
			// Block 4 includes blocks 2 and 3 as uncle headers (with modified extra data).
			b2 := block.PrevBlock(1).Header()
			b2.Extra = []byte("foo")
			block.AddUncle(b2)
			b3 := block.PrevBlock(2).Header()
			b3.Extra = []byte("foo")
			block.AddUncle(b3)
		}
	}
	// Assemble the test environment
	m := mockWithGenerator(t, 4, generator)
	receiptsGetter := receipts.NewGenerator(m.BlockReader, m.Engine, time.Minute, nil)
	// Collect the hashes to request, and the response to expect
	var (
		hashes   []common.Hash
		receipts []rlp.RawValue
	)
	tx, err := m.DB.BeginTemporalRo(m.Ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	for i := uint64(0); i <= rawdb.ReadCurrentHeader(tx).Number.Uint64(); i++ {
		block, err := m.BlockReader.BlockByNumber(m.Ctx, tx, i)
		require.NoError(t, err)

		hashes = append(hashes, block.Hash())
		// If known, encode and queue for response packet

		r, err := receiptsGetter.GetReceipts(m.Ctx, m.ChainConfig, tx, block)
		require.NoError(t, err)
		encoded, err := rlp.EncodeToBytes(r)
		require.NoError(t, err)
		receipts = append(receipts, encoded)
	}

	require.NoError(t, err)
	b, err := rlp.EncodeToBytes(eth.GetReceiptsPacket66{RequestId: 1, GetReceiptsPacket: hashes})
	require.NoError(t, err)

	m.StreamWg.Wait()

	m.ReceiveWg.Add(1)
	// Send the hash request and verify the response
	for _, err = range m.Send(&sentryproto.InboundMessage{Id: eth.ToProto[direct.ETH67][eth.GetReceiptsMsg], Data: b, PeerId: m.PeerId}) {
		require.NoError(t, err)
	}

	expect, err := rlp.EncodeToBytes(eth.ReceiptsRLPPacket66{RequestId: 1, ReceiptsRLPPacket: receipts})
	require.NoError(t, err)
	m.ReceiveWg.Wait()
	sent := m.SentMessage(0)
	require.Equal(t, eth.ToProto[m.SentryClient.Protocol()][eth.ReceiptsMsg], sent.Id)
	require.Equal(t, expect, sent.Data)
}

func TestReadStateSyncReceiptByHash_Found(t *testing.T) {
	m := mock.Mock(t)
	// Enable receipt cache domain
	m.DB.(state.HasAgg).Agg().(*state.Aggregator).EnableDomain(kv.RCacheDomain)

	txRw, err := m.DB.BeginTemporalRw(m.Ctx)
	require.NoError(t, err)
	defer txRw.Rollback()
	ctx := m.Ctx
	br := m.BlockReader
	txNumReader := br.TxnumReader(ctx)

	// Create a fake (legacy) transaction
	tx := types.NewTransaction(
		0,
		common.HexToAddress("0x01"),
		uint256.NewInt(0),
		21000,
		uint256.NewInt(1),
		nil,
	)
	body := &types.Body{Transactions: types.Transactions{tx}}
	header := &types.Header{
		Number:      big.NewInt(1),
		TxHash:      types.DeriveSha(types.Transactions{tx}),
		UncleHash:   empty.UncleHash,
		ReceiptHash: empty.RootHash,
	}

	// Write header and body to DB
	require.NoError(t, rawdb.WriteCanonicalHash(txRw, header.Hash(), 1))
	require.NoError(t, rawdb.WriteHeader(txRw, header))
	require.NoError(t, rawdb.WriteBody(txRw, header.Hash(), 1, body))

	// Create a state-sync receipt (Type == 0 as legacy, CumulativeGasUsed == 0)
	ssr := &types.Receipt{
		Type:              0,
		CumulativeGasUsed: 0,
		TxHash:            tx.Hash(),
		BlockHash:         header.Hash(),
		BlockNumber:       header.Number,
		TransactionIndex:  0,
		Status:            types.ReceiptStatusSuccessful,
	}
	ssr.Bloom = types.CreateBloom(types.Receipts{ssr})

	// Insert into the receipt cache
	sd, err := state.NewSharedDomains(txRw, log.New())
	require.NoError(t, err)
	defer sd.Close()

	base, err := txNumReader.Min(txRw, 1)
	require.NoError(t, err)

	require.NoError(t, rawdb.WriteReceiptCacheV2(sd.AsPutDel(txRw), nil, base))
	require.NoError(t, rawdb.WriteReceiptCacheV2(sd.AsPutDel(txRw), ssr, base+1))
	require.NoError(t, rawdb.WriteReceiptCacheV2(sd.AsPutDel(txRw), nil, base+2))

	_, err = sd.ComputeCommitment(ctx, true, header.Number.Uint64(), base+2, "flush")
	require.NoError(t, err)
	require.NoError(t, sd.Flush(ctx, txRw))

	// Build the block
	b := types.NewBlockFromStorage(header.Hash(), header, body.Transactions, body.Uncles, nil)
	require.NotNil(t, b)

	// Assert state-sync receipt is found
	got, err := rawdb.ReadReceiptsCacheV2(txRw, b, txNumReader)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, got.Len(), 1)

	r := got[0]

	require.Equal(t, tx.Hash(), r.TxHash)
	require.Equal(t, header.Hash(), r.BlockHash)
	require.Equal(t, header.Number.Uint64(), r.BlockNumber.Uint64())
}

func TestReadStateSyncReceiptByHash_NoStateSync(t *testing.T) {
	m := mock.Mock(t)
	m.DB.(state.HasAgg).Agg().(*state.Aggregator).EnableDomain(kv.RCacheDomain)

	txRw, err := m.DB.BeginTemporalRw(m.Ctx)
	require.NoError(t, err)
	defer txRw.Rollback()

	ctx := m.Ctx
	br := m.BlockReader
	txNumReader := br.TxnumReader(ctx)

	// Single transaction block
	tx := types.NewTransaction(0, common.HexToAddress("0x02"), uint256.NewInt(0), 21000, uint256.NewInt(1), nil)
	body := &types.Body{Transactions: types.Transactions{tx}}

	header := &types.Header{
		Number:      big.NewInt(2),
		TxHash:      types.DeriveSha(types.Transactions(body.Transactions)),
		UncleHash:   empty.UncleHash,
		ReceiptHash: empty.RootHash,
	}

	require.NoError(t, rawdb.WriteCanonicalHash(txRw, header.Hash(), 2))
	require.NoError(t, rawdb.WriteHeader(txRw, header))
	require.NoError(t, rawdb.WriteBody(txRw, header.Hash(), 2, body))

	// No state-sync receipt (CumulativeGasUsed != 0)
	norm := &types.Receipt{
		Type:              0,
		CumulativeGasUsed: 21000,
		TxHash:            tx.Hash(),
		BlockHash:         header.Hash(),
		BlockNumber:       header.Number,
		TransactionIndex:  0,
		Status:            types.ReceiptStatusSuccessful,
	}
	norm.Bloom = types.CreateBloom(types.Receipts{norm})

	sd, err := state.NewSharedDomains(txRw, log.New())
	require.NoError(t, err)
	defer sd.Close()

	base, err := txNumReader.Min(txRw, 1)
	require.NoError(t, err)

	require.NoError(t, rawdb.WriteReceiptCacheV2(sd.AsPutDel(txRw), norm, base))
	require.NoError(t, sd.Flush(ctx, txRw))

	b := types.NewBlockFromStorage(header.Hash(), header, body.Transactions, body.Uncles, nil)
	require.NotNil(t, b)

	// Expect nil (no state-sync)
	got, err := rawdb.ReadReceiptsCacheV2(txRw, b, txNumReader)
	require.NoError(t, err)
	require.Equal(t, got.Len(), 0)
}

func TestReadStateSyncReceiptByHash_EqualGasUsedStateSync(t *testing.T) {
	m := mock.Mock(t)
	m.DB.(state.HasAgg).Agg().(*state.Aggregator).EnableDomain(kv.RCacheDomain)

	txRw, err := m.DB.BeginTemporalRw(m.Ctx)
	require.NoError(t, err)
	defer txRw.Rollback()

	ctx := m.Ctx
	br := m.BlockReader
	txNumReader := br.TxnumReader(ctx)

	// Create two transactions: one "normal" and one state-sync
	tx1 := types.NewTransaction(0, common.HexToAddress("0x10"), uint256.NewInt(0), 21000, uint256.NewInt(1), nil)
	tx2 := types.NewTransaction(1, common.HexToAddress("0x11"), uint256.NewInt(0), 21000, uint256.NewInt(1), nil)
	body := &types.Body{Transactions: types.Transactions{tx1, tx2}}

	header := &types.Header{
		Number:      big.NewInt(3),
		TxHash:      types.DeriveSha(types.Transactions(body.Transactions)),
		UncleHash:   empty.UncleHash,
		ReceiptHash: empty.RootHash,
	}

	require.NoError(t, rawdb.WriteCanonicalHash(txRw, header.Hash(), 3))
	require.NoError(t, rawdb.WriteHeader(txRw, header))
	require.NoError(t, rawdb.WriteBody(txRw, header.Hash(), 3, body))

	// Receipt 1: normal tx
	r1 := &types.Receipt{
		Type:              0,
		CumulativeGasUsed: 21000,
		TxHash:            tx1.Hash(),
		BlockHash:         header.Hash(),
		BlockNumber:       header.Number,
		TransactionIndex:  0,
		Status:            types.ReceiptStatusSuccessful,
	}
	// Receipt 2: equal cumulative gas used (state-sync)
	r2 := &types.Receipt{
		Type:              0,
		CumulativeGasUsed: 21000, // same as r1
		TxHash:            tx2.Hash(),
		BlockHash:         header.Hash(),
		BlockNumber:       header.Number,
		TransactionIndex:  1,
		Status:            types.ReceiptStatusSuccessful,
	}
	r1.Bloom = types.CreateBloom(types.Receipts{r1})
	r2.Bloom = types.CreateBloom(types.Receipts{r2})

	// Insert into receipts cache domain
	sd, err := state.NewSharedDomains(txRw, log.New())
	require.NoError(t, err)
	defer sd.Close()

	base, err := txNumReader.Min(txRw, 1)
	require.NoError(t, err)

	// Write receipts
	require.NoError(t, rawdb.WriteReceiptCacheV2(sd.AsPutDel(txRw), r1, base))
	require.NoError(t, rawdb.WriteReceiptCacheV2(sd.AsPutDel(txRw), r2, base+1))
	require.NoError(t, rawdb.WriteReceiptCacheV2(sd.AsPutDel(txRw), nil, base+2))

	// Compute commitment with base+2 to include both receipts (base and base+1)
	_, err = sd.ComputeCommitment(ctx, true, header.Number.Uint64(), base+2, "flush")
	require.NoError(t, err)
	require.NoError(t, sd.Flush(ctx, txRw))

	// Build the block
	b := types.NewBlockFromStorage(header.Hash(), header, body.Transactions, body.Uncles, nil)
	require.NotNil(t, b)

	// Expect ssTx found: receipt r2
	got, err := rawdb.ReadReceiptsCacheV2(txRw, b, txNumReader)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, got.Len(), 2)

	r := got[1]

	require.Equal(t, tx2.Hash(), r.TxHash)
	require.Equal(t, r2.CumulativeGasUsed, r.CumulativeGasUsed)
	require.Equal(t, header.Hash(), r.BlockHash)
	require.Equal(t, header.Number.Uint64(), r.BlockNumber.Uint64())
}

// mockWithGenerator creates a chain with a number of explicitly defined blocks and
// wraps it into a mock backend.
func mockWithGenerator(t *testing.T, blocks int, generator func(int, *core.BlockGen)) *mock.MockSentry {
	m := mock.MockWithGenesis(t, &types.Genesis{
		Config: chain.TestChainConfig,
		Alloc:  types.GenesisAlloc{testAddr: {Balance: big.NewInt(1000000)}},
	}, testKey, false)
	if blocks > 0 {
		chain, _ := core.GenerateChain(m.ChainConfig, m.Genesis, m.Engine, m.DB, blocks, generator)
		err := m.InsertChain(chain)
		require.NoError(t, err)
	}
	return m
}
