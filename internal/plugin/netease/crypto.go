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
	"math/big"
	mrand "math/rand"
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

// randomString generates a 16-char alphanumeric string for the weapi secKey.
func randomString(size int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is practically impossible; fall back to
		// math/rand so the plugin still boots on exotic platforms.
		for i := range b {
			b[i] = byte(mrand.Intn(256))
		}
	}
	for i, v := range b {
		b[i] = letters[int(v)%len(letters)]
	}
	return string(b)
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
func encryptWeapi(text string) (params, encSecKey string) {
	secKey := randomString(16)
	layer1 := aesEncryptCBC([]byte(text), weapiNonce, weapiIV)
	params = aesEncryptCBC([]byte(layer1), secKey, weapiIV)
	encSecKey = rsaEncryptNoPad(secKey, weapiPubKey, weapiPubModulus)
	// Left-pad to 256 hex chars (the reference uses %0256x; big.Int.Text
	// drops leading zeros).
	for len(encSecKey) < 256 {
		encSecKey = "0" + encSecKey
	}
	return params, encSecKey
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
