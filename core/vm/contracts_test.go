// Copyright 2017 The go-ethereum Authors
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

package vm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/holiman/uint256"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/erigontech/erigon-lib/common"
	"github.com/erigontech/erigon-lib/common/hexutil"
	"github.com/erigontech/erigon-lib/common/math"
	"github.com/erigontech/erigon/core/state"
	"github.com/erigontech/erigon/core/vm/evmtypes"
	"github.com/erigontech/erigon/execution/chain"
	"github.com/erigontech/erigon/execution/chain/params"
	"github.com/erigontech/erigon/polygon/bor/borcfg"
)

// precompiledTest defines the input/output pairs for precompiled contract tests.
type precompiledTest struct {
	Input, Expected string
	Gas             uint64
	Name            string
	NoBenchmark     bool // Benchmark primarily the worst-cases
}

// precompiledFailureTest defines the input/error pairs for precompiled
// contract failure tests.
type precompiledFailureTest struct {
	Input         string
	ExpectedError string
	Name          string
}

// allPrecompiles does not map to the actual set of precompiles, as it also contains
// repriced versions of precompiles at certain slots
var allPrecompiles = map[common.Address]PrecompiledContract{
	common.BytesToAddress([]byte{0x01}):       &ecrecover{},
	common.BytesToAddress([]byte{0x02}):       &sha256hash{},
	common.BytesToAddress([]byte{0x03}):       &ripemd160hash{},
	common.BytesToAddress([]byte{0x04}):       &dataCopy{},
	common.BytesToAddress([]byte{0x05}):       &bigModExp{eip2565: false},
	common.BytesToAddress([]byte{0xa5}):       &bigModExp{eip2565: true},
	common.BytesToAddress([]byte{0xb5}):       &bigModExp{osaka: true},
	common.BytesToAddress([]byte{0xc5}):       &bigModExp{madhugiri: true},
	common.BytesToAddress([]byte{0x06}):       &bn254AddIstanbul{},
	common.BytesToAddress([]byte{0x07}):       &bn254ScalarMulIstanbul{},
	common.BytesToAddress([]byte{0x08}):       &bn254PairingIstanbul{},
	common.BytesToAddress([]byte{0x09}):       &blake2F{},
	common.BytesToAddress([]byte{0x0a}):       &pointEvaluation{},
	common.BytesToAddress([]byte{0x0b}):       &bls12381G1Add{},
	common.BytesToAddress([]byte{0x0c}):       &bls12381G1MultiExp{},
	common.BytesToAddress([]byte{0x0d}):       &bls12381G2Add{},
	common.BytesToAddress([]byte{0x0e}):       &bls12381G2MultiExp{},
	common.BytesToAddress([]byte{0x0f}):       &bls12381Pairing{},
	common.BytesToAddress([]byte{0x10}):       &bls12381MapFpToG1{},
	common.BytesToAddress([]byte{0x11}):       &bls12381MapFp2ToG2{},
	common.BytesToAddress([]byte{0x01, 0x00}): &p256Verify{},
	common.BytesToAddress([]byte{0xa1, 0x00}): &p256Verify{eip7951: true},
}

// EIP-152 test vectors
var blake2FMalformedInputTests = []precompiledFailureTest{
	{
		Input:         "",
		ExpectedError: errBlake2FInvalidInputLength.Error(),
		Name:          "vector 0: empty input",
	},
	{
		Input:         "00000c48c9bdf267e6096a3ba7ca8485ae67bb2bf894fe72f36e3cf1361d5f3af54fa5d182e6ad7f520e511f6c3e2b8c68059b6bbd41fbabd9831f79217e1319cde05b61626300000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000001",
		ExpectedError: errBlake2FInvalidInputLength.Error(),
		Name:          "vector 1: less than 213 bytes input",
	},
	{
		Input:         "000000000c48c9bdf267e6096a3ba7ca8485ae67bb2bf894fe72f36e3cf1361d5f3af54fa5d182e6ad7f520e511f6c3e2b8c68059b6bbd41fbabd9831f79217e1319cde05b61626300000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000001",
		ExpectedError: errBlake2FInvalidInputLength.Error(),
		Name:          "vector 2: more than 213 bytes input",
	},
	{
		Input:         "0000000c48c9bdf267e6096a3ba7ca8485ae67bb2bf894fe72f36e3cf1361d5f3af54fa5d182e6ad7f520e511f6c3e2b8c68059b6bbd41fbabd9831f79217e1319cde05b61626300000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000002",
		ExpectedError: errBlake2FInvalidFinalFlag.Error(),
		Name:          "vector 3: malformed final block indicator flag",
	},
}

func testPrecompiled(t *testing.T, addr string, test precompiledTest) {
	p := allPrecompiles[common.HexToAddress(addr)]
	in := common.Hex2Bytes(test.Input)
	gas := p.RequiredGas(in)
	t.Run(fmt.Sprintf("%s-Gas=%d", test.Name, gas), func(t *testing.T) {
		t.Parallel()
		if res, _, err := RunPrecompiledContract(p, in, gas, nil); err != nil {
			t.Error(err)
		} else if common.Bytes2Hex(res) != test.Expected {
			t.Errorf("Expected %v, got %v", test.Expected, common.Bytes2Hex(res))
		}
		if expGas := test.Gas; expGas != gas {
			t.Errorf("%v: gas wrong, expected %d, got %d", test.Name, expGas, gas)
		}
		// Verify that the precompile did not touch the input buffer
		exp := common.Hex2Bytes(test.Input)
		if !bytes.Equal(in, exp) {
			t.Errorf("Precompiled %v modified input data", addr)
		}
	})
}

