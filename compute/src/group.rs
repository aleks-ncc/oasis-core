//! Computation group structures.
use std::sync::{Arc, Mutex};

use ekiden_compute_api::{ComputationGroupClient, SubmitAggCommitRequest, SubmitAggRevealRequest,
                         SubmitBatchRequest};
use ekiden_core::bytes::{B256, B64, H256};
use ekiden_core::environment::Environment;
use ekiden_core::error::{Error, Result};
use ekiden_core::futures::prelude::*;
use ekiden_core::futures::sync::mpsc;
use ekiden_core::node::Node;
use ekiden_core::node_group::NodeGroup;
use ekiden_core::signature::{Signed, Signer};
use ekiden_core::subscribers::StreamSubscribers;
use ekiden_registry_base::EntityRegistryBackend;
use ekiden_scheduler_base::{CommitteeNode, CommitteeType, Role, Scheduler};

use ekiden_consensus_base::{Commitment, Reveal};

/// Signature context used for batch submission.
const SUBMIT_BATCH_SIGNATURE_CONTEXT: B64 = B64(*b"EkCgBaSu");

/// Signature context used for submitting a commit to leader for aggregation.
const SUBMIT_AGG_COMMIT_SIGNATURE_CONTEXT: B64 = B64(*b"EkCgACSu");

/// Signature context used for submitting a reveal to leader for aggregation.
const SUBMIT_AGG_REVEAL_SIGNATURE_CONTEXT: B64 = B64(*b"EkCgARSu");

/// Commands for communicating with the computation group from other tasks.
enum Command {
    /// Submit batch to workers.
    Submit(H256),
    /// Update committee.
    UpdateCommittee(Vec<CommitteeNode>),
    /// Submit a commit to the leader for aggregation.
    SubmitAggCommit(Commitment),
    /// Submit a reveal to the leader for aggregation.
    SubmitAggReveal(Reveal),
}

struct Inner {
    /// Contract identifier the computation group is for.
    contract_id: B256,
    /// Scheduler.
    scheduler: Arc<Scheduler>,
    /// Entity registry.
    entity_registry: Arc<EntityRegistryBackend>,
    /// Computation node group.
    node_group: NodeGroup<ComputationGroupClient, CommitteeNode>,
    /// Computation committee metadata.
    committee: Mutex<Vec<CommitteeNode>>,
    /// Signer for the compute node.
    signer: Arc<Signer>,
    /// Current leader of the computation committee.
    leader: Arc<Mutex<Option<CommitteeNode>>>,
    /// Environment.
    environment: Arc<Environment>,
    /// Command sender.
    command_sender: mpsc::UnboundedSender<Command>,
    /// Command receiver (until initialized).
    command_receiver: Mutex<Option<mpsc::UnboundedReceiver<Command>>>,
    /// Role subscribers.
    role_subscribers: StreamSubscribers<Option<Role>>,
}

impl Inner {
    /// Get local node's role in the committee.
    ///
    /// May be `None` in case the local node is not part of the computation group.
    fn get_role(&self) -> Option<Role> {
        let committee = self.committee.lock().unwrap();
        committee
            .iter()
            .filter(|node| node.public_key == self.signer.get_public_key())
            .map(|node| node.role.clone())
            .next()
    }
}

/// Structure that maintains connections to the current compute committee.
pub struct ComputationGroup {
    inner: Arc<Inner>,
}

impl ComputationGroup {
    /// Create new computation group.
    pub fn new(
        contract_id: B256,
        scheduler: Arc<Scheduler>,
        entity_registry: Arc<EntityRegistryBackend>,
        signer: Arc<Signer>,
        environment: Arc<Environment>,
    ) -> Self {
        let (command_sender, command_receiver) = mpsc::unbounded();

        let instance = Self {
            inner: Arc::new(Inner {
                contract_id,
                scheduler,
                entity_registry,
                node_group: NodeGroup::new(),
                committee: Mutex::new(vec![]),
                signer,
                leader: Arc::new(Mutex::new(None)),
                environment,
                command_sender,
                command_receiver: Mutex::new(Some(command_receiver)),
                role_subscribers: StreamSubscribers::new(),
            }),
        };
        instance.start();

        instance
    }

