package netease

// NetEase request encryption primitives, ported 1:1 from the reference
// go-music-dl / music-lib netease package (crypto.go). The upstream moved
// search to the /api/linux/forward pipeline (AES-128-ECB with the public
// linux key) and download URLs to the weapi pipeline (AES-128-CBC + RSA).
// Both schemes use fixed public keys, so a pure-Go port is stable — no
// rotating keys, no sidecar.
//
// Sources of truth (verified against live upstream 2026-08):
//   - Search:  POST http://music.163.com/api/linux/forward with form
//     `eparams=<EncryptLinux({method,url,params})>` → plain JSON.
//   - Audio:   POST http://music.163.com/weapi/song/enhance/player/url
//     with form `params=<AES-CBC layer1+layer2>&encSecKey=<RSA>`.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

// linuxApiKeyHex is the AES-128 key the NetEase linux client uses for the
// /api/linux/forward `eparams` envelope.
const linuxApiKeyHex = "7246674226682325323F5E6544673A51"

// weapi constants (identical to the well-known NetEase web client scheme).
const (
	weapiNonce      = "0CoJUm6Qyw8W8jud"
	weapiIV         = "0102030405060708"
	weapiPubModulus = "00e0b509f6259df8642dbc35662901477df22677ec152b5ff68ace615bb7b725152b3ab17a876aea8a5aa76d2e417629ec4ee341f56135fccf695280104e0312ecbda92557c93870114af6c9d05c4f7f0c3685b7a46bee255932575cce10b424d813cfe4875d3e82047b97ddef52741d546b8e289dc6935b3ece0462db0a22b8e7"
	weapiPubKey     = "010001"
)

// pkcs7Pad pads src to a multiple of blockSize using PKCS#7.
func pkcs7Pad(src []byte, blockSize int) []byte {
	pad := blockSize - len(src)%blockSize
	out := make([]byte, len(src)+pad)
	copy(out, src)
	for i := len(src); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// aesEncryptECB encrypts src with AES-128-ECB + PKCS#7. Go's stdlib has no
// ECB mode, so we iterate blocks manually (identical to the reference).
func aesEncryptECB(src, key []byte) []byte {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	src = pkcs7Pad(src, block.BlockSize())
	out := make([]byte, len(src))
	for bs, be := 0, block.BlockSize(); bs < len(src); bs, be = bs+block.BlockSize(), be+block.BlockSize() {
		block.Encrypt(out[bs:be], src[bs:be])
	}
	return out
}

// aesEncryptCBC encrypts src with AES-128-CBC + PKCS#7 and returns base64.
func aesEncryptCBC(src []byte, key, iv string) string {
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return ""
	}
	src = pkcs7Pad(src, block.BlockSize())
	out := make([]byte, len(src))
	cipher.NewCBCEncrypter(block, []byte(iv)).CryptBlocks(out, src)
	return base64.StdEncoding.EncodeToString(out)
}

// rsaEncryptNoPad performs the reference's RSA "encryption" of the reversed
// secKey: pow(reverse(secKey) as big-int hex, pubkey, modulus), 256-hex pad.
func rsaEncryptNoPad(text, pubKey, modulus string) string {
	reversed := []byte(text)
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	biText := new(big.Int).SetBytes(reversed)
	biPub := new(big.Int)
	biPub.SetString(pubKey, 16)
	biMod := new(big.Int)
	biMod.SetString(modulus, 16)
	biRet := new(big.Int).Exp(biText, biPub, biMod)
	return biRet.Text(16)
}

// randRead is crypto/rand.Read behind a seam, so the failure path below can
// be exercised: an entropy source that cannot fail on demand is an entropy
// source whose error handling is never tested.
var randRead = rand.Read

// randomString generates a size-char alphanumeric string for the weapi
// secKey.
//
// The error is returned rather than papered over. Falling back to math/rand
// would look fine (Go 1.20+ auto-seeds the global source) but the value then
// comes from a source the process has no control over, and the secKey is what
// the weapi body is encrypted with — a predictable key there means a request
// anybody can forge. A
// crypto/rand failure is a broken platform or a broken container; refusing
// the request is the honest response, and the caller already treats a
// failure here as "no audio URL".
func randomString(size int) (string, error) {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// Rejection sampling rather than letters[v%len(letters)]: 256 is not a
	// multiple of 62, so the modulo form made the first 8 symbols appear
	// 5/256 of the time and the rest 4/256 — a small but free bias to drop
	// while the function is already being rewritten.
	limit := 256 - (256 % len(letters))
	out := make([]byte, 0, size)
	buf := make([]byte, size)
	for len(out) < size {
		if _, err := randRead(buf); err != nil {
			return "", fmt.Errorf("crypto/rand: %w", err)
		}
		for _, v := range buf {
			if int(v) >= limit {
				continue
			}
			out = append(out, letters[int(v)%len(letters)])
			if len(out) == size {
				break
			}
		}
	}
	return string(out), nil
}

// encryptLinux encodes the search envelope for /api/linux/forward.
// Output is uppercase hex of the AES-128-ECB ciphertext (reference:
// strings.ToUpper(hex.EncodeToString(encrypted))).
func encryptLinux(data string) string {
	key, _ := hex.DecodeString(linuxApiKeyHex)
	return hexEncodeUpper(aesEncryptECB([]byte(data), key))
}

// encryptWeapi produces the (params, encSecKey) form values for the weapi
// POST body. Two AES-128-CBC layers (nonce key, then random secKey) + RSA
// of the reversed secKey.
func encryptWeapi(text string) (params, encSecKey string, err error) {
	secKey, err := randomString(16)
	if err != nil {
		return "", "", err
	}
	layer1 := aesEncryptCBC([]byte(text), weapiNonce, weapiIV)
	params = aesEncryptCBC([]byte(layer1), secKey, weapiIV)
	encSecKey = rsaEncryptNoPad(secKey, weapiPubKey, weapiPubModulus)
	// Left-pad to 256 hex chars (the reference uses %0256x; big.Int.Text
	// drops leading zeros).
	for len(encSecKey) < 256 {
		encSecKey = "0" + encSecKey
	}
	return params, encSecKey, nil
}

func hexEncodeUpper(b []byte) string {
	const hexDigits = "0123456789ABCDEF"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexDigits[v>>4]
		out[i*2+1] = hexDigits[v&0x0f]
	}
	return string(out)
}