func testPrecompiledOOG(t *testing.T, addr string, test precompiledTest) {
	p := allPrecompiles[common.HexToAddress(addr)]
	in := common.Hex2Bytes(test.Input)
	gas := p.RequiredGas(in) - 1

	t.Run(fmt.Sprintf("%s-Gas=%d", test.Name, gas), func(t *testing.T) {
		t.Parallel()
		_, _, err := RunPrecompiledContract(p, in, gas, nil)
		if err.Error() != "out of gas" {
			t.Errorf("Expected error [out of gas], got [%v]", err)
		}
		// Verify that the precompile did not touch the input buffer
		exp := common.Hex2Bytes(test.Input)
		if !bytes.Equal(in, exp) {
			t.Errorf("Precompiled %v modified input data", addr)
		}
	})
}

func testPrecompiledFailure(addr string, test precompiledFailureTest, t *testing.T) {
	p := allPrecompiles[common.HexToAddress(addr)]
	in := common.Hex2Bytes(test.Input)
	gas := p.RequiredGas(in)
	t.Run(test.Name, func(t *testing.T) {
		t.Parallel()
		_, _, err := RunPrecompiledContract(p, in, gas, nil)
		if err == nil || err.Error() != test.ExpectedError {
			t.Errorf("Expected error [%v], got [%v]", test.ExpectedError, err)
		}
		// Verify that the precompile did not touch the input buffer
		exp := common.Hex2Bytes(test.Input)
		if !bytes.Equal(in, exp) {
			t.Errorf("Precompiled %v modified input data", addr)
		}
	})
}

func benchmarkPrecompiled(b *testing.B, addr string, test precompiledTest) {
	if test.NoBenchmark {
		return
	}
	p := allPrecompiles[common.HexToAddress(addr)]
	in := common.Hex2Bytes(test.Input)
	reqGas := p.RequiredGas(in)

	var (
		res  []byte
		err  error
		data = make([]byte, len(in))
	)

	b.Run(fmt.Sprintf("%s-Gas=%d", test.Name, reqGas), func(bench *testing.B) {
		bench.ReportAllocs()
		start := time.Now()
		bench.ResetTimer()
		for i := 0; i < bench.N; i++ {
			copy(data, in)
			res, _, err = RunPrecompiledContract(p, data, reqGas, nil)
		}
		bench.StopTimer()
		elapsed := uint64(time.Since(start))
		if elapsed < 1 {
			elapsed = 1
		}
		gasUsed := reqGas * uint64(bench.N)
		bench.ReportMetric(float64(reqGas), "gas/op")
		// Keep it as uint64, multiply 100 to get two digit float later
		mgasps := (100 * 1000 * gasUsed) / elapsed
		bench.ReportMetric(float64(mgasps)/100, "mgas/s")
		//Check if it is correct
		if err != nil {
			bench.Error(err)
			return
		}
		if common.Bytes2Hex(res) != test.Expected {
			bench.Errorf("Expected %v, got %v", test.Expected, common.Bytes2Hex(res))
			return
		}
	})
}

// Benchmarks the sample inputs from the SHA256 precompile.
func BenchmarkPrecompiledSha256(bench *testing.B) {
	t := precompiledTest{
		Input:    "38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02",
		Expected: "811c7003375852fabd0d362e40e68607a12bdabae61a7d068fe5fdd1dbbf2a5d",
		Name:     "128",
	}
	benchmarkPrecompiled(bench, "02", t)
}

// Benchmarks the sample inputs from the RIPEMD precompile.
func BenchmarkPrecompiledRipeMD(b *testing.B) {
	t := precompiledTest{
		Input:    "38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02",
		Expected: "0000000000000000000000009215b8d9882ff46f0dfde6684d78e831467f65e6",
		Name:     "128",
	}
	benchmarkPrecompiled(b, "03", t)
}

// Benchmarks the sample inputs from the identiy precompile.
func BenchmarkPrecompiledIdentity(b *testing.B) {
	t := precompiledTest{
		Input:    "38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02",
		Expected: "38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02",
		Name:     "128",
	}
	benchmarkPrecompiled(b, "04", t)
}

// Tests the sample inputs from the ModExp EIP 198.
func TestPrecompiledModExp(t *testing.T)      { testJson("modexp", "05", t) }
func BenchmarkPrecompiledModExp(b *testing.B) { benchJson("modexp", "05", b) }

func TestPrecompiledModExpEip2565(t *testing.T)      { testJson("modexp_eip2565", "a5", t) }
func BenchmarkPrecompiledModExpEip2565(b *testing.B) { benchJson("modexp_eip2565", "a5", b) }

func TestPrecompiledModExpEip7883(t *testing.T)      { testJson("modexp_eip7883", "b5", t) }
func BenchmarkPrecompiledModExpEip7883(b *testing.B) { benchJson("modexp_eip7883", "b5", b) }

func TestPrecompiledModExpMadhugiri(t *testing.T)      { testJson("modexp_eip7883", "c5", t) }
func BenchmarkPrecompiledModExpMadhugiri(b *testing.B) { benchJson("modexp_eip7883", "c5", b) }

