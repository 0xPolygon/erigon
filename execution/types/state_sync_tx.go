package types

import (
	"bytes"
	"errors"
	"io"
	"math/big"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/crypto"
	"github.com/erigontech/erigon/execution/chain"
	"github.com/erigontech/erigon/execution/rlp"

	"github.com/holiman/uint256"
)

// StateSyncTx is the system transaction of Bor to introduce fetched state sync events from Heimdall
type StateSyncTx struct {
	StateSyncData []*StateSyncData
}

func (tx *StateSyncTx) Type() byte {
	return StateSyncTxType
}

func (tx *StateSyncTx) GetChainID() *uint256.Int {
	return nil
}

func (tx *StateSyncTx) GetNonce() uint64 {
	return 0
}

func (tx *StateSyncTx) GetTipCap() *uint256.Int {
	return uint256.NewInt(0)
}

func (tx *StateSyncTx) GetEffectiveGasTip(_ *uint256.Int) *uint256.Int {
	return uint256.NewInt(0)
}

func (tx *StateSyncTx) GetFeeCap() *uint256.Int {
	return uint256.NewInt(0)
}

func (tx *StateSyncTx) GetBlobHashes() []common.Hash {
	return nil
}

func (tx *StateSyncTx) GetGasLimit() uint64 {
	return 0
}

func (tx *StateSyncTx) GetBlobGas() uint64 {
	return 0
}

func (tx *StateSyncTx) GetValue() *uint256.Int {
	return uint256.NewInt(0)
}

func (tx *StateSyncTx) GetTo() *common.Address {
	return &common.Address{}
}

func (tx *StateSyncTx) AsMessage(_ Signer, baseFee *big.Int, rules *chain.Rules) (*Message, error) {
	if !rules.IsMadhugiri {
		return nil, errors.New("StateSync typed tx requires Madhugiri hard fork")
	}

	msg := Message{
		to:         &common.Address{},
		from:       common.Address{},
		nonce:      0,
		amount:     *uint256.NewInt(0),
		gasLimit:   0,
		gasPrice:   *uint256.NewInt(0),
		feeCap:     *uint256.NewInt(0),
		tipCap:     *uint256.NewInt(0),
		data:       []byte{},
		accessList: nil,
		checkNonce: false,
	}

	if baseFee != nil {
		_ = msg.gasPrice.SetFromBig(baseFee)
	}

	return &msg, nil
}

func (tx *StateSyncTx) WithSignature(_ Signer, _ []byte) (Transaction, error) {
	return nil, errors.New("StateSyncTx is not signed")
}

func (tx *StateSyncTx) Hash() common.Hash {
	// Serialize inner payload ([]StateSyncData)
	var buf bytes.Buffer
	// first write the type prefix
	buf.WriteByte(StateSyncTxType)
	// Then RLP-encode the slice
	if err := tx.encode(&buf); err != nil {
		panic("StateSyncTx encode failed: " + err.Error())
	}
	return crypto.Keccak256Hash(buf.Bytes())
}

func (tx *StateSyncTx) SigningHash(_ *big.Int) common.Hash {
	// StateSync txs are never signed, return canonical hash.
	return tx.Hash()
}

func (tx *StateSyncTx) GetData() []byte {
	return []byte{}
}

func (tx *StateSyncTx) GetAccessList() AccessList {
	return nil
}

func (tx *StateSyncTx) GetAuthorizations() []Authorization {
	return nil
}

func (tx *StateSyncTx) Protected() bool {
	return true
}

func (tx *StateSyncTx) RawSignatureValues() (*uint256.Int, *uint256.Int, *uint256.Int) {
	return uint256.NewInt(0), uint256.NewInt(0), uint256.NewInt(0)
}

func (tx *StateSyncTx) EncodingSize() int {
	var b bytes.Buffer
	_ = tx.encode(&b)
	data := make([]byte, 1+b.Len())
	return rlp.StringLen(data)
}

// EncodeRLP implements rlp.Encoder for database storage.
func (tx *StateSyncTx) EncodeRLP(w io.Writer) error {
	if tx == nil {
		return errors.New("nil StateSyncTx")
	}
	var buf bytes.Buffer
	buf.WriteByte(StateSyncTxType)
	if err := tx.encode(&buf); err != nil {
		return err
	}
	b := newEncodingBuf()
	defer pooledBuf.Put(b)
	return rlp.EncodeString(buf.Bytes(), w, b[:])
}

