//! Address defintion and helpers.
use std::convert::TryFrom;
use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};

use error::Error;

use ekiden_common_api as api;

/// Address represents a public location that can be used to connect to an entity in ekiden.
#[derive(Copy, Clone, Debug, PartialEq, Eq, Hash, Serialize, Deserialize)]
pub struct Address(SocketAddr);

impl TryFrom<api::Address> for Address {
    /// try_from Converts a protobuf `common::api::Address` into an address.
    type Error = super::error::Error;
    fn try_from(a: api::Address) -> Result<Self, Error> {
        let ip = a.get_address();
        let addr = match a.get_transport() {
            api::Address_Transport::TCPv4 => {
                let mut v4: [u8; 4] = Default::default();
                if ip.len() != 4 {
                    return Err(Error::new("Invalid IP length."));
                }
                v4.copy_from_slice(&ip[0..4]);
                IpAddr::V4(Ipv4Addr::from(v4))
            }
            api::Address_Transport::TCPv6 => {
                let mut v6: [u8; 16] = Default::default();
                if ip.len() != 16 {
                    return Err(Error::new("Invalid IP length."));
                }
                v6.copy_from_slice(&ip[0..16]);
                IpAddr::V6(Ipv6Addr::from(v6))
            }
        };
        // TODO: currently just ignore data set in upper half of port. should error.
        let port = a.get_port();
        Ok(Address(SocketAddr::new(addr, port as u16)))
    }
}

impl Into<api::Address> for Address {
    fn into(self) -> api::Address {
        let mut a = api::Address::new();
        match self.0.ip() {
            IpAddr::V4(ip) => {
                a.set_transport(api::Address_Transport::TCPv4);
                a.set_address(ip.octets().to_vec());
            }
            IpAddr::V6(ip) => {
                a.set_transport(api::Address_Transport::TCPv6);
                a.set_address(ip.octets().to_vec());
            }
        }
        a.set_port(self.0.port().into());
        a
    }
}
