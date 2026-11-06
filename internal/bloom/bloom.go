// Package bloom provides a standard bloom filter implementation.
package bloom

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"hash/fnv"
	"math"
	"sync"
)

// Filter is a thread-safe bloom filter.
type Filter struct {
	mu   sync.RWMutex
	bits []uint64 // bit array packed into uint64s
	m    uint     // total number of bits
	k    uint     // number of hash functions
	n    uint     // number of items added
}

// New creates a bloom filter sized for the expected number of items
// and target false-positive rate.
func New(expectedItems int, fpRate float64) *Filter {
	if expectedItems <= 0 {
		expectedItems = 1
	}
	if fpRate <= 0 || fpRate >= 1 {
		fpRate = 0.01
	}

	m := optimalM(expectedItems, fpRate)
	k := optimalK(uint(expectedItems), m)

	numUint64s := (m + 63) / 64
	return &Filter{
		bits: make([]uint64, numUint64s),
		m:    m,
		k:    k,
	}
}

// NewWithParams creates a bloom filter with explicit m (bits) and k (hashes).
func NewWithParams(m, k uint) *Filter {
	if m == 0 {
		m = 1024
	}
	if k == 0 {
		k = 1
	}
	numUint64s := (m + 63) / 64
	return &Filter{
		bits: make([]uint64, numUint64s),
		m:    m,
		k:    k,
	}
}

// Add inserts a byte slice into the filter.
func (f *Filter) Add(data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	h1, h2 := f.hashPair(data)
	for i := uint(0); i < f.k; i++ {
		idx := (h1 + uint64(i)*h2) % uint64(f.m)
		f.setBit(uint(idx))
	}
	f.n++
}

// AddString inserts a string into the filter.
func (f *Filter) AddString(s string) {
	f.Add([]byte(s))
}

// Contains returns true if the data may be in the filter.
// May return false positives but never false negatives.
func (f *Filter) Contains(data []byte) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()

	h1, h2 := f.hashPair(data)
	for i := uint(0); i < f.k; i++ {
		idx := (h1 + uint64(i)*h2) % uint64(f.m)
		if !f.getBit(uint(idx)) {
			return false
		}
	}
	return true
}

// ContainsString is a convenience for Contains([]byte(s)).
func (f *Filter) ContainsString(s string) bool {
	return f.Contains([]byte(s))
}

// Reset clears all bits.
func (f *Filter) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.bits {
		f.bits[i] = 0
	}
	f.n = 0
}

// Count returns the number of items added (insertions, not unique items).
func (f *Filter) Count() uint {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.n
}

// Size returns the number of bits in the filter.
func (f *Filter) Size() uint {
	return f.m
}

// HashCount returns the number of hash functions.
func (f *Filter) HashCount() uint {
	return f.k
}

// EstimatedFPRate returns the current estimated false-positive rate.
func (f *Filter) EstimatedFPRate() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.n == 0 {
		return 0
	}
	// (1 - e^(-kn/m))^k
	exp := -float64(f.k) * float64(f.n) / float64(f.m)
	inner := 1 - math.Exp(exp)
	return math.Pow(inner, float64(f.k))
}

// SetBitsRatio returns the fraction of bits currently set.
func (f *Filter) SetBitsRatio() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()

	setBits := 0
	for _, word := range f.bits {
		setBits += popCount(word)
	}
	if f.m == 0 {
		return 0
	}
	return float64(setBits) / float64(f.m)
}

// MarshalBinary serializes the filter for persistence.
func (f *Filter) MarshalBinary() ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var buf bytes.Buffer

	// Header: magic (4) + m (8) + k (8) + n (8)
	if _, err := buf.Write([]byte("BLM1")); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint64(f.m)); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint64(f.k)); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint64(f.n)); err != nil {
		return nil, err
	}

	// Body: gob-encoded bits
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(f.bits); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// UnmarshalBinary restores a filter from bytes.
func (f *Filter) UnmarshalBinary(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(data) < 4+8+8+8 {
		return errors.New("bloom: data too short")
	}
	if string(data[:4]) != "BLM1" {
		return errors.New("bloom: invalid magic")
	}

	buf := bytes.NewReader(data[4:])
	var m, k, n uint64
	if err := binary.Read(buf, binary.LittleEndian, &m); err != nil {
		return err
	}
	if err := binary.Read(buf, binary.LittleEndian, &k); err != nil {
		return err
	}
	if err := binary.Read(buf, binary.LittleEndian, &n); err != nil {
		return err
	}

	var bits []uint64
	dec := gob.NewDecoder(buf)
	if err := dec.Decode(&bits); err != nil {
		return err
	}

	f.m = uint(m)
	f.k = uint(k)
	f.n = uint(n)
	f.bits = bits
	return nil
}

// Merge combines another filter into this one.
// Both filters must have the same m and k.
func (f *Filter) Merge(other *Filter) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()

	if f.m != other.m || f.k != other.k {
		return errors.New("bloom: filters must have the same m and k to merge")
	}

	for i := range f.bits {
		f.bits[i] |= other.bits[i]
	}
	f.n += other.n
	return nil
}

// hashPair returns two independent hashes for double hashing.
func (f *Filter) hashPair(data []byte) (uint64, uint64) {
	h := fnv.New64a()
	h.Write(data)
	h1 := h.Sum64()

	h.Write([]byte{0xff})
	h2 := h.Sum64()

	return h1, h2
}

func (f *Filter) setBit(idx uint) {
	word := idx / 64
	bit := idx % 64
	if int(word) < len(f.bits) {
		f.bits[word] |= 1 << bit
	}
}

func (f *Filter) getBit(idx uint) bool {
	word := idx / 64
	bit := idx % 64
	if int(word) >= len(f.bits) {
		return false
	}
	return f.bits[word]&(1<<bit) != 0
}

// optimalM computes the optimal number of bits for a given n and fpRate.
func optimalM(n int, p float64) uint {
	return uint(math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)))
}

// optimalK computes the optimal number of hash functions for given n and m.
func optimalK(n, m uint) uint {
	k := uint(math.Round(float64(m) / float64(n) * math.Ln2))
	if k < 1 {
		return 1
	}
	return k
}

// popCount returns the number of bits set in word.
func popCount(word uint64) int {
	word = word - ((word >> 1) & 0x5555555555555555)
	word = (word & 0x3333333333333333) + ((word >> 2) & 0x3333333333333333)
	word = (word + (word >> 4)) & 0x0F0F0F0F0F0F0F0F
	return int((word * 0x0101010101010101) >> 56)
}