func TestPrecompiledModExpEip7823Fail(t *testing.T) { testJsonFail("modexp-eip7823", "b5", t) }

func TestPrecompiledModExpMadhugiriFail(t *testing.T) { testJsonFail("modexp-eip7823", "c5", t) }

// Tests the sample inputs from the elliptic curve addition EIP 213.
func TestPrecompiledBn254Add(t *testing.T)      { testJson("bn254Add", "06", t) }
func BenchmarkPrecompiledBn254Add(b *testing.B) { benchJson("bn254Add", "06", b) }

// Tests OOG
func TestPrecompiledModExpOOG(t *testing.T) {
	t.Parallel()
	modexpTests, err := loadJson("modexp")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range modexpTests {
		testPrecompiledOOG(t, "05", test)
	}
}

func TestPrecompiledModExpPotentialOutOfRange(t *testing.T) {
	modExpContract := allPrecompiles[common.BytesToAddress([]byte{0xa5})]
	hexString := "0x0000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000ffffffffffffffff0000000000000000000000000000000000000000000000000000000000000000ee"
	input := hexutil.MustDecode(hexString)
	maxGas := uint64(math.MaxUint64)
	_, _, err := RunPrecompiledContract(modExpContract, input, maxGas, nil)
	require.NoError(t, err)
}

func TestPrecompiledModExpInputEip7823(t *testing.T) {
	pragueModExp := allPrecompiles[common.BytesToAddress([]byte{0xa5})]
	osakaModExp := allPrecompiles[common.BytesToAddress([]byte{0xb5})]

	// length_of_EXPONENT = 1024; everything else is zero
	in := common.Hex2Bytes("000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000004000000000000000000000000000000000000000000000000000000000000000000")
	gas := pragueModExp.RequiredGas(in)
	res, _, err := RunPrecompiledContract(pragueModExp, in, gas, nil)
	require.NoError(t, err)
	assert.Equal(t, "", common.Bytes2Hex(res))
	gas = osakaModExp.RequiredGas(in)
	_, _, err = RunPrecompiledContract(osakaModExp, in, gas, nil)
	require.NoError(t, err)
	assert.Equal(t, "", common.Bytes2Hex(res))

	// length_of_EXPONENT = 1025; everything else is zero
	in = common.Hex2Bytes("000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000004010000000000000000000000000000000000000000000000000000000000000000")
	gas = pragueModExp.RequiredGas(in)
	res, _, err = RunPrecompiledContract(pragueModExp, in, gas, nil)
	require.NoError(t, err)
	assert.Equal(t, "", common.Bytes2Hex(res))
	gas = osakaModExp.RequiredGas(in)
	_, _, err = RunPrecompiledContract(osakaModExp, in, gas, nil)
	assert.ErrorIs(t, err, errModExpExponentLengthTooLarge)

	// length_of_EXPONENT = 2048; everything else is zero
	in = common.Hex2Bytes("000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000000000000000000000000000000000000")
	gas = pragueModExp.RequiredGas(in)
	res, _, err = RunPrecompiledContract(pragueModExp, in, gas, nil)
	require.NoError(t, err)
	assert.Equal(t, "", common.Bytes2Hex(res))
	gas = osakaModExp.RequiredGas(in)
	_, _, err = RunPrecompiledContract(osakaModExp, in, gas, nil)
	assert.ErrorIs(t, err, errModExpExponentLengthTooLarge)

	// length_of_EXPONENT = 2^32; everything else is zero
	in = common.Hex2Bytes("000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000000000000000")
	gas = pragueModExp.RequiredGas(in)
	res, _, err = RunPrecompiledContract(pragueModExp, in, gas, nil)
	require.NoError(t, err)
	assert.Equal(t, "", common.Bytes2Hex(res))
	gas = osakaModExp.RequiredGas(in)
	_, _, err = RunPrecompiledContract(osakaModExp, in, gas, nil)
	assert.ErrorIs(t, err, errModExpExponentLengthTooLarge)

	// length_of_EXPONENT = 2^64; everything else is zero
	in = common.Hex2Bytes("000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000000000000000000000")
	gas = pragueModExp.RequiredGas(in)
	res, _, err = RunPrecompiledContract(pragueModExp, in, gas, nil)
	require.NoError(t, err)
	assert.Equal(t, "", common.Bytes2Hex(res))
	gas = osakaModExp.RequiredGas(in)
	_, _, err = RunPrecompiledContract(osakaModExp, in, gas, nil)
	assert.ErrorIs(t, err, errModExpExponentLengthTooLarge)
}

// Tests the sample inputs from the elliptic curve scalar multiplication EIP 213.
func TestPrecompiledBn254ScalarMul(t *testing.T)      { testJson("bn254ScalarMul", "07", t) }
func BenchmarkPrecompiledBn254ScalarMul(b *testing.B) { benchJson("bn254ScalarMul", "07", b) }
func TestPrecompiledBn254ScalarMulFail(t *testing.T)  { testJsonFail("bn254ScalarMul", "07", t) }

// Tests the sample inputs from the elliptic curve pairing check EIP 197.
func TestPrecompiledBn254Pairing(t *testing.T)      { testJson("bn254Pairing", "08", t) }
func BenchmarkPrecompiledBn254Pairing(b *testing.B) { benchJson("bn254Pairing", "08", b) }

