// Copyright 2024 The Erigon Authors
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

package jsonrpc

import (
	"context"
	"errors"
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/log/v3"
	"github.com/erigontech/erigon/core/state"
	"github.com/erigontech/erigon/db/kv"
	"github.com/erigontech/erigon/db/kv/order"
	"github.com/erigontech/erigon/db/kv/rawdbv3"
	"github.com/erigontech/erigon/db/kv/stream"
	"github.com/erigontech/erigon/eth/ethutils"
	"github.com/erigontech/erigon/eth/filters"
	"github.com/erigontech/erigon/execution/chain"
	"github.com/erigontech/erigon/execution/types"
	bortypes "github.com/erigontech/erigon/polygon/bor/types"
	"github.com/erigontech/erigon/rpc"
	"github.com/erigontech/erigon/rpc/rpchelper"
)

// getReceipts - checking in-mem cache, or else fallback to db, or else fallback to re-exec of block to re-gen receipts
func (api *BaseAPI) getReceipts(ctx context.Context, tx kv.TemporalTx, block *types.Block) (types.Receipts, error) {
	chainConfig, err := api.chainConfig(ctx, tx)
	if err != nil {
		return nil, err
	}

	return api.receiptsGenerator.GetReceipts(ctx, chainConfig, tx, block)
}

func (api *BaseAPI) getReceipt(ctx context.Context, cc *chain.Config, tx kv.TemporalTx, header *types.Header, txn types.Transaction, index int, txNum uint64) (*types.Receipt, error) {
	return api.receiptsGenerator.GetReceipt(ctx, cc, tx, header, txn, index, txNum)
}

func (api *BaseAPI) getReceiptsGasUsed(ctx context.Context, tx kv.TemporalTx, block *types.Block) (types.Receipts, error) {
	return api.receiptsGenerator.GetReceiptsGasUsed(tx, block, api._txNumReader)
}

func (api *BaseAPI) getCachedReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, bool) {
	return api.receiptsGenerator.GetCachedReceipt(ctx, hash)
}

func (api *BaseAPI) getCachedReceipts(ctx context.Context, hash common.Hash) (types.Receipts, bool) {
	return api.receiptsGenerator.GetCachedReceipts(ctx, hash)
}

