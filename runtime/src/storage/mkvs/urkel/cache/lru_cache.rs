use std::{any::Any, cell::RefCell, collections::BTreeMap, rc::Rc, sync::Arc};

use failure::Fallible;
use io_context::Context;

use crate::{
    common::crypto::hash::Hash,
    storage::mkvs::urkel::{cache::*, sync::*, tree::*, utils::*},
};

struct LRUList<V>
where
    V: CacheItem,
{
    pub list: BTreeMap<u64, Rc<RefCell<V>>>,
    pub seq_next: u64,
    pub size: usize,
    pub capacity: usize,
}

impl<V> LRUList<V>
where
    V: CacheItem,
{
    pub fn new(capacity: usize) -> LRUList<V> {
        LRUList {
            list: BTreeMap::new(),
            seq_next: 1,
            size: 0,
            capacity: capacity,
        }
    }

    fn add_to_front(&mut self, val: Rc<RefCell<V>>) {
        let mut val_ref = val.borrow_mut();
        if val_ref.get_cache_extra() == 0 {
            self.size += val_ref.get_cached_size();
        }
        val_ref.set_cache_extra(self.seq_next);
        self.list.insert(val_ref.get_cache_extra(), val.clone());
        self.seq_next += 1;
    }

    fn move_to_front(&mut self, val: Rc<RefCell<V>>) -> bool {
        let mut val_ref = val.borrow_mut();
        if val_ref.get_cache_extra() == 0 {
            false
        } else {
            self.list.remove(&val_ref.get_cache_extra());
            val_ref.set_cache_extra(self.seq_next);
            self.list.insert(val_ref.get_cache_extra(), val.clone());
            self.seq_next += 1;
            true
        }
    }

    fn remove(&mut self, val: Rc<RefCell<V>>) -> bool {
        let extra = val.borrow().get_cache_extra();
        if extra == 0 {
            false
        } else {
            match self.list.remove(&extra) {
                None => false,
                Some(val) => {
                    let mut val = val.borrow_mut();
                    val.set_cache_extra(0);
                    self.size -= val.get_cached_size();
                    true
                }
            }
        }
    }

    fn evict_for_val(&mut self, val: Rc<RefCell<V>>) -> Vec<Rc<RefCell<V>>> {
        let mut evicted: Vec<Rc<RefCell<V>>> = Vec::new();
        if self.capacity > 0 {
            let target_size = val.borrow().get_cached_size();
            while !self.list.is_empty() && self.capacity - self.size < target_size {
                let lowest = self.list.keys().next().unwrap();
                let item = self.list.get(lowest).unwrap().clone();
                if self.remove(item.clone()) {
                    evicted.push(item);
                }
            }
        }
        evicted
    }
}

/// Cache implementation with a simple LRU eviction strategy.
pub struct LRUCache {
    read_syncer: Box<dyn ReadSync>,

    pending_root: NodePtrRef,
    sync_root: Hash,

    internal_node_count: u64,
    leaf_node_count: u64,

    prefetch_depth: u8,

    lru_values: LRUList<ValuePointer>,
    lru_nodes: LRUList<NodePointer>,
}

impl LRUCache {
    /// Construct a new cache instance.
    ///
    /// * `node_capacity` is the maximum number of nodes held by the
    ///   cache before eviction.
    /// * `value_capacity` is the total size, in bytes, of values held
    ///   by the cache before eviction.
    /// * `read_syncer` is the read syncer used as backing for the cache.
    pub fn new(
        node_capacity: usize,
        value_capacity: usize,
        read_syncer: Box<dyn ReadSync>,
    ) -> Box<LRUCache> {
        Box::new(LRUCache {
            read_syncer: read_syncer,

            pending_root: Rc::new(RefCell::new(NodePointer {
                node: None,
                ..Default::default()
            })),
            sync_root: Hash::default(),

            internal_node_count: 0,
            leaf_node_count: 0,

            prefetch_depth: 0,

            lru_values: LRUList::new(value_capacity),
            lru_nodes: LRUList::new(node_capacity),
        })
    }

    fn new_internal_node_ptr(&mut self, node: Option<NodeRef>) -> NodePtrRef {
        Rc::new(RefCell::new(NodePointer {
            node: node,
            ..Default::default()
        }))
    }

    fn new_leaf_node_ptr(&mut self, node: Option<NodeRef>) -> NodePtrRef {
        Rc::new(RefCell::new(NodePointer {
            node: node,
            ..Default::default()
        }))
    }

    fn new_value_ptr(&self, val: Value) -> ValuePtrRef {
        Rc::new(RefCell::new(ValuePointer {
            value: Some(val.clone()),
            ..Default::default()
        }))
    }

    fn use_node(&mut self, node: NodePtrRef) -> bool {
        self.lru_nodes.move_to_front(node)
    }

