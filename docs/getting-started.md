# Get started

Turn your music collection into a shared radio station. Start with music-only playback, then add presenters and shows when you are ready.

## What you need

- A computer or server with Docker and Docker Compose, plus Git to download the project.
- A compatible UPnP music server, such as MinimServer or Gerbera, reachable from StationOS. Keep it running while listeners tune in.
- Music you are entitled to use for your intended audience.
- AI service access if you want generated presentation or help from the production assistant.

These steps install a local, self-hosted instance. For a server behind an HTTPS proxy, also follow [Hosting and maintenance](hosting.md).

## Install StationOS

Download the project and create your private settings file:

```sh
git clone https://github.com/paulbowler/StationOS.git
cd StationOS
cp .env.example .env
```

On an existing installation, edit its existing `.env`; preserve its password and other settings.

Set `STATIONOS_ADMIN_PASSWORD` in `.env` to a strong password, then start StationOS:

```sh
docker compose up --build -d
```

Open [the producer workspace](http://127.0.0.1:8080/producer). Sign in as `admin` with your chosen password. Docker stores your database in a persistent volume. A blank password disables management login.

## Connect your music

1. Open **Sources → Add source**.
2. Choose **Use server** beside a discovered music server, or enter its UPnP device-description URL manually.
3. Review the name and address, then choose **Add source**.
4. Open the source and choose **Refresh library**.

The description URL identifies the UPnP server; it is not a music folder or a stream URL. If automatic discovery finds nothing, manual setup still works when the server is reachable. See [networking guidance](hosting.md#music-server-connectivity).

StationOS imports searchable metadata and artwork references. Your audio remains on the music server. Sources are shared by all your stations.

## Create your first station

1. In **Stations**, choose **Create station** and set its name and timezone.
2. Open its **Playlists** tab and create a playlist.
3. In **Selections**, choose some music and save. You can also use **Add tracks** to include individual recordings.
4. In the playlist’s **Details**, make it the station’s default playlist.
5. Open the station’s **Overview** and choose **Listen**.

The first listener starts the shared broadcast. Allow time for the source and audio processing to start. With no scheduled show, the station plays its default playlist.

If nothing plays, check that the playlist contains available recordings and that StationOS can reach their source. The station’s Overview and Incidents show current problems.

## Add personality and a programme

Follow [AI setup](ai-setup.md), create a DJ, then add a show from the station’s **Schedule** tab. Choose its days, times, presenter and playlist.

Continue with [Run your station](running-your-station.md) or [Listening and sharing](listening.md).

[All guides](README.md)
