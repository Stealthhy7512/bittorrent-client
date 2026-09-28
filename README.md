# BitTorrent Client

A command-line BitTorrent client written in Go, built to learn the protocol while working toward a
small, usable downloader.

Current protocol support follows the original BitTorrent v1 specification.

## Current capabilities

- Parse single-file v1 `.torrent` metainfo, compute the info hash, and validate the piece layout.
- Announce to HTTP trackers and decode compact IPv4 or dictionary peer lists.
- Discover peers through bounded concurrent TCP connection and handshake attempts.
- Encode and decode peer-wire messages, including block requests and responses.
- Track a remote peer's choke state and piece availability in a session.

The initial scope is single-file torrents, HTTP trackers, and outbound TCP connections.

## Build

Requires Go **1.26.4 or later**.

```sh
go build -o bt ./cmd/bt
```

## Usage

Inspect metainfo:

```sh
./bt inspect example.torrent
```

Contact the tracker and list peer endpoints:

```sh
./bt announce --port 6881 --timeout 10s example.torrent
```

Find a peer that completes a valid handshake:

```sh
./bt handshake --port 6881 --timeout 10s example.torrent
```

For `announce` and `handshake`, `--port` defaults to `6881` and `--timeout` to `10s`. Put flags
before the metainfo path. The port is advertised to the tracker; the client does not currently
listen for inbound connections. The handshake command prints the selected endpoint and remote peer
ID, then closes the connection.

## Project structure

|       Package       |                   Responsibility                         |
| ------------------- | -------------------------------------------------------- |
|       `cmd/bt`      | Command-line arguments and output                        | 
| `internal/metainfo` | Metainfo parsing, info hashes, and piece layouts         | 
| `internal/tracker`  | HTTP announces and tracker response decoding             | 
| `internal/peerwire` | Handshakes, message framing, payloads, and bitfields     | 
| `internal/peerconn` | Peer connections, candidate discovery, and session state |

## Development

```sh
go test ./... go test -race ./... go vet ./...
```

Integration tests use local fake trackers and peers, without depending on a public swarm.