    fn remove_node(&mut self, ptr: NodePtrRef) {
        let mut ptr = ptr.borrow_mut();
        if ptr.is_null() {
            return;
        }
        if let Some(ref node) = ptr.node {
            match *node.borrow() {
                NodeBox::Internal(_) => {
                    self.internal_node_count -= 1;
                }
                NodeBox::Leaf(ref n) => {
                    self.remove_value(n.value.clone());
                    self.leaf_node_count -= 1;
                }
            };
            ptr.node = None;
        }
    }

    fn use_value(&mut self, val: ValuePtrRef) -> bool {
        self.lru_values.move_to_front(val)
    }

    fn _reconstruct_summary(
        &mut self,
        st: &Subtree,
        sptr: &SubtreePointer,
        depth: u8,
        max_depth: u8,
    ) -> Fallible<NodePtrRef> {
        if depth > max_depth {
            return Err(CacheError::MaximumDepthExceeded.into());
        }

        if !sptr.valid {
            return Err(CacheError::InvalidSubtreePointer.into());
        }

        if sptr.full {
            let node_ref = st.get_full_node_at(sptr.index)?;
            return match *node_ref.borrow_mut() {
                NodeBox::Internal(ref mut int) => {
                    int.clean = false;
                    Ok(self.new_internal_node_ptr(Some(node_ref.clone())))
                }
                NodeBox::Leaf(ref mut leaf) => {
                    leaf.clean = false;
                    Ok(self.new_leaf_node_ptr(Some(node_ref.clone())))
                }
            };
        } else {
            let summary = st.get_summary_at(sptr.index)?;
            return match summary {
                None => Ok(NodePointer::null_ptr()),
                Some(summary) => {
                    let left =
                        self._reconstruct_summary(st, &summary.left, depth + 1, max_depth)?;
                    let right =
                        self._reconstruct_summary(st, &summary.right, depth + 1, max_depth)?;
                    Ok(self.new_internal_node(left, right))
                }
            };
        }
    }
}

impl Cache for LRUCache {
    fn as_any(&self) -> &dyn Any {
        self
    }

    fn stats(&self) -> CacheStats {
        CacheStats {
            internal_node_count: self.internal_node_count,
            leaf_node_count: self.leaf_node_count,
            leaf_value_size: self.lru_values.size,
        }
    }

    fn get_pending_root(&self) -> NodePtrRef {
        self.pending_root.clone()
    }

    fn set_pending_root(&mut self, new_root: NodePtrRef) {
        self.pending_root = new_root.clone();
    }

    fn set_sync_root(&mut self, new_hash: Hash) {
        self.sync_root = new_hash;
    }

    fn set_prefetch_depth(&mut self, depth: u8) {
        self.prefetch_depth = depth;
    }

    fn get_read_syncer(&self) -> &Box<dyn ReadSync> {
        &self.read_syncer
    }

    fn new_internal_node(&mut self, left: NodePtrRef, right: NodePtrRef) -> NodePtrRef {
        let node = Rc::new(RefCell::new(NodeBox::Internal(InternalNode {
            left: left,
            right: right,
            ..Default::default()
        })));
        self.new_internal_node_ptr(Some(node))
    }

    fn new_leaf_node(&mut self, key: Hash, val: Value) -> NodePtrRef {
        let node = Rc::new(RefCell::new(NodeBox::Leaf(LeafNode {
            key: key.clone(),
            value: self.new_value(val),
            ..Default::default()
        })));
        self.new_leaf_node_ptr(Some(node))
    }

    fn new_value(&mut self, val: Value) -> ValuePtrRef {
        self.new_value_ptr(val)
    }

    fn try_remove_node(&mut self, ptr: NodePtrRef) {
        if ptr.borrow().get_cache_extra() == 0 {
            return;
        }

        if let Some(ref node_ref) = ptr.borrow().node {
            if let NodeBox::Internal(ref n) = *node_ref.borrow() {
                // We can only remove internal nodes if they have no cached children
                // as otherwise we would need to remove the whole subtree.
                if n.left.borrow().has_node() || n.right.borrow().has_node() {
                    return;
                }
            }
        }

        if self.lru_nodes.remove(ptr.clone()) {
            self.remove_node(ptr);
        }
    }

    fn remove_value(&mut self, ptr: ValuePtrRef) {
        self.lru_values.remove(ptr);
    }

    fn deref_node_id(&mut self, ctx: &Arc<Context>, node_id: NodeID) -> Fallible<NodePtrRef> {
        let mut cur_ptr = self.pending_root.clone();
        for d in 0..node_id.depth {
            let node = self.deref_node_ptr(ctx, node_id.at_depth(d), cur_ptr.clone(), None)?;
            let node = match node {
                None => return Ok(NodePointer::null_ptr()),
                Some(node) => node,
            };

            if let NodeBox::Internal(ref n) = *node.borrow() {
                if get_key_bit(&node_id.path, d) {
                    cur_ptr = n.right.clone();
                } else {
                    cur_ptr = n.left.clone();
                }
            };
        }
        Ok(cur_ptr)
    }

