# Episode planning

The default is a rolling episode session: prepare the DJ introduction using the
DJ personality and show prompt, then choose one upcoming recording at a time.
The same dated episode retains the theme, producer notes, exclusions, kept tracks,
actual played history and spoken links. Each new dated episode starts fresh.
No full running order is generated automatically. Intro failure does not stop
music; actual playback also opens the session for subsequent track selection.

Episode details subscribe to committed events and automatically update status,
upcoming recordings and played tracks. There is no manual refresh control. While
a producer is editing, only read-only status, summary and played history update;
the form and its version remain intact. The latest queued snapshot applies after
the pristine form loses focus, or after successful navigation/save. Failures show a safe explanation and
retry time. No model response content or credentials are exposed in errors.

A future, unaired occurrence can be cancelled and restored from its episode page.
Cancellation preserves the template, session, notes and other dates, invalidates
in-flight planning, suppresses preparation and public contributions, and resolves
the skipped slot to the station default playlist. Cancelled dates remain visibly
labelled on the producer schedule. Started or aired episodes cannot be cancelled
or restored; their transmission history remains available.

The episode page uses the broadcast runtime as the source of truth for Up next,
matching the player. Saved choices are provisional DJ selections until queued.
Runtime queue ownership identifies the dated episode across handovers; live
updates follow broadcast events. Do not infer an exact start from a saved plan.
