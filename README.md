# Server Side Ratelimit Provider for fastd

## Motivation

Gluon supports bandwidth limitation for the Mesh-VPN connection.
This can be employed by using sqm-cake on the node itself in both upstream and
downstream direction (using an Intermediate Functional Block (IFB) device
for the downstream direction).

This approach is the same as if one would employ SQM on a DSL or Fiber connection, where
the available bandwidth is the primary limiting factor. As the scheduler of the BNG can't
be configured by the user, one has to artificially limit the bandwidth on the router itself
to prevent bufferbloat.

For a typical VPN connection case, the situation is different. First we can control both sides
of the to-be-managed link, as the VPN server itself is under the control of the operator.
Second, while bandwidth of the physical user-connection is a limiting factor, on many routers
deployed over the last few years, the limiting factor is often the CPU. In this case, packets
per second (pps) is the limiting factor, and not bandwidth.

## Prerequisites

 - Gluon v2025.1 or later
 - fastd operating in L2TP offloading mode

## Configuration

### Client

The client needs to be configured in order to send the regular updates. This is done using UCI.

The annotated default configuration can be found here <TODO: link to the default configuration file>.

### Server

<TODO: link to the default configuration file>

## Implementation

The goal is to implement a communication link between the VPN server and the VPN client on the
node itself. In this case, fastd is used in L2TP offloading mode, each connection is represented as
a dedicated network interface on the server-side. This allows to employ regular Linux traffic control
(tc) to shape the traffic on the server-side, and to use a simple UDP-based protocol to exchange
information about both ends of the connection.

Upstream traffic (Client to Server) is still shaped on the node itself.

## Rate determination

We need to determine the target rate for both upstream and downstream on a number of factors.

### User limit

The user can set their upper limits for both upstream and downstream in the configuration of the node.
The server takes these values into account as the upper ceilings for the shaper settings.

### Minimum rate

In order to prevent the connection from being completely unusable, a minimum rate for both upstream and
downstream direction is configured by the server. This limit is currently set to 6192 kbit/s for downstream
and 1536 kbit/s for upstream.

These values can be overridden by the user in the configuration of the node, as they are communicated to
the server as part of the regular updates.

### System load

We need to take the system load into account. Contrary to a limit of the available throughput capacity,
we need to evaluate the sum of up and downstream traffic. This is due to the fact we are limited by the PPS throughput
on the CPU.

There will be some discrepancy between these two, as SKB enlargements in the kernel and their expense might
differ from upstream compared to downstream direction.

In case the 1 Minute system load average exceeds number of CPU cores by a factor of 1.5, the server will reduce
the shaper settings by 20% of the total throughput proportional in up and downstream to the current limits in order to
prevent further load on the CPU.

### Throughput capacity

Throughput capacity is the attainable data rate of the connection.

In theory we could do a Speedtest to determine the attainable data rate of the connection, but this
would put unnecessary load on the connection.

Instead, the node communicates the total number of packets and bytes sent in both directions since the
last reboot to the server.

The server gathers this information on his side as well. It will regularly calculate the loss rate of the
connection within the last 60 seconds. In case the loss rate is above 3% in either direction, the server
will reduce the shaper settings by loss-rate * 2 to prevent further packet loss.

In case the loss rate is below 1% in either directions and the currently utilized bandwith is exceeding 80% of
the configured shaper settings, the server will increase the shaper settings by 5% to allow for better
utilization of the connection.

This is a simple heuristic to determine the attainable data rate of the connection without putting unnecessary
load on the connection itself.

### Protocol

The protocol is UDP based. The server is the authorative instance of the shaper settings.

#### Communication

Both endpoints communicate via IPv6 Link-Local unicast addresses.

The Server is always reachable at the Link-Local address `fe80::f421:d:1` on port 42453.
The Client is always reachable at the Link-Local address `fe80::f421:d:2` on port 42454.

Messages are always sent with port 42453 as source and destination port.

The client regularly (every 15 seconds) sends a UDP packet to the server containing the
following information. The server replies to the client with a message containing the
desired shaper settings for both upstream and downstream direction also every 15 seconds.

#### Message encoding

 - All multi-byte fields are encoded in network byte order (big-endian).
 - The message is packed without any padding between fields.
 - The total size of the message is 256 bytes.

