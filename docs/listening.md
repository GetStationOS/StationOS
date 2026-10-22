# Listening and sharing

Give listeners a station page for browsing and playback, or a direct stream address for a compatible internet radio or player.

## Choose who can find your station

In station **Settings**, set **Public visibility**:

| Visibility | Listener access |
| --- | --- |
| Listed | Appears in the public station directory. |
| Unlisted | Works through direct page and stream links without appearing in the directory. |
| Private | Available through producer preview. |

New stations default to Unlisted. Unlisted links can be shared by anyone who has them; use Private when you need to keep the station out of public listening.

## Share the listener page

The public directory is `/`. Each station has a page at `/radio/<stream-address>` with programme information, current playback metadata and listening controls. Shows can offer requests and shoutouts when enabled.

Copy the station’s actual link from your installation. Opening a page never starts playback; the listener chooses to listen.

## Connect an internet radio

Use the MP3 feed at `/streams/<stream-address>`. Copy the advertised URL from StationOS and add it to your compatible player’s internet-radio presets. Public streams do not require the producer password.

Renaming a station preserves its addresses. Editing **Stream address** changes both its listener page and feed; update shared links and device presets afterwards.

For listeners outside your network, configure a reachable HTTPS origin and proxy. [Hosting and maintenance](hosting.md) explains this setup.

## Understand live playback

The first listener starts production. Later listeners join the same programme, and the last disconnection stops production. This is a shared live stream; listeners cannot seek or resume from an earlier song.

The app player stays active during in-app navigation. Pause releases its connection; Resume rejoins the live point. Stop ends that player’s connection. A full browser reload ends playback.

Output is 128 kbps MP3 at 44.1 kHz stereo. Device buffers can put listeners a few seconds apart, so displayed metadata may lead what a particular radio is playing. Small gaps can occur between audio segments; crossfades and sample-perfect gapless playback are not provided.

[All guides](README.md)