// GetLogs implements eth_getLogs. Returns an array of logs matching a given filter object.
func (api *APIImpl) GetLogs(ctx context.Context, crit filters.FilterCriteria) (types.RPCLogs, error) {
	var begin, end uint64
	logs := types.RPCLogs{}

	tx, beginErr := api.db.BeginTemporalRo(ctx)
	if beginErr != nil {
		return logs, beginErr
	}
	defer tx.Rollback()

	if crit.BlockHash != nil {
		block, err := api.blockByHashWithSenders(ctx, tx, *crit.BlockHash)
		if err != nil {
			return nil, err
		}
		if block == nil {
			return nil, fmt.Errorf("block not found: %x", *crit.BlockHash)
		}

		num := block.NumberU64()
		begin = num
		end = num
	} else {
		// Convert the RPC block numbers into internal representations
		latest, _, _, err := rpchelper.GetBlockNumber(ctx, rpc.BlockNumberOrHashWithNumber(rpc.LatestExecutedBlockNumber), tx, api._blockReader, nil)
		if err != nil {
			return nil, err
		}

		begin = latest
		if crit.FromBlock != nil {
			fromBlock := crit.FromBlock.Int64()
			if fromBlock > 0 {
				begin = uint64(fromBlock)
			} else {
				blockNum := rpc.BlockNumber(fromBlock)
				begin, _, _, err = rpchelper.GetBlockNumber(ctx, rpc.BlockNumberOrHashWithNumber(blockNum), tx, api._blockReader, api.filters)
				if err != nil {
					return nil, err
				}
			}

			if begin > latest {
				return types.RPCLogs{}, nil
			}
		}
		end = latest
		if crit.ToBlock != nil {
			toBlock := crit.ToBlock.Int64()
			if toBlock > 0 {
				end = uint64(toBlock)
			} else {
				blockNum := rpc.BlockNumber(toBlock)
				end, _, _, err = rpchelper.GetBlockNumber(ctx, rpc.BlockNumberOrHashWithNumber(blockNum), tx, api._blockReader, api.filters)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if end < begin {
		return nil, fmt.Errorf("end (%d) < begin (%d)", end, begin)
	}
	if end > roaring.MaxUint32 {
		latest, err := rpchelper.GetLatestBlockNumber(tx)
		if err != nil {
			return nil, err
		}
		if begin > latest {
			return nil, fmt.Errorf("begin (%d) > latest (%d)", begin, latest)
		}
		end = latest
	}

	erigonLogs, err := api.getLogsV3(ctx, tx, begin, end, crit)
	if err != nil {
		return nil, err
	}

	rpcLogs := make(types.RPCLogs, len(erigonLogs))
	for i, log := range erigonLogs {
		rpcLogs[i] = &types.RPCLog{
			Log: types.Log{
				Address:     log.Address,
				Topics:      log.Topics,
				Data:        log.Data,
				BlockNumber: log.BlockNumber,
				TxHash:      log.TxHash,
				TxIndex:     log.TxIndex,
				BlockHash:   log.BlockHash,
				Index:       log.Index,
				Removed:     log.Removed,
			},
			BlockTimestamp: log.Timestamp,
		}
	}

	// For a whole block query (blockHash set, no address/topics filters),
	// normalize logIndex (if required) to be continuous 0..N-1 in canonical order.
	/* TODO deactivated for memory spike, although already optimizated with `needsRenumber` addition
	if isWholeBlockUnfiltered(crit) && len(rpcLogs) > 0 && needsRenumber(rpcLogs) {
		sort.Slice(rpcLogs, func(i, j int) bool {
			if rpcLogs[i].BlockNumber == rpcLogs[j].BlockNumber {
				if rpcLogs[i].TxIndex == rpcLogs[j].TxIndex {
					return rpcLogs[i].Index < rpcLogs[j].Index
				}
				return rpcLogs[i].TxIndex < rpcLogs[j].TxIndex
			}
			return rpcLogs[i].BlockNumber < rpcLogs[j].BlockNumber
		})

		currentBlock := rpcLogs[0].BlockHash
		var idx uint
		idx = 0
		for _, l := range rpcLogs {
			if l.BlockHash != currentBlock {
				currentBlock = l.BlockHash
				idx = 0
			}
			l.Index = idx
			idx++
		}
	}
	*/

	return rpcLogs, nil
}

func applyFiltersV3(txNumsReader rawdbv3.TxNumsReader, tx kv.TemporalTx, begin, end uint64, crit filters.FilterCriteria, asc order.By) (out stream.U64, err error) {
	//[from,to)
	var fromTxNum, toTxNum uint64
	if begin > 0 {
		fromTxNum, err = txNumsReader.Min(tx, begin)
		if err != nil {
			return out, err
		}
		r := state.NewHistoryReaderV3()
		r.SetTx(tx)
		if fromTxNum < r.StateHistoryStartFrom() {
			return out, state.PrunedError
		}
	}

	toTxNum, err = txNumsReader.Max(tx, end)
	if err != nil {
		return out, err
	}

	if asc {
		toTxNum++
	} else {
		toTxNum--
	}

	var topicsBitmap stream.U64

	if asc {
		topicsBitmap, err = getTopicsBitmapV3(tx, crit.Topics, fromTxNum, toTxNum, asc)
	} else {
		topicsBitmap, err = getTopicsBitmapV3(tx, crit.Topics, toTxNum, fromTxNum, asc)
	}
	if err != nil {
		return out, err
	}
	if topicsBitmap != nil {
		out = topicsBitmap
	}

	var addrBitmap stream.U64

	if asc {
		addrBitmap, err = getAddrsBitmapV3(tx, crit.Addresses, fromTxNum, toTxNum, asc)
	} else {
		addrBitmap, err = getAddrsBitmapV3(tx, crit.Addresses, toTxNum, fromTxNum, asc)
	}
	if err != nil {
		return out, err
	}
	if addrBitmap != nil {
		if out == nil {
			out = addrBitmap
		} else {
			out = stream.Intersect[uint64](out, addrBitmap, asc, kv.Unlim)
		}
	}
	if out == nil {
		if asc == order.Asc {
			out = stream.Range[uint64](fromTxNum, toTxNum)
		} else {
			out = stream.ReverseRange[uint64](toTxNum, fromTxNum)
		}
	}
	return out, nil
}

func (api *BaseAPI) getLogsV3(ctx context.Context, tx kv.TemporalTx, begin, end uint64, crit filters.FilterCriteria) ([]*types.ErigonLog, error) {
	logs := []*types.ErigonLog{} //nolint

	addrMap := make(map[common.Address]struct{}, len(crit.Addresses))
	for _, v := range crit.Addresses {
		addrMap[v] = struct{}{}
	}

	chainConfig, err := api.chainConfig(ctx, tx)
	if err != nil {
		return nil, err
	}

	var header *types.Header

	txNumbers, err := applyFiltersV3(api._txNumReader, tx, begin, end, crit, order.Asc)
	if err != nil {
		return logs, err
	}

	it := rawdbv3.TxNums2BlockNums(tx, api._txNumReader, txNumbers, order.Asc)
	defer it.Close()

	for it.HasNext() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		txNum, blockNum, txIndex, isFinalTxn, blockNumChanged, err := it.Next()
		if err != nil {
			return nil, err
		}

		// if block number changed, calculate all related field
		if blockNumChanged {
			if header, err = api._blockReader.HeaderByNumber(ctx, tx, blockNum); err != nil {
				return nil, err
			}
			if header == nil {
				log.Warn("[rpc] header is nil", "blockNum", blockNum)
				continue
			}
		}

		if isFinalTxn {
			if chainConfig.Bor != nil {
				if header == nil {
					header, err = api._blockReader.HeaderByNumber(ctx, tx, blockNum)
					if err != nil {
						return nil, err
					}
				}

				block, err := api._blockReader.BlockByHash(ctx, tx, header.Hash())
				if err != nil {
					return nil, err
				}

				// check for state sync event logs
				events, err := api.bridgeReader.Events(ctx, header.Hash(), blockNum)
				if err != nil {
					return logs, err
				}

				if len(events) == 0 {
					continue
				}

				// Post HF, calculate the hash directly from txn instead of deriving it from block number and hash.
				var txHash common.Hash
				if chainConfig.Bor.IsMadhugiri(block.NumberU64()) {
					borTx := block.Transactions()[len(block.Transactions())-1]
					txHash = borTx.Hash()
				} else {
					txHash = bortypes.ComputeBorTxHash(block.NumberU64(), block.Hash())
				}

				borLogs, err := api.borReceiptGenerator.GenerateBorLogs(ctx, events, api._txNumReader, tx, header, chainConfig, txHash, txIndex, txNum)
				if err != nil {
					return logs, err
				}

				borLogs = borLogs.Filter(addrMap, crit.Topics, 0)
				for _, filteredLog := range borLogs {
					logs = append(logs, &types.ErigonLog{
						Address:     filteredLog.Address,
						Topics:      filteredLog.Topics,
						Data:        filteredLog.Data,
						BlockNumber: filteredLog.BlockNumber,
						TxHash:      filteredLog.TxHash,
						TxIndex:     filteredLog.TxIndex,
						BlockHash:   filteredLog.BlockHash,
						Index:       filteredLog.Index,
						Removed:     filteredLog.Removed,
						Timestamp:   header.Time,
					})
				}
			}

			continue
		}

		//fmt.Printf("txNum=%d, blockNum=%d, txIndex=%d, maxTxNumInBlock=%d,mixTxNumInBlock=%d\n", txNum, blockNum, txIndex, maxTxNumInBlock, minTxNumInBlock)
		txn, err := api._txnReader.TxnByIdxInBlock(ctx, tx, blockNum, txIndex)
		if err != nil {
			return nil, err
		}
		if txn == nil {
			continue
		}

		r, err := api.receiptsGenerator.GetReceipt(ctx, chainConfig, tx, header, txn, txIndex, txNum)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, err
		}
		filtered := r.Logs.Filter(addrMap, crit.Topics, 0)

		for _, filteredLog := range filtered {
			logs = append(logs, &types.ErigonLog{
				Address:     filteredLog.Address,
				Topics:      filteredLog.Topics,
				Data:        filteredLog.Data,
				BlockNumber: filteredLog.BlockNumber,
				TxHash:      filteredLog.TxHash,
				TxIndex:     filteredLog.TxIndex,
				BlockHash:   filteredLog.BlockHash,
				Index:       filteredLog.Index,
				Removed:     filteredLog.Removed,
				Timestamp:   header.Time,
			})
		}
	}

	return logs, nil
}

// The Topic list restricts matches to particular event topics. Each event has a list
// of topics. Topics matches a prefix of that list. An empty element slice matches any
// topic. Non-empty elements represent an alternative that matches any of the
// contained topics.
//
// Examples:
// {} or nil          matches any topic list
// {{A}}              matches topic A in first position
// {{}, {B}}          matches any topic in first position AND B in second position
// {{A}, {B}}         matches topic A in first position AND B in second position
// {{A, B}, {C, D}}   matches topic (A OR B) in first position AND (C OR D) in second position
func getTopicsBitmapV3(tx kv.TemporalTx, topics [][]common.Hash, from, to uint64, asc order.By) (res stream.U64, err error) {

	for _, sub := range topics {
		if len(sub) == 0 {
			continue
		}

		var topicsUnion stream.U64
		for _, topic := range sub {
			it, err := tx.IndexRange(kv.LogTopicIdx, topic.Bytes(), int(from), int(to), asc, kv.Unlim)
			if err != nil {
				return nil, err
			}
			topicsUnion = stream.Union[uint64](topicsUnion, it, asc, kv.Unlim)
		}

		if res == nil {
			res = topicsUnion
			continue
		}
		res = stream.Intersect[uint64](res, topicsUnion, asc, kv.Unlim)
	}
	return res, nil
}

func getAddrsBitmapV3(tx kv.TemporalTx, addrs []common.Address, from, to uint64, asc order.By) (res stream.U64, err error) {
	for _, addr := range addrs {
		it, err := tx.IndexRange(kv.LogAddrIdx, addr[:], int(from), int(to), asc, kv.Unlim)
		if err != nil {
			return nil, err
		}
		res = stream.Union[uint64](res, it, asc, kv.Unlim)
	}
	return res, nil
}

// GetTransactionReceipt implements eth_getTransactionReceipt. Returns the receipt of a transaction given the transaction's hash.
func (api *APIImpl) GetTransactionReceipt(ctx context.Context, txnHash common.Hash) (map[string]interface{}, error) {
	tx, err := api.db.BeginTemporalRo(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var blockNum, txNum uint64
	var ok bool

	chainConfig, err := api.chainConfig(ctx, tx)
	if err != nil {
		return nil, err
	}
	blockNum, txNum, ok, err = api.txnLookup(ctx, tx, txnHash)
	if err != nil {
		return nil, err
	}
	if !ok && chainConfig.Bor == nil {
		return nil, nil
	}

	txNumMin, err := api._txNumReader.Min(tx, blockNum)
	if err != nil {
		return nil, err
	}

	// Private API returns 0 if transaction is not found.
	isBorStateSyncTx := blockNum == 0 && chainConfig.Bor != nil

	if isBorStateSyncTx {
		blockNum, ok, err = api.bridgeReader.EventTxnLookup(ctx, txnHash)
		if err != nil {
			return nil, err
		}
	}

	if !ok {
		return nil, nil
	}

	if txNumMin+1 > txNum && !isBorStateSyncTx {
		return nil, fmt.Errorf("uint underflow txnums error txNum: %d, txNumMin: %d, blockNum: %d", txNum, txNumMin, blockNum)
	}

	header, err := api._blockReader.HeaderByNumber(ctx, tx, blockNum)
	if err != nil {
		return nil, err
	}

	generateBorReceipt := func(txn types.Transaction) (map[string]interface{}, error) {
		block, err := api.blockByNumberWithSenders(ctx, tx, blockNum)
		if err != nil {
			return nil, err
		}
		if block == nil {
			return nil, nil // not error, see https://github.com/erigontech/erigon/issues/1645
		}

		events, err := api.bridgeReader.Events(ctx, block.Hash(), blockNum)
		if err != nil {
			return nil, err
		}

		if len(events) == 0 {
			return nil, errors.New("tx not found")
		}

		borReceipt, err := api.borReceiptGenerator.GenerateBorReceipt(ctx, tx, block, events, chainConfig)
		if err != nil {
			return nil, err
		}

		return ethutils.MarshalReceipt(borReceipt, txn, chainConfig, block.HeaderNoCopy(), txnHash, false, true), nil
	}

	// This case covers state-sync txs before HF as they're not available in the txn lookup
	if isBorStateSyncTx {
		return generateBorReceipt(bortypes.NewBorTransaction())
	}

	var txnIndex = int(txNum - txNumMin - 1)

	txn, err := api._blockReader.TxnByIdxInBlock(ctx, tx, header.Number.Uint64(), txnIndex)
	if err != nil {
		return nil, err
	}

	// This case covers state-sync txs post HF as they're available in the txn lookup so we can
	// use the actual state-sync tx type to generate receipt.
	if txn.Type() == types.StateSyncTxType {
		return generateBorReceipt(txn)
	}

	receipt, err := api.getReceipt(ctx, chainConfig, tx, header, txn, txnIndex, txNum)
	if err != nil {
		return nil, fmt.Errorf("getReceipt error: %w", err)
	}

	return ethutils.MarshalReceipt(receipt, txn, chainConfig, header, txnHash, true, true), nil
}

// GetBlockReceipts - receipts for individual block
func (api *APIImpl) GetBlockReceipts(ctx context.Context, numberOrHash rpc.BlockNumberOrHash) ([]map[string]interface{}, error) {
	tx, err := api.db.BeginTemporalRo(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	blockNum, blockHash, _, err := rpchelper.GetBlockNumber(ctx, numberOrHash, tx, api._blockReader, api.filters)
	if err != nil {
		bnh, _ := numberOrHash.Hash()
		if errors.Is(err, rpchelper.BlockNotFoundErr{Hash: bnh}) {
			return nil, nil
		}
		return nil, err
	}
	block, err := api.blockWithSenders(ctx, tx, blockHash, blockNum)
	if err != nil {
		return nil, err
	}
	if block == nil {
		return nil, nil
	}
	chainConfig, err := api.chainConfig(ctx, tx)
	if err != nil {
		return nil, err
	}

	// Get normal transaction receipts.
	// This DOES NOT generate state-sync transaction receipt for bor.
	receipts, err := api.getReceipts(ctx, tx, block)
	if err != nil {
		return nil, fmt.Errorf("getReceipts error: %w", err)
	}

	numReceipts := len(receipts)
	result := make([]map[string]interface{}, 0, numReceipts)

	// Marshal normal transaction receipts and append to result
	for _, receipt := range receipts {
		txn := block.Transactions()[receipt.TransactionIndex]
		result = append(result, ethutils.MarshalReceipt(receipt, txn, chainConfig, block.HeaderNoCopy(), txn.Hash(), true, true))
	}

	// check for state sync event logs and generate state-sync transaction receipt for bor.
	events, err := api.bridgeReader.Events(ctx, block.Hash(), blockNum)
	if err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return result, nil
	}

	var borTx types.Transaction = bortypes.NewBorTransaction()
	if chainConfig.Bor != nil && chainConfig.Bor.IsMadhugiri(blockNum) && len(receipts)+1 == len(block.Transactions()) {
		borTx = block.Transactions()[len(block.Transactions())-1]
		if borTx.Type() != types.StateSyncTxType {
			return result, nil
		}
	}

	borReceipt, err := api.borReceiptGenerator.GenerateBorReceipt(ctx, tx, block, events, chainConfig)
	if err != nil {
		return nil, err
	}

	result = append(result, ethutils.MarshalReceipt(borReceipt, borTx, chainConfig, block.HeaderNoCopy(), borReceipt.TxHash, false, true))

	return result, nil
}

// isWholeBlockUnfiltered returns true if the filter is to get all logs for a specific block hash.
func isWholeBlockUnfiltered(crit filters.FilterCriteria) bool {
	if crit.BlockHash == nil {
		return false
	}
	if len(crit.Addresses) > 0 {
		return false
	}
	if len(crit.Topics) > 0 {
		return false
	}
	return true
}

// needsRenumber checks if the log indexes are continuous 0..N-1 per block.
func needsRenumber(rpcLogs types.RPCLogs) bool {
	if len(rpcLogs) == 0 {
		return false
	}
	curBlock := rpcLogs[0].BlockHash
	var expected uint
	expected = 0

	for _, l := range rpcLogs {
		if l.BlockHash != curBlock {
			curBlock = l.BlockHash
			expected = 0
		}
		if l.Index != expected {
			return true
		}
		expected++
	}
	return false
}
