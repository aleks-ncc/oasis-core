package alg

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKeyBasicOps(t *testing.T) {
	assert := assert.New(t)
	k := EmptyKey()
	assert.True(k.IsEmpty())

	keyBytes := make([]byte, 32)
	for ix := 0; ix < 32; ix++ {
		keyBytes[ix] = byte(ix)
	}

	k = NewKey(keyBytes)
	DumpKey(k)
	assert.False(k.IsEmpty())

	assert.Equal(256, k.NumBits(), "Expected 256 bits, got ", k.NumBits())

	for ix := 0; ix < 8+7; ix++ {
		assert.Equal(0, k.GetBit(ix))
	}
	assert.Equal(1, k.GetBit(8+7))
	for ix := 8 + 7 + 1; ix < 8+8+6; ix++ {
		assert.Equal(0, k.GetBit(ix))
	}
	assert.Equal(1, k.GetBit(8+8+6))
	for ix := 8 + 8 + 6 + 1; ix < 8+8+8+6; ix++ {
		assert.Equal(0, k.GetBit(ix))
	}
	assert.Equal(1, k.GetBit(8+8+8+6))
	assert.Equal(1, k.GetBit(8+8+8+7))
	assert.True(k.Equals(&k))

	otherBytes := make([]byte, 32)
	for ix := 0; ix < 32; ix++ {
		otherBytes[ix] = byte(ix)
	}
	other := NewKey(otherBytes)
	assert.True(k.Equals(&other))
	assert.True(other.Equals(&k))
	other = EmptyKey()
	otherBytes[31] = byte(1)
	other = NewKey(otherBytes)
	assert.False(k.Equals(&other))
	assert.False(other.Equals(&k))
}

func TestKeyGetSet(t *testing.T) {
	assert := assert.New(t)
	kb := [...]byte{byte(0x00), byte(0x00)}
	k := NewKey(kb[:])

	k.SetBit(0, 0)
	assert.Equal(0, k.GetBit(0))

	k.SetBit(0, 1)
	assert.Equal(1, k.GetBit(0))
}

func DumpKey(k Key) {
	fmt.Printf("%9s: %d\n%9s:", "k.msbBix", k.msbBix, "k")
	for ix := 0; ix < len(k.k); ix++ {
		fmt.Printf(" %02x", k.k[ix])
	}
	fmt.Printf("\n")
}

func CheckBits(t *testing.T, bits []int, k Key, msg string) {
	assert := assert.New(t)
	assert.Equal(len(bits), k.NumBits(), msg)

	if testing.Verbose() {
		fmt.Printf("%9s:", "bits")
		for ix := 0; ix < len(bits); ix++ {
			fmt.Printf(" %d", bits[ix])
		}
		fmt.Printf("\n")
		DumpKey(k)
	}

	for ix := 0; ix < len(bits); ix++ {
		assert.Equal(bits[ix], k.GetBit(ix), msg)
	}
}

func ObviousSplitter(k Key, which int) (Key, Key) {
	return k.SplitAtObviouslyCorrect(which)
}

func FastSplitter(k Key, which int) (Key, Key) {
	return k.SplitAt(which)
}

func TestKeySplitAt(t *testing.T) {
	doTestKeySplitAtFunc(t, ObviousSplitter, "ObviousSplitter")
	doTestKeySplitAtFunc(t, FastSplitter, "FastSplitter")
}

func doTestKeySplitAtFunc(t *testing.T, splitter func(Key, int) (Key, Key), splitterName string) {
	assert := assert.New(t)
	if testing.Verbose() {
		fmt.Printf("%s\n", splitterName)
	}

	keyBytes := [...]byte{byte(0x7a), byte(0xa5)}
	k := NewKey(keyBytes[:])
	assert.Equal(16, k.NumBits())

	p, s := splitter(k, 1)
	pExpected1 := [...]int{
		0,
	}
	sExpected1 := [...]int{
		1, 1, 1,
		1, 0, 1, 0,
		1, 0, 1, 0,
		0, 1, 0, 1,
	}
	CheckBits(t, pExpected1[:], p, "p1")
	CheckBits(t, sExpected1[:], s, "s1")

	p, s = splitter(k, 2)
	pExpected2 := [...]int{
		0, 1,
	}
	sExpected2 := [...]int{
		1, 1,
		1, 0, 1, 0,
		1, 0, 1, 0,
		0, 1, 0, 1,
	}
	CheckBits(t, pExpected2[:], p, "p2")
	CheckBits(t, sExpected2[:], s, "s2")

	p, s = splitter(k, 3)
	pExpected3 := [...]int{
		0, 1, 1,
	}
	sExpected3 := [...]int{
		1,
		1, 0, 1, 0,
		1, 0, 1, 0,
		0, 1, 0, 1,
	}
	CheckBits(t, pExpected3[:], p, "p3")
	CheckBits(t, sExpected3[:], s, "s3")

	p, s = splitter(k, 8)
	pExpected8 := [...]int{
		0, 1, 1, 1,
		1, 0, 1, 0,
	}
	sExpected8 := [...]int{
		1, 0, 1, 0,
		0, 1, 0, 1,
	}
	CheckBits(t, pExpected8[:], p, "p8")
	CheckBits(t, sExpected8[:], s, "s8")

	p, s = splitter(k, 10)
	pExpected10 := [...]int{
		0, 1, 1, 1,
		1, 0, 1, 0,
		1, 0,
	}
	sExpected10 := [...]int{
		1, 0,
		0, 1, 0, 1,
	}
	CheckBits(t, pExpected10[:], p, "p10")
	CheckBits(t, sExpected10[:], s, "s10")
}
