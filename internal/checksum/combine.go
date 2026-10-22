package checksum

import (
	"encoding/binary"
	"fmt"
)

// IsCRC reports whether a is a CRC, whose values can be combined.
func (a Algorithm) IsCRC() bool { return a == CRC32 || a == CRC32C || a == CRC64NVME }

// reflected polynomials, as used by hash/crc32 and hash/crc64
var polys = map[Algorithm]uint64{
	CRC32:     0xedb88320,
	CRC32C:    0x82f63b78,
	CRC64NVME: 0x9a6c9329ac4bc9b5,
}

// Combine returns the CRC of A‖B given CRC(A), CRC(B) and len(B), without the
// data: the zlib crc32_combine method (GF(2) matrix exponentiation), generic
// over width. It works for CRCs with all-ones init and final XOR, which all
// three S3 CRCs are. Sums are big-endian bytes as produced by hash.Sum.
func Combine(a Algorithm, sumA, sumB []byte, lenB int64) ([]byte, error) {
	poly, ok := polys[a]
	if !ok {
		return nil, fmt.Errorf("checksum: %s cannot be combined", a)
	}
	width := len(sumA) * 8
	if len(sumB) != len(sumA) || (width != 32 && width != 64) {
		return nil, fmt.Errorf("checksum: bad %s sums", a)
	}
	crcA, crcB := fromBytes(sumA), fromBytes(sumB)
	return toBytes(combine(crcA, crcB, lenB, poly, width), width), nil
}

func fromBytes(b []byte) uint64 {
	if len(b) == 4 {
		return uint64(binary.BigEndian.Uint32(b))
	}
	return binary.BigEndian.Uint64(b)
}

func toBytes(v uint64, width int) []byte {
	if width == 32 {
		return binary.BigEndian.AppendUint32(nil, uint32(v))
	}
	return binary.BigEndian.AppendUint64(nil, v)
}

func gf2Times(mat []uint64, vec uint64) uint64 {
	var sum uint64
	for i := 0; vec != 0; i, vec = i+1, vec>>1 {
		if vec&1 != 0 {
			sum ^= mat[i]
		}
	}
	return sum
}

func gf2Square(square, mat []uint64) {
	for n := range mat {
		square[n] = gf2Times(mat, mat[n])
	}
}

func combine(crc1, crc2 uint64, len2 int64, poly uint64, width int) uint64 {
	if len2 <= 0 {
		return crc1
	}
	even := make([]uint64, width) // operator for an even power of two zero bits
	odd := make([]uint64, width)  // and for an odd power

	odd[0] = poly // the operator for one zero bit
	row := uint64(1)
	for n := 1; n < width; n++ {
		odd[n] = row
		row <<= 1
	}
	gf2Square(even, odd) // two zero bits
	gf2Square(odd, even) // four zero bits

	// Apply len2 zero bytes to crc1; the first squaring gives one zero byte.
	for {
		gf2Square(even, odd)
		if len2&1 != 0 {
			crc1 = gf2Times(even, crc1)
		}
		len2 >>= 1
		if len2 == 0 {
			break
		}
		gf2Square(odd, even)
		if len2&1 != 0 {
			crc1 = gf2Times(odd, crc1)
		}
		len2 >>= 1
		if len2 == 0 {
			break
		}
	}
	return crc1 ^ crc2
}