func TestPrecompiledBlake2F(t *testing.T)      { testJson("blake2F", "09", t) }
func BenchmarkPrecompiledBlake2F(b *testing.B) { benchJson("blake2F", "09", b) }

func TestPrecompileBlake2FMalformedInput(t *testing.T) {
	t.Parallel()
	for _, test := range blake2FMalformedInputTests {
		testPrecompiledFailure("09", test, t)
	}
}

func TestPrecompiledEcrecover(t *testing.T)      { testJson("ecRecover", "01", t) }
func BenchmarkPrecompiledEcrecover(b *testing.B) { benchJson("ecRecover", "01", b) }

func testJson(name, addr string, t *testing.T) {
	tests, err := loadJson(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		testPrecompiled(t, addr, test)
	}
}

func testJsonFail(name, addr string, t *testing.T) {
	tests, err := loadJsonFail(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		testPrecompiledFailure(addr, test, t)
	}
}

func benchJson(name, addr string, b *testing.B) {
	tests, err := loadJson(name)
	if err != nil {
		b.Fatal(err)
	}
	for _, test := range tests {
		benchmarkPrecompiled(b, addr, test)
	}
}

func TestPrecompiledBLS12381G1Add(t *testing.T)      { testJson("blsG1Add", "0b", t) }
func TestPrecompiledBLS12381G1MultiExp(t *testing.T) { testJson("blsG1MultiExp", "0c", t) }
func TestPrecompiledBLS12381G2Add(t *testing.T)      { testJson("blsG2Add", "0d", t) }
func TestPrecompiledBLS12381G2MultiExp(t *testing.T) { testJson("blsG2MultiExp", "0e", t) }
func TestPrecompiledBLS12381Pairing(t *testing.T)    { testJson("blsPairing", "0f", t) }
func TestPrecompiledBLS12381MapG1(t *testing.T)      { testJson("blsMapG1", "10", t) }
func TestPrecompiledBLS12381MapG2(t *testing.T)      { testJson("blsMapG2", "11", t) }
func TestPrecompiledPointEvaluation(t *testing.T)    { testJson("pointEvaluation", "0a", t) }

func BenchmarkPrecompiledBLS12381G1Add(b *testing.B)      { benchJson("blsG1Add", "0b", b) }
func BenchmarkPrecompiledBLS12381G1MultiExp(b *testing.B) { benchJson("blsG1MultiExp", "0c", b) }
func BenchmarkPrecompiledBLS12381G2Add(b *testing.B)      { benchJson("blsG2Add", "0d", b) }
func BenchmarkPrecompiledBLS12381G2MultiExp(b *testing.B) { benchJson("blsG2MultiExp", "0e", b) }
func BenchmarkPrecompiledBLS12381Pairing(b *testing.B)    { benchJson("blsPairing", "0f", b) }
func BenchmarkPrecompiledBLS12381MapG1(b *testing.B)      { benchJson("blsMapG1", "10", b) }
func BenchmarkPrecompiledBLS12381MapG2(b *testing.B)      { benchJson("blsMapG2", "11", b) }
func BenchmarkPrecompiledPointEvaluation(b *testing.B)    { benchJson("pointEvaluation", "0a", b) }

// Failure tests
func TestPrecompiledBLS12381G1AddFail(t *testing.T)      { testJsonFail("blsG1Add", "0b", t) }
func TestPrecompiledBLS12381G1MultiExpFail(t *testing.T) { testJsonFail("blsG1MultiExp", "0c", t) }
func TestPrecompiledBLS12381G2AddFail(t *testing.T)      { testJsonFail("blsG2Add", "0d", t) }
func TestPrecompiledBLS12381G2MultiExpFail(t *testing.T) { testJsonFail("blsG2MultiExp", "0e", t) }
func TestPrecompiledBLS12381PairingFail(t *testing.T)    { testJsonFail("blsPairing", "0f", t) }
func TestPrecompiledBLS12381MapG1Fail(t *testing.T)      { testJsonFail("blsMapG1", "10", t) }
func TestPrecompiledBLS12381MapG2Fail(t *testing.T)      { testJsonFail("blsMapG2", "11", t) }

func loadJson(name string) ([]precompiledTest, error) {
	data, err := os.ReadFile(fmt.Sprintf("testdata/precompiles/%v.json", name))
	if err != nil {
		return nil, err
	}
	var testcases []precompiledTest
	err = json.Unmarshal(data, &testcases)
	return testcases, err
}

func loadJsonFail(name string) ([]precompiledFailureTest, error) {
	data, err := os.ReadFile(fmt.Sprintf("testdata/precompiles/fail-%v.json", name))
	if err != nil {
		return nil, err
	}
	var testcases []precompiledFailureTest
	err = json.Unmarshal(data, &testcases)
	return testcases, err
}