#### Message format

 - Version (1) (uint8_t)
 - Message Sequence Number (Start: 0) (uint32_t)
 - Machine Information (char[128])
   - Target (char[32])
   - Subtarget (char[32])
   - Number of CPU cores (uint8_t)
   - Reserved (All-Zeros) (uint8_t[63])
 - System state information (Load * 10) (uint8_t[4])
   - Load average over the last 1 minute (uint8_t)
   - Load average over the last 5 minutes (uint8_t)
   - Load average over the last 15 minutes (uint8_t)
 - Packet counter (uint64_t[2])
   - Total number of packets sent from the client to the server since the last reboot (uint64_t)
   - Total number of kilobytes sent from the client to the server since the last reboot (uint64_t)
   - Total number of packets sent from the server to the client since the last reboot (uint64_t)
   - Total number of kilobytes sent from the server to the client since the last reboot (uint64_t)
 - Downstream rate information (uint32_t[4])
   - Current target rate Downstream (Server -> Client) in kbit/s (uint32_t)
   - Current configured rate Downstream (Server -> Client) in kbit/s (uint32_t)
   - Minimum rate Downstream (Server -> Client) in kbit/s (uint32_t)
   - Maximum rate Downstream (Server -> Client) in kbit/s (uint32_t)
 - Upstream rate information (uint32_t[4])
   - Current target rate Upstream (Client -> Server) in kbit/s (uint32_t)
   - Current configured rate Upstream (Client -> Server) in kbit/s (uint32_t)
   - Minimum rate Upstream (Client -> Server) in kbit/s (uint32_t)
   - Maximum rate Upstream (Client -> Server) in kbit/s (uint32_t)

##### Rate Information

The rate information fields are used to determine the desired shaper settings on either side.
The configured rate always reflects the configuration currently active on the sending side.

###### Target Rate

The target rate field has a different meaning on the client and server side.

When sent from the client to the server, it reflects the desired shaper settings on the server side
by the client.
The server will use this information to determine the desired shaper settings on the server side.

When sent from the server to the client, it reflects the desired shaper settings on the client side by the server.
The client will use this information to determine the desired shaper settings on the client side.

A target rate of 0 indicates that no target rate has been set by the sending side. In this case,
the other side is free to set the shaper settings to any value within the configured minimum
and maximum rate.

###### Configured Rate

Configured rate reflects the current shaper settings on the sending side. It is used by the receiving
side to gain information about the current shaper settings on the other side of the connection.

###### Minimum and Maximum Rate

The minimum and maximum rate fields are used to communicate the configured minimum and maximum rates
on the sending side to the receiving side.
The receiving side will use this information to determine the desired shaper settings within
the configured limits.

In case neither target rate nor limits are configured on the sending side, the receiving side
is free to set the shaper settings to any value. In case no limits are applicable, the shaper
settings will be set to the maximum possible value or disabled completely, depending on
the capabilities of the shaper implementation.

### Shaper script

In order to make the shaper configuration more flexible, the shaper is configured using a
shell-script, where the following environment variables are set:

 - `FSSRL_ROLE`: The role of the script, either `server` or `client`
 - `FSSRL_TARGET_IF`: The target interface of the shaper settings, e.g. `mesh-vpn`
 - `FSSRL_DOWNSTREAM_RATE`: The desired downstream rate (Server -> Client) in kbit/s
 - `FSSRL_UPSTREAM_RATE`: The desired upstream rate (Client -> Server) in kbit/s

For the rate values, a value of 0 indicates that the shaper shall be disabled. The script
might also choose to set the shaper settings to a value of its choice, e.g. the maximum possible value, in case no limits are configured.

An example script can be found in `contrib/apply-shaper.sh`. It uses the cake qdisc to
configure the shaper settings and can be used out-of-the-box on the server as well as on
the client side.

### Server

The server listens on all interfaces for incoming messages from the clients.
Upon receiving a message, it extracts the information about the connection and
the current system state of the client.

The server extracts the information about minimum and maximum rates in downstream direction to set lower and
upper limits for the desired shaper settings. It also extracts the current system load information as well as
packet counters to make informed decisions about the current PPS / data-rate of the connection.

Based on this information, the server calculates the desired shaper settings for both upstream and downstream
direction and sends them back to the client.

### Client

The client receives the shaper settings from the server and applies them to the local shaper configuration.
It also regularly sends the current system load information and packet counters to the server to allow it
to make informed decisions about the shaper settings.

In similar fashion to the server, the client configures the shaper settings using a shell-script
executed on the client side, where the following environment variables are set:

 - `FSSRL_ROLE`: The role of the script, either `server` or `client`
 - `FSSRL_TARGET_IF`: The target interface of the shaper settings, e.g. `mesh-vpn`
 - `FSSRL_DOWNSTREAM_RATE`: The desired downstream rate (Server -> Client) in kbit/s
 - `FSSRL_UPSTREAM_RATE`: The desired upstream rate (Client -> Server) in kbit/s

