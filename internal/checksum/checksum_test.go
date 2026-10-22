package checksum

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// Check values from the CRC catalogue (input "123456789") and FIPS 180 test
// vectors (input "abc").
func TestKnownVectors(t *testing.T) {
	crcs := map[Algorithm]uint64{
		CRC32:     0xcbf43926,
		CRC32C:    0xe3069283,
		CRC64NVME: 0xae8b14860a799888,
	}
	for a, want := range crcs {
		h := a.New()
		h.Write([]byte("123456789"))
		sum := h.Sum(nil)
		var got uint64
		if len(sum) == 4 {
			got = uint64(binary.BigEndian.Uint32(sum))
		} else {
			got = binary.BigEndian.Uint64(sum)
		}
		if got != want {
			t.Errorf("%s(123456789) = %#x, want %#x", a, got, want)
		}
	}
	hashes := map[Algorithm]string{
		SHA1:   "a9993e364706816aba3e25717850c26c9cd0d89d",
		SHA256: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
	}
	for a, want := range hashes {
		h := a.New()
		h.Write([]byte("abc"))
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			t.Errorf("%s(abc) = %s, want %s", a, got, want)
		}
	}
}

func TestParseAndHeader(t *testing.T) {
	a, ok := Parse(" crc64nvme ")
	if !ok || a != CRC64NVME || a.Header() != "x-amz-checksum-crc64nvme" {
		t.Errorf("Parse = %q, %v, header %q", a, ok, a.Header())
	}
	if _, ok := Parse("md5"); ok {
		t.Error("md5 is not an S3 checksum algorithm")
	}
}

func TestValid(t *testing.T) {
	if !CRC64NVME.Valid("OGYDL2FShCk=") || CRC64NVME.Valid("AAAA") || SHA256.Valid("not base64!") {
		t.Error("Valid misjudged a value")
	}
}
