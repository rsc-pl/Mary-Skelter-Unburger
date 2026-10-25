package updater

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

// Linker-injected release values. They deliberately contain only XOR shares
// and a byte permutation; the raw .unburger-assets key is never written to a
// generated source file or copied into the release directory.
var (
	EmbeddedAssetKeyShareA string
	EmbeddedAssetKeyShareB string
	EmbeddedAssetKeyShareC string
	EmbeddedAssetKeyPerm   string
)

// EmbeddedAssetV3Key reconstructs the release key when all linker-injected
// shares are present. Development builds simply return ok=false and may use a
// local maintainer key file instead.
func EmbeddedAssetV3Key() (key []byte, ok bool, err error) {
	vals := []string{EmbeddedAssetKeyShareA, EmbeddedAssetKeyShareB, EmbeddedAssetKeyShareC, EmbeddedAssetKeyPerm}
	set := 0
	for _, v := range vals {
		if v != "" {
			set++
		}
	}
	if set == 0 {
		return nil, false, nil
	}
	if set != len(vals) {
		return nil, false, fmt.Errorf("embedded asset key shares are incomplete")
	}
	encoded := make([][]byte, 3)
	for i, s := range vals[:3] {
		b, e := hex.DecodeString(s)
		if e != nil || len(b) != 32 {
			return nil, false, fmt.Errorf("embedded asset key share %d is invalid", i+1)
		}
		encoded[i] = b
	}
	perm, e := hex.DecodeString(vals[3])
	if e != nil || len(perm) != 32 {
		return nil, false, fmt.Errorf("embedded asset key permutation is invalid")
	}
	seen := [32]bool{}
	shares := make([][32]byte, 3)
	for i, p := range perm {
		if p >= 32 || seen[p] {
			return nil, false, fmt.Errorf("embedded asset key permutation is not bijective")
		}
		seen[p] = true
		for s := range shares {
			shares[s][p] = encoded[s][i]
		}
	}
	key = make([]byte, 32)
	for i := range key {
		key[i] = shares[0][i] ^ shares[1][i] ^ shares[2][i]
	}
	return key, true, nil
}

// GenerateAssetV3KeyShares creates three random XOR shares and independently
// permutes their byte positions. The returned strings are suitable for Go
// linker -X injection and intentionally never contain the raw key itself.
func GenerateAssetV3KeyShares(key []byte) (a, b, c, permutation string, err error) {
	if len(key) != 32 {
		return "", "", "", "", fmt.Errorf("asset key must be exactly 32 bytes")
	}
	var shares [3][32]byte
	if _, err = rand.Read(shares[0][:]); err != nil {
		return
	}
	if _, err = rand.Read(shares[1][:]); err != nil {
		return
	}
	for i := 0; i < 32; i++ {
		shares[2][i] = key[i] ^ shares[0][i] ^ shares[1][i]
	}
	perm := make([]byte, 32)
	for i := range perm {
		perm[i] = byte(i)
	}
	for i := len(perm) - 1; i > 0; i-- {
		n, e := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if e != nil {
			err = e
			return
		}
		j := int(n.Int64())
		perm[i], perm[j] = perm[j], perm[i]
	}
	enc := [3][32]byte{}
	for i, p := range perm {
		for s := range shares {
			enc[s][i] = shares[s][p]
		}
	}
	return hex.EncodeToString(enc[0][:]), hex.EncodeToString(enc[1][:]), hex.EncodeToString(enc[2][:]), hex.EncodeToString(perm), nil
}