// BenchmarkPrecompiledBLS12381G1MultiExpWorstCase benchmarks the worst case we could find that still fits a gaslimit of 10MGas.
func BenchmarkPrecompiledBLS12381G1MultiExpWorstCase(b *testing.B) {
	task := "0000000000000000000000000000000008d8c4a16fb9d8800cce987c0eadbb6b3b005c213d44ecb5adeed713bae79d606041406df26169c35df63cf972c94be1" +
		"0000000000000000000000000000000011bc8afe71676e6730702a46ef817060249cd06cd82e6981085012ff6d013aa4470ba3a2c71e13ef653e1e223d1ccfe9" +
		"FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"
	input := task
	for i := 0; i < 4787; i++ {
		input = input + task
	}
	testcase := precompiledTest{
		Input:       input,
		Expected:    "0000000000000000000000000000000005a6310ea6f2a598023ae48819afc292b4dfcb40aabad24a0c2cb6c19769465691859eeb2a764342a810c5038d700f18000000000000000000000000000000001268ac944437d15923dc0aec00daa9250252e43e4b35ec7a19d01f0d6cd27f6e139d80dae16ba1c79cc7f57055a93ff5",
		Name:        "WorstCaseG1",
		NoBenchmark: false,
	}
	benchmarkPrecompiled(b, "f0c", testcase)
}

// BenchmarkPrecompiledBLS12381G2MultiExpWorstCase benchmarks the worst case we could find that still fits a gaslimit of 10MGas.
func BenchmarkPrecompiledBLS12381G2MultiExpWorstCase(b *testing.B) {
	task := "000000000000000000000000000000000d4f09acd5f362e0a516d4c13c5e2f504d9bd49fdfb6d8b7a7ab35a02c391c8112b03270d5d9eefe9b659dd27601d18f" +
		"000000000000000000000000000000000fd489cb75945f3b5ebb1c0e326d59602934c8f78fe9294a8877e7aeb95de5addde0cb7ab53674df8b2cfbb036b30b99" +
		"00000000000000000000000000000000055dbc4eca768714e098bbe9c71cf54b40f51c26e95808ee79225a87fb6fa1415178db47f02d856fea56a752d185f86b" +
		"000000000000000000000000000000001239b7640f416eb6e921fe47f7501d504fadc190d9cf4e89ae2b717276739a2f4ee9f637c35e23c480df029fd8d247c7" +
		"FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"
	input := task
	for i := 0; i < 1040; i++ {
		input = input + task
	}

	testcase := precompiledTest{
		Input:       input,
		Expected:    "0000000000000000000000000000000018f5ea0c8b086095cfe23f6bb1d90d45de929292006dba8cdedd6d3203af3c6bbfd592e93ecb2b2c81004961fdcbb46c00000000000000000000000000000000076873199175664f1b6493a43c02234f49dc66f077d3007823e0343ad92e30bd7dc209013435ca9f197aca44d88e9dac000000000000000000000000000000000e6f07f4b23b511eac1e2682a0fc224c15d80e122a3e222d00a41fab15eba645a700b9ae84f331ae4ed873678e2e6c9b000000000000000000000000000000000bcb4849e460612aaed79617255fd30c03f51cf03d2ed4163ca810c13e1954b1e8663157b957a601829bb272a4e6c7b8",
		Name:        "WorstCaseG2",
		NoBenchmark: false,
	}
	benchmarkPrecompiled(b, "f0f", testcase)
}

// Benchmarks the sample inputs from the P256VERIFY precompile.
func BenchmarkPrecompiledP256Verify(b *testing.B) {
	testcase := precompiledTest{
		Input:    "4cee90eb86eaa050036147a12d49004b6b9c72bd725d39d4785011fe190f0b4da73bd4903f0ce3b639bbbf6e8e80d16931ff4bcf5993d58468e8fb19086e8cac36dbcd03009df8c59286b162af3bd7fcc0450c9aa81be5d10d312af6c66b1d604aebd3099c618202fcfe16ae7770b0c49ab5eadf74b754204a3bb6060e44eff37618b065f9832de4ca6ca971a7a1adc826d0f7c00181a5fb2ddf79ae00b4e10e",
		Expected: "0000000000000000000000000000000000000000000000000000000000000001",
		Name:     "p256Verify",
	}
	benchmarkPrecompiled(b, "100", testcase)
}

func TestPrecompiledP256Verify(t *testing.T) {
	t.Parallel()
	testJson("p256Verify", "100", t)
	testJson("p256Verify-EIP-7951", "a100", t)
}

// TestLisovoP256VerifyGasCost verifies P256 precompile gas cost changes at Lisovo
func TestLisovoP256VerifyGasCost(t *testing.T) {
	preLisovo := &p256Verify{eip7951: false}
	postLisovo := &p256Verify{eip7951: true}

	preGas := preLisovo.RequiredGas(nil)
	postGas := postLisovo.RequiredGas(nil)

	if preGas != params.P256VerifyGas {
		t.Errorf("pre-Lisovo gas: got %d, want %d", preGas, params.P256VerifyGas)
	}
	if postGas != params.P256VerifyGasEIP7951 {
		t.Errorf("post-Lisovo gas: got %d, want %d", postGas, params.P256VerifyGasEIP7951)
	}
	if preGas >= postGas {
		t.Errorf("post-Lisovo gas (%d) should be higher than pre-Lisovo (%d)", postGas, preGas)
	}
}

// TestLisovoCLZOpcode verifies CLZ opcode availability at Lisovo.
func TestLisovoCLZOpcode(t *testing.T) {
	preLisovo := newBhilaiInstructionSet()
	postLisovo := newLisovoInstructionSet()

	// Pre-Lisovo: CLZ should be undefined.
	if preLisovo[CLZ].execute != nil && preLisovo[CLZ].constantGas != 0 {
		t.Error("CLZ opcode should not be defined pre-Lisovo")
	}

	// Post-Lisovo: CLZ should be defined.
	if postLisovo[CLZ].execute == nil {
		t.Error("CLZ opcode should be defined post-Lisovo")
	}
	if postLisovo[CLZ].constantGas != GasFastStep {
		t.Errorf("CLZ gas: got %d, want %d", postLisovo[CLZ].constantGas, GasFastStep)
	}
}