    /// Start computation group tasks.
    pub fn start(&self) {
        info!("Starting computation group services");

        let mut event_sources = stream::SelectAll::new();

        // Subscribe to computation group formations for given contract and update nodes.
        let contract_id = self.inner.contract_id;
        event_sources.push(
            self.inner
                .scheduler
                .watch_committees()
                .filter(|committee| committee.kind == CommitteeType::Compute)
                .filter(move |committee| committee.contract.id == contract_id)
                .map(|committee| Command::UpdateCommittee(committee.members))
                .into_box(),
        );

        // Receive commands.
        let command_receiver = self.inner
            .command_receiver
            .lock()
            .unwrap()
            .take()
            .expect("start already called");
        event_sources.push(
            command_receiver
                .map_err(|_| Error::new("command channel closed"))
                .into_box(),
        );

        // Process commands.
        self.inner.environment.spawn({
            let inner = self.inner.clone();

            event_sources.for_each_log_errors(
                module_path!(),
                "Unexpected error while processing group commands",
                move |command| match command {
                    Command::Submit(batch_hash) => Self::handle_submit(inner.clone(), batch_hash),
                    Command::UpdateCommittee(members) => {
                        measure_counter_inc!("committee_updates_count");

                        Self::handle_update_committee(inner.clone(), members)
                    }
                    Command::SubmitAggCommit(commit) => {
                        Self::handle_submit_agg_commit(inner.clone(), commit)
                    }
                    Command::SubmitAggReveal(reveal) => {
                        Self::handle_submit_agg_reveal(inner.clone(), reveal)
                    }
                },
            )
        });
    }

    /// Handle committee update.
    fn handle_update_committee(inner: Arc<Inner>, members: Vec<CommitteeNode>) -> BoxFuture<()> {
        info!("Starting update of computation group committee");

        // Clear previous group.
        {
            let mut committee = inner.committee.lock().unwrap();
            if *committee == members {
                info!("Not updating committee as membership has not changed");
                return future::ok(()).into_box();
            }

            committee.clear();
        }
        inner.node_group.clear();

        // Clear the current leader as well.
        *inner.leader.lock().unwrap() = None;

        // Check if we are still part of the committee. If we are not, do not populate the node
        // group with any nodes as it is not needed.
        if !members
            .iter()
            .any(|node| node.public_key == inner.signer.get_public_key())
        {
            info!("No longer a member of the computation group");
            inner.role_subscribers.notify(&None);
            return Box::new(future::ok(()));
        }

        // Find new leader.
        *inner.leader.lock().unwrap() = members
            .iter()
            .find(|node| node.role == Role::Leader)
            .cloned();

        // Resolve nodes via the entity registry.
        // TODO: Support group fetch to avoid multiple requests to registry or make scheduler return nodes.
        let nodes: Vec<BoxFuture<(Node, CommitteeNode)>> = members
            .iter()
            .filter(|node| node.public_key != inner.signer.get_public_key())
            .filter(|node| node.role == Role::Worker || node.role == Role::Leader)
            .map(|node| {
                let node = node.clone();

                inner
                    .entity_registry
                    .get_node(node.public_key)
                    .and_then(move |reg_node| Ok((reg_node, node.clone())))
                    .into_box()
            })
            .collect();

        Box::new(
            future::join_all(nodes)
                .and_then(move |nodes| {
                    // Update group.
                    for (node, committee_node) in nodes {
                        let channel = node.connect(inner.environment.grpc());
                        let client = ComputationGroupClient::new(channel);
                        inner.node_group.add_node(client, committee_node);
                    }

                    trace!("New committee: {:?}", members);

                    let old_role = inner.get_role();

                    // Update current committee.
                    {
                        let mut committee = inner.committee.lock().unwrap();
                        *committee = members;
                    }

                    let new_role = inner.get_role();
                    if new_role != old_role {
                        info!("Our new role is: {:?}", &new_role.unwrap());
                        inner.role_subscribers.notify(&new_role);
                    }

                    info!("Update of computation group committee finished");

                    Ok(())
                })
                .or_else(|error| {
                    error!(
                        "Failed to resolve computation group from registry: {}",
                        error.message
                    );
                    Ok(())
                }),
        )
    }

