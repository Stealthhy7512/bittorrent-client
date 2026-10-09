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
- Download and SHA-1 verify one selected piece that fits in a single 16 KiB block.

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

Download one piece to an explicit output path:

```sh
./bt download-piece --piece 0 --output piece-0.bin example.torrent
```

`--piece` is a zero-based index and `--output` is required. The command accepts `--force` to
replace an existing non-directory output, `--port` (default `6881`), and `--timeout` (default
`2m`). It creates a temporary file beside the output, checks the piece's SHA-1 hash, then
publishes the verified bytes. A failed transfer does not publish the output. The output's parent
directory must already exist.

The current transfer handles one block from the first peer that completes a valid handshake.
Pieces larger than 16 KiB are rejected, and the command does not retry another peer after a
transfer failure. Multi-block transfers, peer retries, and a `stopped` tracker announce are still
planned. `announce` and `handshake` default to a `10s` timeout. Put flags before the metainfo path.
The port is advertised to the tracker; the client does not listen for inbound connections or
upload data. The handshake command prints the selected endpoint and remote peer ID, then closes
the connection.

## Project structure

| Package             | Responsibility                                           |
| ------------------- | -------------------------------------------------------- |
| `cmd/bt`            | Commands, one-piece storage, verification, and output    |
| `internal/metainfo` | Metainfo parsing, info hashes, and piece layouts         |
| `internal/tracker`  | HTTP announces and tracker response decoding             |
| `internal/peerwire` | Handshakes, message framing, payloads, and bitfields     |
| `internal/peerconn` | Peer connections, candidate discovery, and session state |

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

Integration tests use local fake trackers and peers, without depending on a public swarm.
