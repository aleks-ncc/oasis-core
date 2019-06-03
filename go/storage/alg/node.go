package alg

import (
	"bytes"
	"crypto/sha256"
)

type Hash []byte
type Value []byte

type Node interface {
	// l, r, v accessors loads the Node or Value from the storage environment via StoreEnv
	// if the weak pointers are non-null (see Hash.IsNull method).
	l(StoreEnv) Node
	r(StoreEnv) Node
	v(StoreEnv) Value
	compressedKey() Key

	lh() Hash // accessors; does not fetch
	rh() Hash
	vh() Hash // value hash

	HashValue() Hash
}

// The nullHash value should be a constant.  Unfortunately, Golang does not (yet) have a way to
// specify that the contents of an array should go into .rodata and actually be immutable with
// hardware detecting attempts to write to it.  We use nullHash by value as with all Hash
// objects, so this shouldn't leak to client code.  It may be better to use a factory function
// that creates new instances -- and pay the memory overhead -- to prevent client code from
// modifying the null value and affect all other uses, or to make the Hash type be more opaque,
// so that client code only gets a read-only view.
var nullHash Hash = make([]byte, sha256.Size)

type LazyNode struct {
	// Immutable values.
	lhash, vhash, rhash Hash
	compressed          Key // empty if no additional key bits should be consumed

	// These are "weak references".  Interior mutability.
	value  Value // May be nil.
	lp, rp *LazyNode // May be nil.
}

var nullNode LazyNode = LazyNode{nullHash, nullHash, nullHash, EmptyKey(), nil, nil, nil}
var emptyKey Key = EmptyKey()

// No need for a constant time version, since hash values are public.
func (h Hash) IsNull() bool {
	for i := 0; i < sha256.Size; i++ {
		if h[i] != 0x0 {
			return false
		}
	}
	return true
}

func (h Hash) Equal(other Hash) bool {
	return bytes.Equal(h, other)
}

func NewLazyNode(env StoreEnv, k Key, lh Hash, val Value, rh Hash, hint *Key) *LazyNode {
	return &LazyNode{
		lhash:      lh,
		vhash:      env.StoreValue(val, hint),
		rhash:      rh,
		compressed: k,
		value:      val,
		lp:         nil,
		rp:         nil,
	}
}

func NewLazyNodeWithWeakRefs(env StoreEnv, k Key, v Value, lh, vh, rh Hash, lp, rp *LazyNode) *LazyNode {
	return &LazyNode{
		lhash:      lh,
		vhash:      vh,
		rhash:      rh,
		compressed: k,
		value:      v,
		lp:         lp,
		rp:         rp,
	}
}

func (n LazyNode) l(env StoreEnv) Node {
	if n.lp == nil && !n.lhash.IsNull() {
		n.lp = env.FetchNode(n.lhash, nil).(*LazyNode)
	}
	return n.lp
}

func (n LazyNode) r(env StoreEnv) Node {
	if n.rp == nil && !n.rhash.IsNull() {
		n.rp = env.FetchNode(n.rhash, nil).(*LazyNode)
	}
	return n.rp
}

func (n LazyNode) v(env StoreEnv) Value {
	if len(n.value) == 0 && n.vhash.IsNull() {
		n.value = env.FetchValue(n.vhash, nil)
	}
	return n.value
}

func (n LazyNode) compressedKey() Key {
	return n.compressed
}

func (n LazyNode) lh() Hash {
	return n.lhash
}

func (n LazyNode) rh() Hash {
	return n.rhash
}

func (n LazyNode) vh() Hash {
	return n.vhash
}

// Used by proof verifier
func HashNodeData(lh, vh, rh Hash, compressed Key) Hash {
	hasher := sha256.New()
	hasher.Write(lh)
	hasher.Write(vh)
	pfx, sfx := compressed.HashData()
	hasher.Write(pfx)
	hasher.Write(sfx)
	return hasher.Sum(rh)
}

func (n LazyNode) HashValue() Hash {
	return HashNodeData(n.lh(), n.vh(), n.rh(), n.compressedKey())
}

func (n *LazyNode) InsertRecursive(
	env StoreEnv, k Key, v Value, hint *Key, pf *WriteProof,
) *LazyNode {
	if n.compressed.IsEmpty() {
		// recursive l or r if k is non-empty; or replace vhash and value if k is empty
		// i.e., we are at the target location.
		if k.IsEmpty() {
			// use lh(), lhash, rh(), and rhash -- do not use l() or r() since we
			// do not want to actually fetch the nodes.
			pf.Append(n.lhash, n.rhash, emptyKey)
			pf.SetOrigValueHash(n.vhash) // old value to verify peer hash with start state
			nvh := env.StoreValue(v, hint)
			return NewLazyNodeWithWeakRefs(env, emptyKey, v, n.lhash, nvh, n.rhash, n.lp, n.rp)
		}
		msb, kprime := k.MSBAndDerive()
		if msb == 0 {
			// left
			pf.Append(n.vhash, n.lhash, emptyKey)

			var newLeft *LazyNode
			if n.lh().IsNull() {
				newLeft = NewLazyNode(env, kprime, nullHash, v, nullHash, hint)
			} else {
				newLeft = n.l(env).(*LazyNode).InsertRecursive(env, kprime, v, hint, pf)
			}
			nlh := newLeft.HashValue()
			return NewLazyNodeWithWeakRefs(
				env, emptyKey, v,
				nlh, n.vhash, n.rhash,
				newLeft, n.rp)
		} else {
			// right
			pf.Append(n.lhash, n.vhash, emptyKey)

			var newRight *LazyNode
			if n.rh().IsNull() {
				newRight = NewLazyNode(env, kprime, nullHash, v, nullHash, hint)
			} else {
				newRight = n.r(env).(*LazyNode).InsertRecursive(env, kprime, v, hint, pf)
			}
			nrh := newRight.HashValue()
			return NewLazyNodeWithWeakRefs(
				env, emptyKey, v,
				n.lhash, n.vhash, nrh,
				n.lp, newRight)
		}
	}
	// ...TODO...
	return &nullNode
}
