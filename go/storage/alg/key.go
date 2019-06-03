package alg

// Key object for looking up entries in the authenticated data structure.  Keys are arbitrary
// bit strings, stored so that the lsb is in the lowest order bit of the last byte in the
// slice.  This is consistent with encoding something like a file path as the key.  If
// arithmetic is done with keys for a (semi) linear mapping of aggregate objects to keys, that
// would change the lower-order bits (toward the end of the slice).

// We access/traverse msb first, so use a descriptor to allow sharing the underlying slice
// while allowing different views of the key where high-order bits have been "consumed" while
// traversing the ADT.  We number the key bits with the msb as 0, in big bit-endian order.
//
// We must expose the key bits for hashing.  This is done by slicing away the "fully used"
// bytes at the beginning of the array, so we always return a pair of msb []byte, rest []byte,
// where the msb slice contains the number of bits in the key along with a single byte with
// "used" bits masked off, so the hasher can hash the two return values.  If k1.Equals(k2),
// then the contents of the returned slice pairs will be identical.

type Key struct {
	k      []byte
	msbBix int // bit indices into k.
}

const MaxInt = int(^uint(0) >> 1)

/// NewKey creates a key descriptor to wrap externalKey.  It is the responsibilty of the caller
/// to ensure that externalKey is effectively immutable while any key descriptor is still live,
/// or to make a copy.
///
/// Pre: len(externalKey) <= MaxInt/8.  The math package does not have a MaxInt constant.
func NewKey(externalKey []byte) Key {
	nbytes := len(externalKey)
	if nbytes > MaxInt/8 {
		panic("crazy large key")
	}
	// post: comparisons between msbBix and 8*len(k) will not suffer from overflow
	return Key{k: externalKey, msbBix: 0}
}

func EmptyKey() Key {
	return Key{k: nil, msbBix: 0}
}

/// Clone creates a copy of the key descriptor.  The underlying "immutable" slice containing
/// the key bits are shared.
func (k *Key) Clone() Key {
	return Key{k: k.k, msbBix: k.msbBix}
}

func (k *Key) NumBits() int {
	return int(8*len(k.k)) - k.msbBix
}

func (k *Key) HashData() ([]byte, []byte) {
	nb := k.NumBits()
	// assumes sizeof(uint) <= 16
	s := make([]byte, 17)
	for ix := uint(0); ix < 16; ix++ {
		s[ix] = byte(nb >> (8 * (16 - 1 - ix)))
	}
	msbPos := k.msbBix / 8
	s[16] = k.k[msbPos] & byte((uint(1)<<(uint(8-(k.msbBix%8))))-1)
	return s, k.k[msbPos+1:]
}

func (k *Key) IsEmpty() bool {
	return k.NumBits() == 0 // msbBix == 8 * len(k.k)
}

func (k *Key) MSB() int {
	if k.IsEmpty() {
		panic("Empty key msb()")
	}
	return int((k.k[k.msbBix/8] >> uint(7-(k.msbBix%8))) & 0x1)
}

func (k *Key) GetBit(bix int) int {
	if bix < 0 || k.NumBits() <= bix {
		panic("GetBit out of range")
	}
	// bix < 8*len(k.k) - k.msbBix ⇒ k.msbBiix + bix < 8*len(k.k)
	kix := k.msbBix + bix
	bit := (k.k[kix/8] >> uint(7-(kix%8))) & 0x1
	return int(bit)
}

// Used for the obvious SplitAt implementation.
func (k *Key) SetBit(bix int, v int) {
	if bix < 0 || k.NumBits() <= bix {
		panic("SetBit out of range")
	}
	if v != 0 && v != 1 {
		panic("SetBit bit value is not 0 or 1")
	}
	// bix < k.NumBits() ⇔ bix < 8*len(k.k) - k.msbBix ⇒ k.msbBix + bix < 8*len(k.k)
	kix := k.msbBix + bix
	off := kix / 8
	bitpos := uint(7 - (kix % 8))
	k.k[off] = (k.k[off] &^ byte(1<<bitpos)) | byte(v<<bitpos)
}