    fn deref_node_ptr(
        &mut self,
        ctx: &Arc<Context>,
        node_id: NodeID,
        ptr: NodePtrRef,
        key: Option<Hash>,
    ) -> Fallible<Option<NodeRef>> {
        let mut ptr = ptr.borrow_mut();
        if let Some(ref node) = &ptr.node {
            return Ok(Some(node.clone()));
        }
        if !ptr.clean || ptr.is_null() {
            return Ok(None);
        }

        match key {
            None => {
                let node_ref = self.read_syncer.get_node(
                    Context::create_child(ctx),
                    self.sync_root,
                    node_id,
                )?;
                node_ref.borrow_mut().validate(ptr.hash)?;
                ptr.node = Some(node_ref.clone());
            }
            Some(key) => {
                let subtree = self.read_syncer.get_path(
                    Context::create_child(ctx),
                    self.sync_root,
                    key,
                    node_id.depth,
                )?;
                let new_ptr = self.reconstruct_subtree(
                    ctx,
                    ptr.hash,
                    &subtree,
                    node_id.depth,
                    (8 * Hash::len() - 1) as u8,
                )?;
                let new_ptr = new_ptr.borrow();
                ptr.clean = new_ptr.clean;
                ptr.hash = new_ptr.hash;
                ptr.node = new_ptr.node.clone();
            }
        };

        Ok(ptr.node.clone())
    }

    fn deref_value_ptr(&mut self, ctx: &Arc<Context>, val: ValuePtrRef) -> Fallible<Option<Value>> {
        if self.use_value(val.clone()) || val.borrow().value != None {
            return Ok(val.borrow().value.clone());
        }

        {
            let mut val = val.borrow_mut();
            if !val.clean {
                return Ok(None);
            }

            let value =
                self.read_syncer
                    .get_value(Context::create_child(ctx), self.sync_root, val.hash)?;
            val.value = value;
            let hash = val.hash;
            val.validate(hash)?;
        }
        self.commit_value(val.clone());

        Ok(val.borrow().value.clone())
    }

    fn commit_node(&mut self, ptr: NodePtrRef) {
        if !ptr.borrow().clean {
            panic!("urkel: commit_node called on dirty node");
        }
        if ptr.borrow().node.is_none() {
            return;
        }
        if self.use_node(ptr.clone()) {
            return;
        }

        for node in self.lru_nodes.evict_for_val(ptr.clone()).iter() {
            self.remove_node(node.clone());
        }
        self.lru_nodes.add_to_front(ptr.clone());

        if let Some(ref some_node) = ptr.borrow().node {
            match *some_node.borrow() {
                NodeBox::Internal(_) => self.internal_node_count += 1,
                NodeBox::Leaf(_) => self.leaf_node_count += 1,
            };
        }
    }

    fn commit_value(&mut self, ptr: ValuePtrRef) {
        if !ptr.borrow().clean {
            panic!("urkel: commit_value called on dirty value");
        }
        if self.use_value(ptr.clone()) {
            return;
        }
        if let None = ptr.borrow().value {
            return;
        }

        self.lru_values.evict_for_val(ptr.clone());
        self.lru_values.add_to_front(ptr.clone());
    }

    fn reconstruct_subtree(
        &mut self,
        ctx: &Arc<Context>,
        root: Hash,
        st: &Subtree,
        depth: u8,
        max_depth: u8,
    ) -> Fallible<NodePtrRef> {
        let ptr = self._reconstruct_summary(st, &st.root, depth, max_depth)?;
        if ptr.borrow().is_null() {
            return Err(CacheError::ReconstructedRootNil.into());
        }

        let mut update_list: UpdateList<LRUCache> = UpdateList::new();
        let new_root = _commit(ctx, ptr.clone(), &mut update_list)?;
        if new_root != root {
            Err(CacheError::SyncerBadRoot {
                expected_root: root,
                returned_root: new_root,
            }
            .into())
        } else {
            update_list.commit(self);
            Ok(ptr)
        }
    }

    fn prefetch(
        &mut self,
        ctx: &Arc<Context>,
        subtree_root: Hash,
        subtree_path: Hash,
        depth: u8,
    ) -> Fallible<NodePtrRef> {
        if self.prefetch_depth == 0 {
            return Ok(NodePointer::null_ptr());
        }

        let result = self.read_syncer.get_subtree(
            Context::create_child(ctx),
            self.sync_root,
            NodeID {
                path: subtree_path,
                depth: depth,
            },
            self.prefetch_depth,
        );

        let st = match result {
            Err(err) => {
                if let Some(sync_err) = err.downcast_ref::<SyncerError>() {
                    if let SyncerError::Unsupported = sync_err {
                        return Ok(NodePointer::null_ptr());
                    }
                }
                return Err(err);
            }
            Ok(ref st) => st,
        };
        self.reconstruct_subtree(ctx, subtree_root, st, 0, self.prefetch_depth)
    }
}
