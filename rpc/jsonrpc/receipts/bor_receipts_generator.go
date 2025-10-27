package receipts

import (
	"context"
	"math/big"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon/core"
	"github.com/erigontech/erigon/core/state"
	"github.com/erigontech/erigon/core/vm"
	"github.com/erigontech/erigon/core/vm/evmtypes"
	"github.com/erigontech/erigon/db/kv"
	"github.com/erigontech/erigon/db/kv/rawdbv3"
	"github.com/erigontech/erigon/db/rawdb/rawtemporaldb"
	"github.com/erigontech/erigon/execution/chain"
	"github.com/erigontech/erigon/execution/consensus"
	"github.com/erigontech/erigon/execution/types"
	bortypes "github.com/erigontech/erigon/polygon/bor/types"
	"github.com/erigontech/erigon/turbo/services"
	"github.com/erigontech/erigon/turbo/transactions"
)

type BorGenerator struct {
	receiptCache *lru.Cache[common.Hash, *types.Receipt]
	blockReader  services.FullBlockReader
	engine       consensus.EngineReader
}

func NewBorGenerator(blockReader services.FullBlockReader,
	engine consensus.EngineReader) *BorGenerator {
	receiptCache, err := lru.New[common.Hash, *types.Receipt](receiptsCacheLimit)
	if err != nil {
		panic(err)
	}

	return &BorGenerator{
		receiptCache: receiptCache,
		blockReader:  blockReader,
		engine:       engine,
	}
}

// GenerateBorReceipt generates the receipt for state sync transactions of a block
func (g *BorGenerator) GenerateBorReceipt(ctx context.Context, tx kv.TemporalTx, block *types.Block,
	msgs []*types.Message, chainConfig *chain.Config) (*types.Receipt, error) {
	if receipt, ok := g.receiptCache.Get(block.Hash()); ok {
		return receipt, nil
	}

	// Post Madhugiri HF, state-sync txn is part of block body so calculate index accordingly.
	txIndex := len(block.Transactions())
	if chainConfig.Bor.IsMadhugiri(block.NumberU64()) {
		txIndex = len(block.Transactions()) - 1
	}

	txNumsReader := g.blockReader.TxnumReader(ctx)
	ibs, blockContext, _, _, _, err := transactions.ComputeBlockContext(ctx, g.engine, block.HeaderNoCopy(), chainConfig, g.blockReader, txNumsReader, tx, txIndex) // we want to get the state at the end of the block
	if err != nil {
		return nil, err
	}

	txNum, err := txNumsReader.Max(tx, block.NumberU64())
	if err != nil {
		return nil, err
	}

	cumGasUsedInLastBlock, _, logIdxAfterTx, err := rawtemporaldb.ReceiptAsOf(tx, txNum+1)
	if err != nil {
		return nil, err
	}

	gp := new(core.GasPool).AddGas(msgs[0].Gas() * uint64(len(msgs))).AddBlobGas(msgs[0].BlobGas() * uint64(len(msgs)))
	evm := vm.NewEVM(blockContext, evmtypes.TxContext{}, ibs, chainConfig, vm.Config{})

	// Post Madhugiri HF, calculate the hash directly from txn instead of deriving it from block number and hash.
	var txHash common.Hash
	if chainConfig.Bor.IsMadhugiri(block.NumberU64()) {
		borTx := block.Transactions()[len(block.Transactions())-1]
		txHash = borTx.Hash()
	} else {
		txHash = bortypes.ComputeBorTxHash(block.NumberU64(), block.Hash())
	}

	receipt, err := applyBorTransaction(msgs, evm, gp, ibs, block.Number(), block.Hash(), txHash, uint(txIndex), cumGasUsedInLastBlock, uint(logIdxAfterTx), rawtemporaldb.ReceiptStoresFirstLogIdx(tx))
	if err != nil {
		return nil, err
	}

	g.receiptCache.Add(block.Hash(), receipt.Copy())
	return receipt, nil
}

