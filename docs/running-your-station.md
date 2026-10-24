# Run your station

Your station brings together a musical identity, fictional presenters and a weekly programme. Use the producer workspace at `/producer` to shape and monitor it.

## Choose the music

Each station owns its playlists. A playlist is a pool of eligible music from which the programme selects recordings; the order shown while browsing does not set playback order.

Use **Selections** to choose genres, albums, performing artists, composers, years or decades. Alternatives within a category are combined; selected categories narrow each other. For example, Pop plus the 1980s gives 1980s pop. Pop and Rock plus the 1980s and 1990s gives either genre from either decade.

No selections means no automatically included tracks. Add individual recordings through **Add tracks**. Exclusions take priority, and overlapping selections do not duplicate a recording. After a library refresh, matching new music joins your rule-based selections automatically.

Use **Tracks** to search the playlist and review or exclude recordings. **Details** holds its name, description and default-playlist setting. Give each station a playable default to cover time between shows.

Refresh a source after changing its library. A failed import preserves the previous catalogue. Missing recordings can remain visible in saved selections but cannot supply audio.

## Create your presenters

Under **DJs**, create or edit a fictional presenter. Give them a personality and editorial brief, then choose their speech endpoint, model and voice. Available voice controls depend on the speech service. See [AI setup](ai-setup.md).

DJs are shared, so one presenter can appear on several stations. Their credentials belong to shared endpoints; text models are selected centrally. Saving a DJ profile does not start a broadcast.

## Build a weekly programme

In the station’s **Schedule**, choose **Add show**. Set its name, public description, days, start and end times, DJ and station playlist. Add a DJ music prompt when you want a particular theme or approach.

Shows repeat in the station’s timezone. Selecting several days creates separately editable weekly slots. An end time at or before the start continues into the next day; overlapping shows are rejected.

Open a dated episode from the calendar to see its selections and played tracks, or edit the direction for that episode. Episodes plan and run automatically; producer review is optional. Cancelling one dated episode leaves its recurring show in place.

The current track finishes before a handover. Track durations and preparation times mean exact-to-the-second transitions are not guaranteed. Default music covers schedule gaps.

## Go beyond music

Use ordered episodes for podcasts and audiobooks so instalments and chapters follow their intended sequence within the programme. Keep these sequences distinct from music playlists, which remain pools of eligible recordings.

Open **Announcements** to create listener messages, news and promotions. **Read exactly** speaks your saved script verbatim; **DJ adaptation** uses your supplied facts in the presenter's style. Saving a draft generates no speech. **Preview** in the Delivery card prepares a private sample and displays the spoken text and audio player beside the button. Save edits first; previews use the saved message and never start the station or count as airtime. Adapted wording may vary when prepared again for broadcast.

Every message has a start and expiry in station-local time. Activate it for a single delivery, interval repetition or selected daily times. Choose Whole station, Selected shows or One episode under Where to play. For one episode, select its show and local date; the matching programme time appears immediately. A presenter override wins over the current DJ; continuous music uses the default presenter in **Announcement settings**. Configure that default to allow messages outside scheduled shows.

Announcements play between tracks while listeners are connected. A selected time means the first suitable track boundary at or after that time. Ordinary breaks contain at most one announcement, respecting the station's minimum gap. **Announce next** activates and prioritises a saved message, bypassing the gap while retaining validity and targeting. It does not interrupt a track or start a dormant station. Missed repeats never build a backlog.

Pausing, editing or deleting a message invalidates its prepared speech. Expired messages are skipped. Preparation failure leaves music playing and permits preparation at a later opportunity within the validity window. Once any audio has aired, an interrupted message counts as delivered for repeat spacing; retry it explicitly with **Announce next**. Its saved version, spoken text, presenter and outcome remain in transmission history and Timeline. A restart marks unfinished transmissions as interrupted and does not automatically replay a partial one-off message.

Recorded announcements and advertising audio are reserved for a later advertising feature.

## Let listeners take part

Enable track requests or shoutouts on the shows where you want participation. Listener pages offer the relevant choices, including consent for names and messages to be read on air.

Submissions are reviewed for suitability. Requests must fit the show’s eligible music and timing; submitting or accepting a request does not guarantee airtime. Use **Audience** to inspect contributions and available decisions.

## Keep an eye on the broadcast

- **Overview** shows measured output status, current and next audio, listeners and the upcoming programme.
- **Mixer** controls music and DJ levels. Auto level can reduce differences between recordings and applies at the next track.
- **Timeline** shows recorded broadcast activity and spoken text, with filters and CSV export.
- **Incidents** helps explain current faults and past recoveries. Monitoring and retries run automatically.

Dormant means there are no listeners. Delayed or unavailable AI preparation can leave music playing while presentation recovers. History describes server output; device buffering affects when listeners hear it.

For public visibility and stream addresses, see [Listening and sharing](listening.md). For operational problems, see [Hosting and maintenance](hosting.md).

[All guides](README.md)
