extern crate ekiden_common;
extern crate ekiden_stake_base;
extern crate ekiden_stake_dummy;

#[macro_use]
extern crate log;

use std::collections::HashSet;
use std::sync::Arc;

use ekiden_common::bytes::B256;
use ekiden_common::error::Error;
use ekiden_common::futures::Future;
use ekiden_common::testing::try_init_logging;
use ekiden_common::uint::U256;
use ekiden_stake_base::*;
use ekiden_stake_dummy::*;

#[derive(Copy, Clone)]
struct IdGenerator {
    id: U256,
}

impl IdGenerator {
    fn new() -> Self {
        Self { id: U256::from(1) }
    }

    fn get(&self) -> B256 {
        B256::from_slice(&self.id.to_vec())
    }

    fn incr_mut(&mut self) -> Result<(), Error> {
        let mut next = self.id;
        next = next + U256::from(1);
        if next == U256::from(0) {
            return Err(Error::new("OVERFLOW"));
        }
        self.id = next;
        Ok(())
    }

    fn gen_id(&mut self) -> B256 {
        let rv = self.get();
        self.incr_mut().unwrap();
        rv
    }
}

pub fn amount_to_hex_string(amt: AmountType) -> String {
    let vs: Vec<String> = amt.to_vec().iter().map(|v| format!("{:02x}", v)).collect();
    vs.join("")
}

fn get_and_show_stake(backend: &Arc<DummyStakeEscrowBackend>, id: B256, name: &str) -> StakeStatus {
    let ss = backend.get_stake_status(id).wait().unwrap();
    debug!("{}'s stake status:", name);
    debug!(" total_stake: {}", ss.total_stake);
    debug!(" escrowed: {}", ss.escrowed);
    ss
}

