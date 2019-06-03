package alg

/// The StoreEnv is the interface to external resources such as disk storage or a database,
/// possibly fronted by a redis/memcache cluster as a write-through cache.  The store/fetch
/// interface computes/uses the sha256 hash of the data item, which tend to obliterate any
/// locality information.
type StoreEnv interface {
	StoreValue(val Value, hint *Key) Hash
	FetchValue(hash Hash, hint *Key) Value

	StoreNode(n Node, hint *Key) Hash
	FetchNode(hash Hash, hint *Key) Node
	// We provide additional hint info at store, such as the Key path.  Do we want to
	// include the root hash (requires buffering and explicitly setting the root hash for
	// all entries in the buffer)?  For fetch the root hash can be set prior to any
	// FetchNode/FetchValue operations, but for StoreNode the new root hash necessarily
	// has to be computed after all the stores are done.  NB: Nodes and Value objects tend
	// to be referenced from many root hashes, so for now hint is just the Key, which may
	// be nil.

	PrefetchHint(h Hash, k Key) // Provide root hash, key to make fetches faster
}
