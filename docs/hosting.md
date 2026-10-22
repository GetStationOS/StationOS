# Hosting and maintenance

Keep your self-hosted station reachable, protect producer access and preserve the configuration behind it. For the first local installation, use [Get started](getting-started.md).

## Save installation settings

Keep deployment settings in the project’s ignored `.env` file. Use `.env.example` for a new installation; preserve the existing file when updating.

| Setting | Purpose |
| --- | --- |
| `STATIONOS_ADMIN_PASSWORD` | Password for the `admin` account. Blank disables management login. |
| `STATIONOS_PUBLIC_URL` | Your public HTTPS origin, without a path, for advertised links and proxied form writes. |
| `COMPOSE_FILE` | `compose.yaml` for standard networking; `compose.yaml:compose.discovery.yaml` for optional Linux LAN discovery. |
| `STATIONOS_LISTEN_ADDRESS` | Listening address in discovery mode; use an address reachable by your proxy. |

Keep `.env`, credentials and encryption keys private and out of Git. Docker’s standard configuration publishes port 8080 on host loopback and stores application data in a named volume.

## Publish through HTTPS

Use a reverse proxy to provide HTTPS at your chosen hostname. Set `STATIONOS_PUBLIC_URL` to that exact origin, for example `https://radio.example.com`, and set a management password before exposing the installation.

The public origin controls advertised links and trusted form writes. It does not configure DNS, certificates, routing or network exposure. Forwarded headers do not add trusted origins.

A proxy on a separate computer or in another network namespace must connect to a reachable StationOS address. Its own `127.0.0.1` is not the StationOS host. In standard bridge mode, the supplied Compose configuration publishes only to host loopback; arrange connectivity to match your proxy deployment.

For Nginx Proxy Manager, disable response caching. Preserve unbuffered delivery for streams and live event responses, including `/streams/`, `/radio-events`, `/radio/…` and `/producer/stations/…`. StationOS sends `X-Accel-Buffering: no`; custom proxy rules must not override it with buffering or compression.

Producer login uses the single `admin` account. Use HTTPS or a private tunnel for remote management. Multi-user roles are not provided. Public station pages and permitted streams are separate from producer login.

## Music-server connectivity

StationOS needs outbound access to the configured music server. Automatic discovery also needs IPv4 SSDP multicast on its local network. Docker’s bridge may not carry LAN discovery; manually entered UPnP description URLs can still work.

On Linux, the optional `compose.discovery.yaml` enables host networking. It requires Docker Compose 2.24.4 or later. Save `COMPOSE_FILE=compose.yaml:compose.discovery.yaml` in `.env` and set `STATIONOS_LISTEN_ADDRESS` for your actual proxy connection. Host networking exposes the configured listener directly; choose its address deliberately.

The discovery override keeps the same named data volume. Standard Compose commands use the files selected by `.env`; explicit `-f` flags override that selection. Keep your project name and directory stable so updates retain the existing volume.

Discovery does not cross routed networks or support IPv6 discovery. The description URL must identify the source server; loopback, multicast, link-local and unspecified source addresses are rejected. Audio resources must stay on that server. If a source address changes, check its configuration and refresh the library.

## Back up and restore

Back up the SQLite database and its `.secrets-key` sidecar together. The key is required to decrypt saved AI credentials. Keep a private copy of `.env` as well.

Use SQLite online backup tooling for a running database. A plain file copy requires a stopped, consistent database and its relevant WAL files; do not copy only the main database while writes continue. Include any application-managed artwork and other persistent files by backing up the data volume consistently.

On restore, preserve the database/key pair and file permissions. StationOS refuses to create a replacement key when encrypted credentials already exist. Only one server should own a database.

## Update an installation

Before updating, take a verified backup and preserve `.env` and the data volume. Review the changes, update the checkout and rebuild with the same Compose project settings. Database migrations run on startup. An older executable must not be used against an upgraded database; rollback requires a compatible backup.

If you previously stored local addresses in tracked Compose files, move those values into `.env` before replacing the files. Preserve unrelated local changes.

## Check a problem

| Symptom | Check |
| --- | --- |
| Station is Dormant | This is normal with no listeners. Choose Listen to start production. |
| No music starts | Confirm a playable default/show playlist, available recordings and source connectivity. |
| Music plays without a DJ | Check the selected text service and the DJ’s speech endpoint, credentials, model and voice. |
| Source discovery finds nothing | Try its actual description URL and check multicast reachability from StationOS. |
| Proxy returns 502 | Confirm the proxy can reach the actual listening address and port. |
| Proxied forms fail | Match `STATIONOS_PUBLIC_URL` to the external HTTPS origin. |
| Hardware radio stalls | Compare direct LAN playback with HTTPS playback; check source supply, proxy buffering and network quality. |
| History is incomplete | Inspect recording-health warnings and storage; audio can continue despite a history-write failure. |

Use station **Overview**, **Timeline** and **Incidents**, then inspect container logs where needed. Remove credentials and private source addresses before sharing logs publicly.

## Capacity and history

Output uses approximately 128 kbps per listener before network overhead. Configured admission limits are not hardware capacity guarantees; size bandwidth, CPU and AI services for your expected audience.

The stream buffer defaults to five seconds and is configurable from zero to five seconds. Longer source or network outages can exhaust it. Listeners that cannot keep up may be disconnected to protect the shared broadcast.

Timeline records server output once per item, regardless of listener count. Restarted or interrupted records can have an unknown outcome. These records do not establish what a particular device heard. Filter and export up to 1,000 matching records at a time; narrow the filters for larger histories. History has no automatic expiry, so monitor database growth.

## Command-line maintenance

For installations using the executable directly:

```sh
./bin/stationos -db /path/to/stationos.db -task integrity
./bin/stationos -db /path/to/stationos.db -task sync -source <source-id>
./bin/stationos -db /path/to/stationos.db -task clear-catalogue -source <source-id>
```

Use the intended installation database. Clearing a catalogue removes derived metadata; refresh the source to rebuild it. Station and playlist configuration are kept separately. Imports replace a complete source snapshot rather than applying incremental changes.

For executable flags, use `./bin/stationos -help`. For building from source, see [Contributing](../CONTRIBUTING.md).

[All guides](README.md)
