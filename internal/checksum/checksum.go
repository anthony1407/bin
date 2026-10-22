// Package checksum implements the data integrity checksums S3 clients send
// with uploads (x-amz-checksum-*): CRC32, CRC32C, CRC64NVME, SHA1 and SHA256.
package checksum

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"strings"
)

// Algorithm is an S3 checksum algorithm name, in its canonical upper case.
type Algorithm string

const (
	CRC32     Algorithm = "CRC32"
	CRC32C    Algorithm = "CRC32C"
	CRC64NVME Algorithm = "CRC64NVME"
	SHA1      Algorithm = "SHA1"
	SHA256    Algorithm = "SHA256"
)

// All lists every supported algorithm.
var All = []Algorithm{CRC32, CRC32C, CRC64NVME, SHA1, SHA256}

// CRC-64/NVME: reflected polynomial 0x9a6c9329ac4bc9b5, init and xorout all
// ones, which is exactly what hash/crc64 computes for a custom table.
var (
	crc64NVMETable = crc64.MakeTable(0x9a6c9329ac4bc9b5)
	castagnoli     = crc32.MakeTable(crc32.Castagnoli)
)

// Parse returns the algorithm for a name in any case ("crc64nvme", "SHA256").
func Parse(name string) (Algorithm, bool) {
	a := Algorithm(strings.ToUpper(strings.TrimSpace(name)))
	for _, known := range All {
		if a == known {
			return a, true
		}
	}
	return "", false
}

// New returns a fresh hash for the algorithm.
func (a Algorithm) New() hash.Hash {
	switch a {
	case CRC32:
		return crc32.NewIEEE()
	case CRC32C:
		return crc32.New(castagnoli)
	case CRC64NVME:
		return crc64.New(crc64NVMETable)
	case SHA1:
		return sha1.New()
	case SHA256:
		return sha256.New()
	}
	panic("checksum: unknown algorithm " + string(a))
}

// Header is the request/response header carrying this checksum.
func (a Algorithm) Header() string { return "x-amz-checksum-" + strings.ToLower(string(a)) }

// Encode renders a digest the way S3 transmits it: standard base64.
func Encode(sum []byte) string { return base64.StdEncoding.EncodeToString(sum) }

// Valid reports whether value is a well-formed base64 digest for a.
func (a Algorithm) Valid(value string) bool {
	b, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(b) == a.New().Size()
}
