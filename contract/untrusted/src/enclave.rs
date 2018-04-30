//! Enclave async contract interface.
use serde_cbor;
use sgx_types::*;

use ekiden_common::error::{Error, Result};
use ekiden_contract_common::batch::{CallBatch, OutputBatch};
use ekiden_enclave_untrusted::Enclave;

use super::ecall_proxy;

pub trait EnclaveContract {
    /// Maximum response size (in kilobytes).
    const MAX_RESPONSE_SIZE: usize = 1024;

    /// Check if the enclave has a batch ready for execution and copy it over.
    fn contract_take_batch(&self) -> Result<CallBatch>;

    /// Invoke a contract on a batch of calls and return the (encrypted) outputs.
    fn contract_call_batch(&self, batch: &CallBatch) -> Result<OutputBatch>;
}

impl EnclaveContract for Enclave {
    fn contract_take_batch(&self) -> Result<CallBatch> {
        // Reserve space up to the maximum size of serialized response.
        let mut response: Vec<u8> = Vec::with_capacity(Self::MAX_RESPONSE_SIZE * 1024);
        let mut response_length = 0;

        let status = unsafe {
            ecall_proxy::contract_take_batch(
                self.get_id(),
                response.as_mut_ptr() as *mut u8,
                response.capacity(),
                &mut response_length,
            )
        };

        if status != sgx_status_t::SGX_SUCCESS {
            return Err(Error::new(format!(
                "contract_take_batch: failed to call enclave ({})",
                status
            )));
        }

        unsafe {
            response.set_len(response_length);
        }

        Ok(serde_cbor::from_slice(&response)?)
    }

    fn contract_call_batch(&self, batch: &CallBatch) -> Result<OutputBatch> {
        // Encode input batch.
        let batch_encoded = serde_cbor::to_vec(batch)?;

        // Reserve space up to the maximum size of serialized response.
        let mut response: Vec<u8> = Vec::with_capacity(Self::MAX_RESPONSE_SIZE * 1024);
        let mut response_length = 0;

        let status = unsafe {
            ecall_proxy::contract_call_batch(
                self.get_id(),
                batch_encoded.as_ptr() as *const u8,
                batch_encoded.len(),
                response.as_mut_ptr() as *mut u8,
                response.capacity(),
                &mut response_length,
            )
        };

        if status != sgx_status_t::SGX_SUCCESS {
            return Err(Error::new(format!(
                "contract_call_batch: failed to call enclave ({})",
                status
            )));
        }

        unsafe {
            response.set_len(response_length);
        }

        let outputs: OutputBatch = serde_cbor::from_slice(&response)?;

        // Assert equal number of responses, fail otherwise (corrupted response).
        if outputs.len() != batch.len() {
            return Err(Error::new(
                "contract_call_batch: corrupted response (response count != request count)",
            ));
        }

        Ok(outputs)
    }
}