// Used for the obvious SplitAt implementation.
func (k *Key) DropBits(count int) {
	if count < 0 || k.NumBits() < count {
		panic("DropBits count invalid")
	}
	k.msbBix += count
}

func (k *Key) MSBAndDerive() (int, Key) {
	b := k.MSB()
	return b, Key{k: k.k, msbBix: k.msbBix + 1}
}

func (k *Key) Equals(other *Key) bool {
	nb := k.NumBits()
	if nb != other.NumBits() {
		return false
	}
	// This is ripe for optimization, since shift-compare can compare multiple bits at a
	// time.
	for ix := 0; ix < nb; ix++ {
		if k.GetBit(ix) != other.GetBit(ix) {
			return false
		}
	}
	return true
}

func (k *Key) IsPrefixOf(other *Key) bool {
	kbits := k.NumBits()
	if kbits > other.NumBits() {
		return false
	}
	for ix := 0; ix < kbits; ix++ {
		if k.GetBit(ix) != other.GetBit(ix) {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		b = a
	}
	return b
}

// 3-way compare, useful for sorting, plus distance down the key path to the differing bit
func (k *Key) DiffAt(other *Key) (cmpResult int, bitPos int) {
	nkb := k.NumBits()
	nob := other.NumBits()
	lim := min(nkb, nob)
	for ix := 0; ix < lim; ix++ {
		kb := k.GetBit(ix)
		ob := other.GetBit(ix)
		if kb != ob {
			if kb == 0 {
				return -1, ix
			} else {
				return 1, ix
			}
		}
	}
	// shorter key is between extension by 0 and extension by 1
	if nkb < nob {
		if other.GetBit(nkb) == 0 {
			return 1, nkb
		} else {
			return -1, nkb
		}
	} else if nkb > nob {
		if k.GetBit(nob) == 0 {
			return -1, nob
		} else {
			return 1, nob
		}
	}
	return 0, nkb
}

func (k *Key) cmp(other *Key) int {
	d, _ := k.DiffAt(other)
	return d
}

func (k *Key) SplitAtObviouslyCorrect(ix int) (pfx, sfx Key) {
	// Clone k, make a slice for ix bits, and use GetBit to populate the slice, and then
	// DropBits on the clone to create the sfx.
	if ix < 0 || k.NumBits() < ix {
		panic("SplitAt bit index negative or exceeds number of bits in key")
	}
	nbytes := (ix + 7) / 8
	pfx = Key{k: make([]byte, nbytes), msbBix: 8*nbytes - ix}
	sfx = k.Clone()
	for jx := 0; jx < ix; jx++ {
		b := sfx.GetBit(jx)
		pfx.SetBit(jx, b)
	}
	sfx.DropBits(ix)
	return pfx, sfx
}

func (k *Key) SplitAt(ix int) (pfx, sfx Key) {
	if ix < 0 || k.NumBits() < ix {
		panic("SplitAt bit index negative or exceeds number of bits in key")
	}
	splitBix := ix + k.msbBix

	if splitBix%8 == 0 {
		// split bit position is byte aligned, no shift-copying needed
		pfx = Key{k: k.k[:splitBix/8], msbBix: k.msbBix}
		sfx = Key{k: k.k, msbBix: splitBix}
		return
	}

	nb := (ix + 7) / 8 // round up
	pfxK := make([]byte, nb)

	boundary := int(splitBix / 8)
	numHiBits := uint(splitBix % 8)
	numLowBits := uint(8 - numHiBits)
	partial := k.k[boundary] >> numLowBits
	jx := boundary - 1

	for kx := nb; kx > 0; {
		kx--
		b := byte(0)
		if 0 <= jx {
			b = k.k[jx]
		}
		partial = partial | byte(b<<numHiBits)
		pfxK[kx] = partial
		partial = b >> numLowBits
		jx--
	}
	pfx = Key{k: pfxK, msbBix: 8*nb - ix}
	sfx = Key{k: k.k, msbBix: splitBix}
	return pfx, sfx
}