// TestPointEvaluationPrecompileRemoval verifies that the kzgPointEvaluation precompile
// is present from Madhugiri through Lisovo, and is not present before Madhugiri and starting
// with LisovoPro.
func TestPointEvaluationPrecompileRemoval(t *testing.T) {
	t.Parallel()

	kzgPointEvaluationAddr := common.BytesToAddress([]byte{0x0a})
	kzgPointEvaluationPrecompile := &pointEvaluation{}

	// We verify a few things in this test:
	//   - Madhugiri, MadhugiriPro, and Lisovo have the kzg precompile enabled
	//   - LisovoPro removes it, so it should not be enabled
	//   - Hard forks before Madhugiri (for example, Bhilai, Napoli) should not have kzg enabled
	// Note that this test is slightly different from bor as in erigon we never enable
	// ethereum hard forks like Prague, Cancun.
	type testCase struct {
		name          string
		rules         chain.Rules
		shouldHaveKzg bool
	}
	cases := []testCase{
		{name: "Napoli (Pre-Madhugiri)", rules: chain.Rules{IsNapoli: true}, shouldHaveKzg: false},
		{name: "Bhilai (Pre-Madhugiri)", rules: chain.Rules{IsBhilai: true}, shouldHaveKzg: false},
		{name: "Madhugiri", rules: chain.Rules{IsMadhugiri: true}, shouldHaveKzg: true},
		{name: "MadhugiriPro", rules: chain.Rules{IsMadhugiriPro: true}, shouldHaveKzg: true},
		{name: "Lisovo", rules: chain.Rules{IsLisovo: true}, shouldHaveKzg: true},
		{name: "LisovoPro", rules: chain.Rules{IsLisovoPro: true}, shouldHaveKzg: false},
		{name: "Chicago", rules: chain.Rules{IsChicago: true}, shouldHaveKzg: false},
	}
	for _, tc := range cases {
		precompiles := Precompiles(&tc.rules)
		pc, exists := precompiles[kzgPointEvaluationAddr]
		if tc.shouldHaveKzg && !exists {
			t.Errorf("kzgPointEvaluation (0x0a) should exist in %v precompiles", tc.name)
		}
		if !tc.shouldHaveKzg && exists {
			t.Errorf("kzgPointEvaluation (0x0a) should not exist in %v precompiles", tc.name)
		}
		if exists && pc.Name() != kzgPointEvaluationPrecompile.Name() {
			t.Errorf("invalid precompile loaded instead of kzgPointEvaluation (0x0a). expected name: %s, got name: %s, test case: %s", kzgPointEvaluationPrecompile.Name(), pc.Name(), tc.name)
		}
	}
}

// pip88StorageReader is a minimal state.StateReader that returns a configured
// committed-storage value for one (address, slot) pair. It lets the SStore
// test populate "original" (committed) state without going through a real
// commit, mirroring the Finalise(false) trick used in the bor test.
type pip88StorageReader struct {
	state.NoopReader
	addr common.Address
	slot common.Hash
	val  uint256.Int
}

func (r *pip88StorageReader) ReadAccountStorage(address common.Address, key common.Hash) (uint256.Int, bool, error) {
	if address == r.addr && key == r.slot {
		return r.val, true, nil
	}
	return uint256.Int{}, false, nil
}

// TestPIP88PrecompileGasCosts verifies pre- and post-PIP-88 gas for every
// precompile repriced by the Chicago fork.
func TestPIP88PrecompileGasCosts(t *testing.T) {
	t.Parallel()

	// blake2F input: 213 bytes, rounds=12 in big-endian uint32 at [0:4].
	blake2FInput := make([]byte, 213)
	blake2FInput[3] = 12

	cases := []struct {
		addr    byte
		input   []byte
		preGas  uint64
		postGas uint64
		name    string
	}{
		{0x06, nil, params.Bn254AddGasIstanbul, params.Bn254AddGasIstanbulPIP88, "bn254Add (3.6x)"},
		{0x07, nil, params.Bn254ScalarMulGasIstanbul, params.Bn254ScalarMulGasIstanbulPIP88, "bn254ScalarMul (2.1x)"},
		{0x0b, nil, params.Bls12381G1AddGas, params.Bls12381G1AddGasPIP88, "bls12381G1Add (2.8x)"},
		{0x0d, nil, params.Bls12381G2AddGas, params.Bls12381G2AddGasPIP88, "bls12381G2Add (2.7x)"},
		{0x10, nil, params.Bls12381MapFpToG1Gas, params.Bls12381MapFpToG1GasPIP88, "bls12381MapFpToG1 (2.8x)"},
		{0x11, nil, params.Bls12381MapFp2ToG2Gas, params.Bls12381MapFp2ToG2GasPIP88, "bls12381MapFp2ToG2 (2.8x)"},
		{
			addr:    0x08,
			input:   make([]byte, 192),
			preGas:  params.Bn254PairingBaseGasIstanbul + params.Bn254PairingPerPointGasIstanbul,
			postGas: params.Bn254PairingBaseGasIstanbulPIP88 + params.Bn254PairingPerPointGasIstanbulPIP88,
			name:    "bn254Pairing k=1 (1.5x)",
		},
		{
			addr:    0x0f,
			input:   make([]byte, 384),
			preGas:  params.Bls12381PairingBaseGas + params.Bls12381PairingPerPairGas,
			postGas: params.Bls12381PairingBaseGasPIP88 + params.Bls12381PairingPerPairGasPIP88,
			name:    "bls12381Pairing k=1 (2.9x)",
		},
		// MSM at k=1: discount table[0]=1000, so gas = mulGas.
		{0x0c, make([]byte, 160), params.Bls12381G1MulGas, params.Bls12381G1MulGasPIP88, "bls12381G1MultiExp k=1 (6.1x)"},
		{0x0e, make([]byte, 288), params.Bls12381G2MulGas, params.Bls12381G2MulGasPIP88, "bls12381G2MultiExp k=1 (6.4x)"},
		{0x09, blake2FInput, 12, 12 * params.GFROUNDPIP88, "blake2F rounds=12 (22x)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr := common.BytesToAddress([]byte{tc.addr})

			pre, ok := PrecompiledContractsLisovoPro[addr]
			if !ok {
				t.Fatalf("0x%02x missing from PrecompiledContractsLisovoPro", tc.addr)
			}
			post, ok := PrecompiledContractsChicago[addr]
			if !ok {
				t.Fatalf("0x%02x missing from PrecompiledContractsChicago", tc.addr)
			}

			if got := pre.RequiredGas(tc.input); got != tc.preGas {
				t.Errorf("pre-PIP-88 gas: got %d, want %d", got, tc.preGas)
			}
			if got := post.RequiredGas(tc.input); got != tc.postGas {
				t.Errorf("post-PIP-88 gas: got %d, want %d", got, tc.postGas)
			}
		})
	}
}

