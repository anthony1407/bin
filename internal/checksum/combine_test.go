package checksum

import (
	"bytes"
	"math/rand/v2"
	"sort"
	"testing"
)

func sum(a Algorithm, data []byte) []byte {
	h := a.New()
	h.Write(data)
	return h.Sum(nil)
}

// Combining the CRCs of the pieces must equal the CRC of the whole, for
// every split, including empty pieces and sizes that are not powers of two.
func TestCombineMatchesWhole(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	data := make([]byte, 300_000)
	for i := range data {
		data[i] = byte(rng.Uint32())
	}
	for _, a := range []Algorithm{CRC32, CRC32C, CRC64NVME} {
		for trial := 0; trial < 40; trial++ {
			// Split into up to 6 random pieces.
			cuts := []int{0, len(data)}
			for n := rng.IntN(6); n > 0; n-- {
				cuts = append(cuts, rng.IntN(len(data)+1))
			}
			sort.Ints(cuts)
			acc := sum(a, data[cuts[0]:cuts[1]])
			for i := 1; i+1 < len(cuts); i++ {
				piece := data[cuts[i]:cuts[i+1]]
				var err error
				if acc, err = Combine(a, acc, sum(a, piece), int64(len(piece))); err != nil {
					t.Fatal(err)
				}
			}
			if want := sum(a, data); !bytes.Equal(acc, want) {
				t.Fatalf("%s cuts %v: combined %x, whole %x", a, cuts, acc, want)
			}
		}
	}
}

func TestCombineRejectsHashes(t *testing.T) {
	if _, err := Combine(SHA256, sum(SHA256, nil), sum(SHA256, nil), 0); err == nil {
		t.Error("SHA-256 values cannot be combined")
	}
}