    /// Handle batch submission.
    fn handle_submit(inner: Arc<Inner>, batch_hash: H256) -> BoxFuture<()> {
        trace!("Submitting batch to workers");

        // Sign batch.
        let signed_batch = Signed::sign(&inner.signer, &SUBMIT_BATCH_SIGNATURE_CONTEXT, batch_hash);

        // Submit batch.
        let mut request = SubmitBatchRequest::new();
        request.set_batch_hash(batch_hash.to_vec());
        request.set_signature(signed_batch.signature.into());

        inner
            .node_group
            .call_filtered(
                |_, node| node.role == Role::Worker,
                move |client, _| client.submit_batch_async(&request),
            )
            .and_then(|results| {
                for result in results {
                    if let Err(error) = result {
                        error!("Failed to submit batch to node: {}", error.message);
                    }
                }

                Ok(())
            })
            .into_box()
    }

    /// Handle submission of a single commit to the leader for aggregation.
    fn handle_submit_agg_commit(inner: Arc<Inner>, commit: Commitment) -> BoxFuture<()> {
        trace!("Submitting aggregate commit to leader");

        // Sign commit.
        let signed_commit = Signed::sign(
            &inner.signer,
            &SUBMIT_AGG_COMMIT_SIGNATURE_CONTEXT,
            commit.clone(),
        );

        // Submit commit.
        let mut request = SubmitAggCommitRequest::new();
        request.set_commit(commit.into());
        request.set_signature(signed_commit.signature.into());

        inner
            .node_group
            .call_filtered(
                |_, node| node.role == Role::Leader,
                move |client, _| client.submit_agg_commit_async(&request),
            )
            .and_then(|results| {
                trace!("Aggregate commit submitted successfully!");

                for result in results {
                    if let Err(error) = result {
                        error!(
                            "Failed to submit aggregate commit to node: {}",
                            error.message
                        );
                    }
                }

                Ok(())
            })
            .into_box()
    }

    /// Handle submission of a single reveal to the leader for aggregation.
    fn handle_submit_agg_reveal(inner: Arc<Inner>, reveal: Reveal) -> BoxFuture<()> {
        trace!("Submitting aggregate reveal to leader");

        // Sign reveal.
        let signed_reveal = Signed::sign(
            &inner.signer,
            &SUBMIT_AGG_REVEAL_SIGNATURE_CONTEXT,
            reveal.clone(),
        );

        // Submit reveal.
        let mut request = SubmitAggRevealRequest::new();
        request.set_reveal(reveal.into());
        request.set_signature(signed_reveal.signature.into());

        inner
            .node_group
            .call_filtered(
                |_, node| node.role == Role::Leader,
                move |client, _| client.submit_agg_reveal_async(&request),
            )
            .and_then(|results| {
                trace!("Aggregate reveal submitted successfully!");

                for result in results {
                    if let Err(error) = result {
                        error!(
                            "Failed to submit aggregate reveal to node: {}",
                            error.message
                        );
                    }
                }

                Ok(())
            })
            .into_box()
    }

    /// Submit batch to workers in the computation group.
    pub fn submit(&self, batch_hash: H256) -> Vec<CommitteeNode> {
        self.inner
            .command_sender
            .unbounded_send(Command::Submit(batch_hash))
            .unwrap();

        let committee = self.inner.committee.lock().unwrap();
        committee.clone()
    }

    /// Submit a commit to the leader for aggregation.
    ///
    /// Returns the current leader of the computation group.
    pub fn submit_agg_commit(&self, commit: Commitment) -> CommitteeNode {
        self.inner
            .command_sender
            .unbounded_send(Command::SubmitAggCommit(commit))
            .unwrap();

        self.inner.leader.lock().unwrap().clone().unwrap()
    }

    /// Submit a reveal to the leader for aggregation.
    ///
    /// Returns the current leader of the computation group.
    pub fn submit_agg_reveal(&self, reveal: Reveal) -> CommitteeNode {
        self.inner
            .command_sender
            .unbounded_send(Command::SubmitAggReveal(reveal))
            .unwrap();

        self.inner.leader.lock().unwrap().clone().unwrap()
    }

    /// Verify that given batch has been signed by the current leader.
    ///
    /// Returns the call batch and the current compute committee.
    pub fn open_remote_batch(
        &self,
        batch_hash: Signed<H256>,
    ) -> Result<(H256, Vec<CommitteeNode>)> {
        // Check if batch was signed by leader, drop otherwise.
        let committee = {
            let committee = self.inner.committee.lock().unwrap();
            if !committee.iter().any(|node| {
                node.role == Role::Leader && node.public_key == batch_hash.signature.public_key
            }) {
                warn!("Dropping call batch not signed by compute committee leader");
                return Err(Error::new("not signed by compute committee leader"));
            }

            committee.clone()
        };

        Ok((batch_hash.open(&SUBMIT_BATCH_SIGNATURE_CONTEXT)?, committee))
    }