// DecodeRLP implements rlp.Decoder.
func (tx *StateSyncTx) DecodeRLP(s *rlp.Stream) error {
	raw, err := s.Raw()
	if err != nil {
		return err
	}
	return tx.decode(raw)
}

// MarshalBinary returns the canonical encoding for network transmission.
func (tx *StateSyncTx) MarshalBinary(w io.Writer) error {
	if tx == nil {
		return errors.New("nil StateSyncTx")
	}
	if _, err := w.Write([]byte{StateSyncTxType}); err != nil {
		return err
	}
	var payloadBuf bytes.Buffer
	if err := tx.encode(&payloadBuf); err != nil {
		return err
	}
	_, err := w.Write(payloadBuf.Bytes())
	return err
}

func (tx *StateSyncTx) Sender(_ Signer) (common.Address, error) {
	return common.Address{}, nil
}

func (tx *StateSyncTx) cachedSender() (common.Address, bool) {
	return common.Address{}, false
}

func (tx *StateSyncTx) GetSender() (common.Address, bool) {
	return common.Address{}, false
}

func (tx *StateSyncTx) SetSender(_ common.Address) {
	// no-op, StateSyncTx has no sender.
}

func (tx *StateSyncTx) IsContractDeploy() bool {
	return false
}

func (tx *StateSyncTx) Unwrap() Transaction {
	return tx
}

func (tx *StateSyncTx) copy() StateSyncTx {
	if tx == nil {
		return StateSyncTx{}
	}
	out := &StateSyncTx{}
	if tx.StateSyncData != nil {
		out.StateSyncData = make([]*StateSyncData, len(tx.StateSyncData))
		for i, d := range tx.StateSyncData {
			if d != nil {
				c := *d
				out.StateSyncData[i] = &c
			} else {
				out.StateSyncData[i] = nil
			}
		}
	}
	return *out
}

// accessors for innerTx.

func (tx *StateSyncTx) effectiveGasPrice(_ *big.Int, _ *big.Int) *big.Int {
	return big.NewInt(0)
}

func (tx *StateSyncTx) rawSignatureValues() (v, r, s *big.Int) {
	panic("no signatures on StateSyncTx")
}

func (tx *StateSyncTx) setSignatureValues(_, _, _, _ *big.Int) {
	panic("no sigs on StateSyncTx")
}

func (tx *StateSyncTx) encode(buf *bytes.Buffer) error {
	if tx == nil {
		return errors.New("nil StateSyncTx")
	}
	// Validate ascending and contiguous IDs as per PIP-74.
	var prev uint64
	for i, d := range tx.StateSyncData {
		if d == nil {
			return errors.New("nil StateSyncData")
		}
		if i == 0 {
			prev = d.ID
		} else {
			if d.ID != prev+1 {
				return errors.New("stateSyncData IDs must be ascending and contiguous")
			}
			prev = d.ID
		}
	}
	enc := make([]StateSyncData, 0, len(tx.StateSyncData))
	for _, d := range tx.StateSyncData {
		enc = append(enc, *d)
	}
	return rlp.Encode(buf, enc)
}

func (tx *StateSyncTx) decode(b []byte) error {
	var dec []StateSyncData
	if err := rlp.DecodeBytes(b, &dec); err != nil {
		return err
	}
	// Validate ascending and contiguous IDs as per PIP-74.
	if len(dec) > 0 {
		prev := dec[0].ID
		for i := 1; i < len(dec); i++ {
			if dec[i].ID != prev+1 {
				return errors.New("stateSyncData IDs must be ascending and contiguous")
			}
			prev = dec[i].ID
		}
	}
	tx.StateSyncData = make([]*StateSyncData, len(dec))
	for i := range dec {
		d := dec[i]
		tx.StateSyncData[i] = &d
	}
	return nil
}

func (tx *StateSyncTx) sigHash(_ *big.Int) common.Hash {
	panic("StateSyncTx has no sigHash")
}