// TestPIP88SStoreGas walks every branch of makeGasSStoreFuncPIP88 and verifies
// the gas charged and refund-pool delta match closed-form values.
func TestPIP88SStoreGas(t *testing.T) {
	t.Parallel()

	addr := common.Address{0xaa}
	slot := common.BigToHash(big.NewInt(1))
	val42 := *uint256.NewInt(0x42)
	val99 := *uint256.NewInt(0x99)
	zero := uint256.Int{}

	cases := []struct {
		name            string
		original        uint256.Int // committed value before this tx
		current         uint256.Int // dirty value (only applied if != original)
		value           uint256.Int // value being written by SSTORE
		warm            bool
		wantGas         uint64
		wantRefundDelta int64
	}{
		{
			name:     "cold reset existing slot (EIP-2929 invariant: total = 5000)",
			original: val42, current: val42, value: val99, warm: false,
			wantGas: params.SstoreResetGasEIP2200, wantRefundDelta: 0,
		},
		{
			name:     "warm reset existing slot",
			original: val42, current: val42, value: val99, warm: true,
			wantGas: params.SstoreResetGasEIP2200 - params.ColdSstoreCostPIP88, wantRefundDelta: 0,
		},
		{
			name:     "cold create slot",
			original: zero, current: zero, value: val99, warm: false,
			wantGas: params.ColdSstoreCostPIP88 + params.SstoreSetGasEIP2200, wantRefundDelta: 0,
		},
		{
			name:     "cold delete clean slot (clearingRefund = SstoreClearsScheduleRefundPIP88)",
			original: val42, current: val42, value: zero, warm: false,
			wantGas:         params.SstoreResetGasEIP2200,
			wantRefundDelta: int64(params.SstoreClearsScheduleRefundPIP88),
		},
		{
			name:     "cold noop (current == value)",
			original: val42, current: val42, value: val42, warm: false,
			wantGas: params.ColdSstoreCostPIP88 + params.WarmStorageReadCostEIP2929, wantRefundDelta: 0,
		},
		{
			name:     "reset to original existing slot (refund = (RESET - cold) - warm)",
			original: val42, current: val99, value: val42, warm: true,
			wantGas:         params.WarmStorageReadCostEIP2929,
			wantRefundDelta: int64((params.SstoreResetGasEIP2200 - params.ColdSstoreCostPIP88) - params.WarmStorageReadCostEIP2929),
		},
		{
			name:     "reset to clean zero (refund = SstoreSet - warm, unchanged from EIP-3529)",
			original: zero, current: val99, value: zero, warm: true,
			wantGas:         params.WarmStorageReadCostEIP2929,
			wantRefundDelta: int64(params.SstoreSetGasEIP2200 - params.WarmStorageReadCostEIP2929),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// pip88StorageReader supplies tc.original as the committed value for slot.
			reader := &pip88StorageReader{addr: addr, slot: slot, val: tc.original}
			statedb := state.New(reader)
			require.NoError(t, statedb.CreateAccount(addr, false))

			// Apply dirty current value when it differs from committed original.
			if !tc.current.Eq(&tc.original) {
				require.NoError(t, statedb.SetState(addr, slot, tc.current))
			}
			if tc.warm {
				statedb.AddSlotToAccessList(addr, slot)
			}

			evm := NewEVM(
				evmtypes.BlockContext{BlockNumber: 1, Time: 1, PrevRanDao: &common.Hash{}},
				evmtypes.TxContext{},
				statedb, chain.TestChainBorConfig, Config{},
			)
			contract := NewContract(AccountRef(common.Address{}), addr, uint256.NewInt(0), 1_000_000, false, NewJumpDestCache(16))

			stack := New()
			stack.push(new(uint256.Int).Set(&tc.value))         // Back(1) = value
			stack.push(new(uint256.Int).SetBytes(slot.Bytes())) // peek = slot

			refundBefore := statedb.GetRefund()
			gas, err := gasSStorePIP88(evm, contract, stack, NewMemory(), 0)
			if err != nil {
				t.Fatalf("gasSStorePIP88 returned error: %v", err)
			}
			refundDelta := int64(statedb.GetRefund()) - int64(refundBefore)

			if gas != tc.wantGas {
				t.Errorf("gas: got %d, want %d", gas, tc.wantGas)
			}
			if refundDelta != tc.wantRefundDelta {
				t.Errorf("refund delta: got %d, want %d", refundDelta, tc.wantRefundDelta)
			}
		})
	}
}

