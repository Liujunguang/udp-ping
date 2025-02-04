# UDP Ping

Ping with UDP, send and receive UDP packets in client-server mode,
detect the network status between two endpoints, print the detection results.

## Usage

Compile:
```shell
go build .
```

`Server` side:
```shell
./udp_ping -s -local 0.0.0.0:10000
```

`Client` side:
```shell
./udp_ping -host [target_ip] -port 10000
```

## Parameters

- s: Start server mode
- host: Target address
- port: Target port
- local: Local UDP listening address
- proxy: Whether to enable local proxy protocol forwarding
- proxyAddress: Local proxy protocol forwarding address
- rate: Packet sending rate (ms), default 1000ms
- num: Number of packets to send and receive, default 1000
- max: Upper limit of random packet size, default 1100 bytes
- min: Lower limit of random packet size, default 100 bytes
- div: Offset for random packet size, default 400 bytes, max = maxExp + maxDiv + rand, min = maxExp - maxDiv + rand

## Features

- No configuration file required
- Supports multiple instances
- Supports local proxy protocol, default supports SOCKS5
- Supports CRC32 checksum