    pub fn open_agg_commit(&self, signed_commit: Signed<Commitment>) -> Result<(Commitment, Role)> {
        // Check if commitment was signed by a worker and that we're the
        // current leader, drop otherwise.  Also note that the leader and
        // backup workers also count as workers.
        let leader = self.inner.leader.lock().unwrap().clone().unwrap();

        if leader.public_key != self.inner.signer.get_public_key() {
            warn!("Dropping commit for aggregation, as we're not the current compute committee leader");
            return Err(Error::new("am not the current compute committee leader"));
        }

        let committee = self.inner.committee.lock().unwrap();

        // Find the node that signed this commitment.
        let node = committee
            .iter()
            .find(|node| node.public_key == signed_commit.signature.public_key);

        if node == None {
            warn!("Dropping commit for aggregation, as it was not signed by any node");
            return Err(Error::new("not signed by any node"));
        }

        // Get the role of the node that signed this commitment.
        let role = node.unwrap().role;

        if role != Role::Worker && role != Role::BackupWorker && role != Role::Leader {
            warn!(
                "Dropping commit for aggregation, as it was not signed by compute committee worker"
            );
            return Err(Error::new("not signed by compute committee worker"));
        }

        Ok((
            signed_commit.open(&SUBMIT_AGG_COMMIT_SIGNATURE_CONTEXT)?,
            role,
        ))
    }

    pub fn open_agg_reveal(&self, signed_reveal: Signed<Reveal>) -> Result<(Reveal, Role)> {
        // Check if reveal was signed by a worker and that we're the
        // current leader, drop otherwise.  Also note that the leader and
        // backup workers also count as workers.
        let leader = self.inner.leader.lock().unwrap().clone().unwrap();

        if leader.public_key != self.inner.signer.get_public_key() {
            warn!("Dropping reveal for aggregation, as we're not the current compute committee leader");
            return Err(Error::new("am not the current compute committee leader"));
        }

        let committee = self.inner.committee.lock().unwrap();

        // Find the node that signed this reveal.
        let node = committee
            .iter()
            .find(|node| node.public_key == signed_reveal.signature.public_key);

        if node == None {
            warn!("Dropping reveal for aggregation, as it was not signed by any node");
            return Err(Error::new("not signed by any node"));
        }

        // Get the role of the node that signed this reveal.
        let role = node.unwrap().role;

        if role != Role::Worker && role != Role::BackupWorker && role != Role::Leader {
            warn!(
                "Dropping reveal for aggregation, as it was not signed by compute committee worker"
            );
            return Err(Error::new("not signed by compute committee worker"));
        }

        Ok((
            signed_reveal.open(&SUBMIT_AGG_REVEAL_SIGNATURE_CONTEXT)?,
            role,
        ))
    }

    /// Subscribe to notifications on our current role in the computation committee.
    pub fn watch_role(&self) -> BoxStream<Option<Role>> {
        self.inner.role_subscribers.subscribe().1
    }

    /// Get current committee.
    pub fn get_committee(&self) -> Vec<CommitteeNode> {
        self.inner.committee.lock().unwrap().clone()
    }

    /// Get number of workers (+ leader!) in the committee.
    pub fn get_number_of_workers(&self) -> usize {
        let committee = self.inner.committee.lock().unwrap();

        committee
            .iter()
            .filter(|node| node.role == Role::Worker || node.role == Role::Leader)
            .count()
    }

    /// Get number of backup workers in the committee.
    pub fn get_number_of_backup_workers(&self) -> usize {
        let committee = self.inner.committee.lock().unwrap();

        committee
            .iter()
            .filter(|node| node.role == Role::BackupWorker)
            .count()
    }

    /// Get local node's role in the committee.
    ///
    /// May be `None` in case the local node is not part of the computation group.
    pub fn get_role(&self) -> Option<Role> {
        self.inner.get_role()
    }

    /// Check if the local node is a leader of the computation group.
    pub fn is_leader(&self) -> bool {
        self.get_role() == Some(Role::Leader)
    }
}