func (g *BorGenerator) GenerateBorLogs(ctx context.Context, msgs []*types.Message, txNumsReader rawdbv3.TxNumsReader, tx kv.TemporalTx, header *types.Header, chainConfig *chain.Config, txHash common.Hash, txIndex int, txNum uint64) (types.Logs, error) {
	ibs, blockContext, _, _, _, err := transactions.ComputeBlockContext(ctx, g.engine, header, chainConfig, g.blockReader, txNumsReader, tx, txIndex)
	if err != nil {
		return nil, err
	}

	_, _, logIdxAfterTx, err := rawtemporaldb.ReceiptAsOf(tx, txNum+1)
	if err != nil {
		return nil, err
	}

	gp := new(core.GasPool).AddGas(msgs[0].Gas() * uint64(len(msgs))).AddBlobGas(msgs[0].BlobGas() * uint64(len(msgs)))
	evm := vm.NewEVM(blockContext, evmtypes.TxContext{}, ibs, chainConfig, vm.Config{})

	return getBorLogs(msgs, evm, gp, ibs, header.Number.Uint64(), header.Hash(), txHash, uint(txIndex), uint(logIdxAfterTx), rawtemporaldb.ReceiptStoresFirstLogIdx(tx))
}

func getBorLogs(msgs []*types.Message, evm *vm.EVM, gp *core.GasPool, ibs *state.IntraBlockState, blockNum uint64, blockHash common.Hash, txHash common.Hash, txIndex, logIdxAfterTx uint, receiptWithFirstLogIdx bool) (types.Logs, error) {
	for _, msg := range msgs {
		txContext := core.NewEVMTxContext(msg)
		evm.Reset(txContext, ibs)

		_, err := core.ApplyMessage(evm, msg, gp, true /* refunds */, false /* gasBailout */, nil /* engine */)
		if err != nil {
			return nil, err
		}
	}

	receiptLogs := ibs.GetLogs(0, txHash, blockNum, blockHash)

	// set fields
	var logIndex uint
	if receiptWithFirstLogIdx {
		logIndex = logIdxAfterTx
	} else {
		// this check is a hack put in place because for cases where a block had only one tx, which was system
		// e.g. 50075104 on bor.
		// the receipt calculation stored 0 for logIdxAfterTx, which leads to underflow
		// this check allows to adjust for that error (first logIndex is 0 for such cases)
		// can be removed when receipt files fixed and all users are sure to have it (v2.2)
		if logIdxAfterTx >= uint(len(receiptLogs)) {
			logIndex = logIdxAfterTx - uint(len(receiptLogs))
		}
	}
	for i, l := range receiptLogs {
		l.TxIndex = txIndex
		l.Index = logIndex + uint(i)
	}
	return receiptLogs, nil
}

func applyBorTransaction(msgs []*types.Message, evm *vm.EVM, gp *core.GasPool, ibs *state.IntraBlockState, blockNumber *big.Int, blockHash common.Hash, txHash common.Hash, txIndex uint, cumulativeGasUsed uint64, logIdxAfterTx uint, receiptWithFirstLogIdx bool) (*types.Receipt, error) {
	receiptLogs, err := getBorLogs(msgs, evm, gp, ibs, blockNumber.Uint64(), blockHash, txHash, txIndex, logIdxAfterTx, receiptWithFirstLogIdx)
	if err != nil {
		return nil, err
	}

	receipt := types.Receipt{
		Type:              0,
		CumulativeGasUsed: cumulativeGasUsed,
		TxHash:            txHash,
		GasUsed:           0,
		BlockHash:         blockHash,
		BlockNumber:       blockNumber,
		TransactionIndex:  txIndex,
		Logs:              receiptLogs,
		Status:            types.ReceiptStatusSuccessful,
	}

	return &receipt, nil
}