#[test]
fn test_dummy_stake_backend() {
    try_init_logging();

    let mut id_generator = IdGenerator::new();
    let oasis = id_generator.gen_id();

    let initial_total_tokens = AmountType::from(1);
    let expected_decimals = 18u8;
    let initial_total_supply =
        initial_total_tokens * AmountType::from(1_000_000_000_000_000_000u64);

    let backend = Arc::new(DummyStakeEscrowBackend::new(
        oasis,
        "EkidenStake".to_string(),
        "E$".to_string(),
        initial_total_tokens,
    ));

    let alice = id_generator.gen_id();

    let decimals = backend.get_decimals().wait().unwrap();
    assert_eq!(decimals, expected_decimals);

    // backend.deposit_stake(alice, AmountType::from(100)).wait().unwrap();
    backend
        .transfer(oasis, alice, AmountType::from(100))
        .wait()
        .unwrap();

    let stake_status = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(stake_status.total_stake, AmountType::from(100));
    assert_eq!(stake_status.escrowed, AmountType::from(0));

    let bob = id_generator.gen_id();
    let bob_aux = id_generator.gen_id();

    let bob_escrow_id = backend
        .allocate_escrow(alice, bob, AmountType::from(9), bob_aux)
        .wait()
        .unwrap();

    debug!("got escrow id {} for bob", bob_escrow_id);

    let stake_status = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(stake_status.total_stake, AmountType::from(100));
    assert_eq!(stake_status.escrowed, AmountType::from(9));

    let carol = id_generator.gen_id();
    let carol_aux = id_generator.gen_id();
    let carol_escrow_id = backend
        .allocate_escrow(alice, carol, AmountType::from(13), carol_aux)
        .wait()
        .unwrap();

    debug!("got escrow id {} for carol", carol_escrow_id);

    let mut expected = HashSet::new();
    expected.insert((bob_escrow_id, bob, AmountType::from(9), bob_aux));
    expected.insert((carol_escrow_id, carol, AmountType::from(13), carol_aux));
    for entry in &expected {
        debug!(
            "expected: ({}, {}, {}, {})",
            entry.0, entry.1, entry.2, entry.3
        );
    }

    let mut iter = backend.list_active_escrows_iterator(alice).wait().unwrap();
    let mut actual = HashSet::new();
    while iter.has_next {
        let (eas, iter_tmp) = backend.list_active_escrows_get(iter).wait().unwrap();
        // https://github.com/rust-lang/rfcs/issues/372 For now,
        // destructuring assignments has to be a let, so iter_tmp is
        // needed.
        actual.insert((eas.id, eas.target, eas.amount, eas.aux));
        iter = iter_tmp;
    }

    let d: HashSet<_> = expected.symmetric_difference(&actual).collect();
    for entry in &d {
        debug!("sd: ({}, {}, {}, {})", entry.0, entry.1, entry.2, entry.3);
    }
    assert!(d.is_empty());

    // Should we now check that ved is actually sorted?  This is only
    // a part of the interface to minimize information leak, so maybe
    // later: we should make the EscrowAccount type public for
    // testing, convert the ved via a map to that, ....

    // let withdrawn_amount = backend.withdraw_stake(alice, AmountType::from(10)).wait().unwrap();
    // assert_eq!(withdrawn_amount, AmountType::from(10));
    assert!(
        backend
            .transfer(alice, oasis, AmountType::from(10))
            .wait()
            .unwrap()
    );

    let stake_status = backend.get_stake_status(alice).wait().unwrap();
    assert_eq!(stake_status.total_stake, AmountType::from(100 - 10));
    assert_eq!(stake_status.escrowed, AmountType::from(9 + 13));

    let eas = backend.fetch_escrow_by_id(bob_escrow_id).wait().unwrap();
    assert_eq!(eas.id, bob_escrow_id);
    assert_eq!(eas.target, bob);
    assert_eq!(eas.amount, AmountType::from(9));

    debug!("taking 10 -- too much, should fail");
    match backend
        .take_and_release_escrow(bob, bob_escrow_id, AmountType::from(10))
        .wait()
    {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(
                e.message,
                ErrorCodes::RequestExceedsEscrowedFunds.to_string()
            );
        }
        Ok(v) => {
            error!(
                "Got amount {} when take request should have failed (RequestExceedsEscrowedFunds)",
                v
            );
            assert!(false);
        }
    }

    debug!("carol attempts to take 4");
    match backend
        .take_and_release_escrow(carol, bob_escrow_id, AmountType::from(4))
        .wait()
    {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::CallerNotEscrowTarget.to_string());
        }
        Ok(amount) => {
            error!("Got {} instead of failing when Carol attempting to take too much (CallerNotEscrowTarget)", amount);
            assert!(false);
        }
    };

    debug!("bob taking 5");
    assert_eq!(
        backend
            .take_and_release_escrow(bob, bob_escrow_id, AmountType::from(5))
            .wait()
            .unwrap(),
        AmountType::from(5)
    );

    debug!("escrow id should be invalid");
    match backend.fetch_escrow_by_id(bob_escrow_id).wait() {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::NoEscrowAccount.to_string());
        }
        Ok(eas) => {
            error!(
                "Found escrow account {} when request should have failed, target {}, amount {} (NoEscrowAccount)",
                eas.id, eas.target, eas.amount
            );
            assert!(false);
        }
    }

    let stake_status = backend.get_stake_status(alice).wait().unwrap();
    assert_eq!(stake_status.total_stake, AmountType::from(100 - 10 - 5));
    assert_eq!(stake_status.escrowed, AmountType::from(13));

    debug!("bob's account should have been credited");
    let ss = get_and_show_stake(&backend, bob, "Bob");
    assert_eq!(ss.total_stake, AmountType::from(5));
    assert_eq!(ss.escrowed, AmountType::from(0));

    debug!("transfer from bob to carol -- too much");
    match backend.transfer(bob, carol, AmountType::from(100)).wait() {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientFunds.to_string());
        }
        Ok(_) => {
            error!("Transfer from bob to carol (too much) should not have succeeded (InsufficientFunds).");
            assert!(false);
        }
    }

    debug!("transfer from bob to carol -- some (2)");
    backend
        .transfer(bob, carol, AmountType::from(2))
        .wait()
        .unwrap();
    // verify amounts
    let ss = get_and_show_stake(&backend, bob, "Bob");
    assert_eq!(ss.total_stake, AmountType::from(3));
    assert_eq!(ss.escrowed, AmountType::from(0));
    let ss = get_and_show_stake(&backend, carol, "Carol");
    assert_eq!(ss.total_stake, AmountType::from(2));
    assert_eq!(ss.escrowed, AmountType::from(0));

    // Transfer all
    debug!("transfer from bob to carol -- all");
    backend
        .transfer(bob, carol, AmountType::from(3))
        .wait()
        .unwrap();
    // verify amounts
    let ss = get_and_show_stake(&backend, bob, "Bob");
    assert_eq!(ss.total_stake, AmountType::from(0));
    assert_eq!(ss.escrowed, AmountType::from(0));

    let ss = get_and_show_stake(&backend, carol, "Carol");
    assert_eq!(ss.total_stake, AmountType::from(5));
    assert_eq!(ss.escrowed, AmountType::from(0));

    let carol_stake = ss.total_stake;

    debug!("transfer from alice to bob -- should be insufficient");
    match backend
        .transfer(alice, bob, AmountType::from(100 - 10 - 5 - 13 + 1))
        .wait()
    {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientFunds.to_string());
        }
        Ok(_) => {
            error!("Transfer from alice to bob should not have succeeded (InsufficientFunds).");
            assert!(false);
        }
    }

    debug!("transfer from alice to dexter -- should create dexter account");
    let dexter = id_generator.gen_id();
    backend
        .transfer(alice, dexter, AmountType::from(17))
        .wait()
        .unwrap();
    // verify amounts
    let ss = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(ss.total_stake, AmountType::from(100 - 10 - 5 - 17));
    assert_eq!(ss.escrowed, AmountType::from(13));

    let ss = get_and_show_stake(&backend, dexter, "Dexter");
    assert_eq!(ss.total_stake, AmountType::from(17));
    assert_eq!(ss.escrowed, AmountType::from(0));

    // ----------------------------------------------------
    // self transfer
    // ----------------------------------------------------
    debug!("transfer from dexter to dexter");
    backend
        .transfer(dexter, dexter, AmountType::from(17))
        .wait()
        .unwrap();
    let ss = get_and_show_stake(&backend, dexter, "Dexter");
    assert_eq!(ss.total_stake, AmountType::from(17));
    assert_eq!(ss.escrowed, AmountType::from(0));
    // ----------------------------------------------------
    debug!("transfer too much from dexter to dexter");
    match backend
        .transfer(dexter, dexter, AmountType::from(19))
        .wait()
    {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientFunds.to_string());
        }
        Ok(_) => {
            error!("Transfer from dexter to dexter succeeded but shouldn't (InsufficientFunds).");
            assert!(false);
        }
    }
    let ss = get_and_show_stake(&backend, dexter, "Dexter");
    assert_eq!(ss.total_stake, AmountType::from(17));
    assert_eq!(ss.escrowed, AmountType::from(0));

    // ----------------------------------------------------
    // Self escrow & release
    // ----------------------------------------------------
    debug!("create escrow from alice to alice");
    let self_19_aux = id_generator.gen_id();
    let self_escrow_id = backend
        .allocate_escrow(alice, alice, AmountType::from(19), self_19_aux)
        .wait()
        .unwrap();
    let ss = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(ss.total_stake, AmountType::from(100 - 10 - 5 - 17));
    assert_eq!(ss.escrowed, AmountType::from(13 + 19));

    debug!("taking some from self-escrow");
    backend
        .take_and_release_escrow(alice, self_escrow_id, AmountType::from(11))
        .wait()
        .unwrap();
    let ss = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(ss.total_stake, AmountType::from(100 - 10 - 5 - 17));
    assert_eq!(ss.escrowed, AmountType::from(13));
    // ----------------------------------------------------
    debug!("create escrow from alice to alice");
    let self_23_aux = id_generator.gen_id();
    let self_escrow_id = backend
        .allocate_escrow(alice, alice, AmountType::from(23), self_23_aux)
        .wait()
        .unwrap();
    let ss = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(ss.total_stake, AmountType::from(100 - 10 - 5 - 17));
    assert_eq!(ss.escrowed, AmountType::from(13 + 23));

    debug!("taking all from self-escrow");
    backend
        .take_and_release_escrow(alice, self_escrow_id, AmountType::from(23))
        .wait()
        .unwrap();
    let ss = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(ss.total_stake, AmountType::from(100 - 10 - 5 - 17));
    assert_eq!(ss.escrowed, AmountType::from(13));
    // ----------------------------------------------------
    debug!("create escrow from alice to alice");
    let self_29_aux = id_generator.gen_id();
    let self_escrow_id = backend
        .allocate_escrow(alice, alice, AmountType::from(29), self_29_aux)
        .wait()
        .unwrap();
    let ss = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(ss.total_stake, AmountType::from(100 - 10 - 5 - 17));
    assert_eq!(ss.escrowed, AmountType::from(13 + 29));

    debug!("taking none from self-escrow");
    backend
        .take_and_release_escrow(alice, self_escrow_id, AmountType::from(0))
        .wait()
        .unwrap();
    let ss = get_and_show_stake(&backend, alice, "Alice");
    assert_eq!(ss.total_stake, AmountType::from(100 - 10 - 5 - 17));
    assert_eq!(ss.escrowed, AmountType::from(13));

    let alice_balance = backend.balance_of(alice).wait().unwrap();
    assert_eq!(alice_balance, ss.total_stake - ss.escrowed);

    // total_supply, burn
    let total_supply = backend.get_total_supply().wait().unwrap();
    assert_eq!(total_supply, initial_total_supply);
    let burn_amount = AmountType::from(1_000);
    let expected_supply = initial_total_supply - burn_amount;
    assert!(backend.burn(oasis, burn_amount).wait().unwrap());
    let total_supply = backend.get_total_supply().wait().unwrap();
    assert_eq!(total_supply, expected_supply);

    // excessive approve, allowance
    let excessive_approval = AmountType::from(1_000_000_000);
    assert!(
        backend
            .approve(alice, bob, excessive_approval)
            .wait()
            .unwrap()
    );

    let excessive_burn = AmountType::from(2_000_000_000);
    match backend.burn_from(bob, alice, excessive_burn).wait() {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientFunds.to_string());
        }
        Ok(b) => {
            if b {
                error!("Got result {} when request should have failed.", b);
                assert!(!b);
            }
        }
    }
    let allowance = backend.allowance(alice, bob).wait().unwrap();
    assert_eq!(allowance, excessive_approval);

    // approve, allowance, burn_from
    let reasonable_approval = AmountType::from(10);
    assert!(
        backend
            .approve(alice, bob, reasonable_approval)
            .wait()
            .unwrap()
    );
    let reasonable_burn = AmountType::from(6);
    assert!(
        backend
            .burn_from(bob, alice, reasonable_burn)
            .wait()
            .unwrap()
    );
    let remaining_allowance = backend.allowance(alice, bob).wait().unwrap();
    assert_eq!(remaining_allowance, reasonable_approval - reasonable_burn);

    assert_eq!(
        backend.balance_of(alice).wait().unwrap(),
        alice_balance - reasonable_burn
    );
    let alice_balance = alice_balance - reasonable_burn;

    // burn too much
    let excessive_burn = remaining_allowance + AmountType::from(1);
    match backend.burn_from(bob, alice, excessive_burn).wait() {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientAllowance.to_string());
        }
        Ok(b) => {
            if b {
                error!("Got result {} when request should have failed.", b);
                assert!(!b);
            }
        }
    }
    assert_eq!(backend.balance_of(alice).wait().unwrap(), alice_balance);

    // transfer_from
    let excessive_transfer = remaining_allowance + AmountType::from(1);
    match backend
        .transfer_from(bob, alice, carol, excessive_transfer)
        .wait()
    {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientAllowance.to_string());
        }
        Ok(b) => {
            if b {
                error!("Got result {} when request should have failed.", b);
                assert!(!b);
            }
        }
    }
    assert_eq!(backend.balance_of(alice).wait().unwrap(), alice_balance);

    let excessive_transfer = alice_balance + AmountType::from(1);
    match backend
        .transfer_from(bob, alice, carol, excessive_transfer)
        .wait()
    {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientFunds.to_string());
        }
        Ok(b) => {
            if b {
                error!("Got result {} when request should have failed.", b);
                assert!(!b);
            }
        }
    }

    let ok_transfer = alice_balance;
    debug!(
        "ok_transfer = {}, alice_balance = {}",
        ok_transfer, alice_balance
    );
    match backend.transfer_from(bob, alice, carol, ok_transfer).wait() {
        Err(e) => {
            debug!("Got error {}", e.message);
            assert_eq!(e.message, ErrorCodes::InsufficientAllowance.to_string());
        }
        Ok(b) => {
            if b {
                error!("Got result {} when request should have failed.", b);
                assert!(!b);
            }
        }
    }
    assert!(backend.approve(alice, bob, ok_transfer).wait().unwrap());
    assert!(
        backend
            .transfer_from(bob, alice, carol, ok_transfer)
            .wait()
            .unwrap()
    );

    assert_eq!(
        backend.balance_of(alice).wait().unwrap(),
        AmountType::from(0)
    );
    assert_eq!(
        backend.balance_of(carol).wait().unwrap(),
        carol_stake + ok_transfer
    );
}
