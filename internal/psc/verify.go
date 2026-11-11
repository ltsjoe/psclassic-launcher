package psc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math/big"
)

// DefaultPublicKey is the manifest signing key built into the official launcher
// (PlanetsideClassic.exe, BuildInfo.PublicKey). It is a Windows CNG ECCPUBLICBLOB:
// "ECS1", the key length (32), then X and Y of a P-256 point.
const DefaultPublicKey = "RUNTMSAAAADBI8v3hHtDqJChbaKc0iccFWrcIUaGeIeD/7kUraLTSw9KNrwhb9sPv7tY19qsnZVIX13hH7QKant9BEPkeuTu"

// ParsePublicKey decodes a base64 ECS1 blob into a P-256 public key.
func ParsePublicKey(b64 string) (*ecdsa.PublicKey, error) {
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("public key is not valid base64")
	}
	if len(blob) != 8+64 || string(blob[:4]) != "ECS1" || binary.LittleEndian.Uint32(blob[4:8]) != 32 {
		return nil, errors.New("public key is not a P-256 ECDSA key blob (ECS1)")
	}
	point := append([]byte{4}, blob[8:]...) // SEC 1 uncompressed point
	return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
}

// VerifySignature checks sig over data the way ECDsaCng.VerifyData does with SHA-256:
// sig is the raw 64-byte r||s (IEEE P1363), not ASN.1.
func VerifySignature(pub *ecdsa.PublicKey, data, sig []byte) bool {
	if len(sig) != 64 {
		return false
	}
	h := sha256.Sum256(data)
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	return ecdsa.Verify(pub, h[:], r, s)
}