// TestPIP88ForkBoundary verifies that the Chicago fork dispatch flips at the
// configured block: precompile set and SLOAD instruction-set gas function both
// switch from EIP-3529/LisovoPro to PIP-88 at block N (with N-1 still old).
func TestPIP88ForkBoundary(t *testing.T) {
	t.Parallel()

	const chicagoBlock = 100

	// Construct a minimal bor chain config with Chicago pushed to a specific
	// block so we have a real boundary to test against. All earlier height
	// forks default to active at block 0 (matching TestChainBorConfig).
	cfg := &chain.Config{
		ChainID:               big.NewInt(1337),
		Consensus:             chain.BorConsensus,
		HomesteadBlock:        big.NewInt(0),
		TangerineWhistleBlock: big.NewInt(0),
		SpuriousDragonBlock:   big.NewInt(0),
		ByzantiumBlock:        big.NewInt(0),
		ConstantinopleBlock:   big.NewInt(0),
		PetersburgBlock:       big.NewInt(0),
		IstanbulBlock:         big.NewInt(0),
		MuirGlacierBlock:      big.NewInt(0),
		BerlinBlock:           big.NewInt(0),
		LondonBlock:           big.NewInt(0),
		Bor: &borcfg.BorConfig{
			AgraBlock:         big.NewInt(0),
			NapoliBlock:       big.NewInt(0),
			AhmedabadBlock:    big.NewInt(0),
			BhilaiBlock:       big.NewInt(0),
			RioBlock:          big.NewInt(0),
			MadhugiriBlock:    big.NewInt(0),
			MadhugiriProBlock: big.NewInt(0),
			LisovoBlock:       big.NewInt(0),
			LisovoProBlock:    big.NewInt(0),
			GiuglianoBlock:    big.NewInt(0),
			ChicagoBlock:      big.NewInt(chicagoBlock),
		},
	}

	addr := common.Address{0xaa}
	slot := common.BigToHash(big.NewInt(1))

	cases := []struct {
		name             string
		block            uint64
		wantIsChicago    bool
		wantBn254AddGas  uint64 // probe for ActivePrecompiledContracts dispatch
		wantColdSloadGas uint64 // probe for instruction-set dispatch
	}{
		{
			name:             "block N-1 (pre-Chicago)",
			block:            chicagoBlock - 1,
			wantIsChicago:    false,
			wantBn254AddGas:  params.Bn254AddGasIstanbul,
			wantColdSloadGas: params.ColdSloadCostEIP2929,
		},
		{
			name:             "block N (Chicago active)",
			block:            chicagoBlock,
			wantIsChicago:    true,
			wantBn254AddGas:  params.Bn254AddGasIstanbulPIP88,
			wantColdSloadGas: params.ColdSloadCostPIP88,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bc := evmtypes.BlockContext{BlockNumber: tc.block, Time: 1}
			rules := bc.Rules(cfg)
			if rules.IsChicago != tc.wantIsChicago {
				t.Fatalf("rules.IsChicago: got %v, want %v", rules.IsChicago, tc.wantIsChicago)
			}

			// Precompile dispatch probe.
			bn254Add := Precompiles(rules)[common.BytesToAddress([]byte{0x06})]
			if got := bn254Add.RequiredGas(nil); got != tc.wantBn254AddGas {
				t.Errorf("bn254Add gas via Precompiles: got %d, want %d", got, tc.wantBn254AddGas)
			}

			// Instruction-set dispatch probe via SLOAD's dynamicGas on a cold slot.
			statedb := state.New(state.NewNoopReader())
			require.NoError(t, statedb.CreateAccount(addr, false))
			evm := NewEVM(
				evmtypes.BlockContext{BlockNumber: tc.block, Time: 1, PrevRanDao: &common.Hash{}},
				evmtypes.TxContext{},
				statedb, cfg, Config{},
			)
			contract := NewContract(AccountRef(common.Address{}), addr, uint256.NewInt(0), 1_000_000, false, NewJumpDestCache(16))
			stack := New()
			stack.push(new(uint256.Int).SetBytes(slot.Bytes()))

			jt := evm.Interpreter().(*EVMInterpreter).jt
			gas, err := jt[SLOAD].dynamicGas(evm, contract, stack, NewMemory(), 0)
			if err != nil {
				t.Fatalf("SLOAD dynamicGas: %v", err)
			}
			if gas != tc.wantColdSloadGas {
				t.Errorf("cold SLOAD gas: got %d, want %d", gas, tc.wantColdSloadGas)
			}
		})
	}
}
