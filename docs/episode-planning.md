# Episode planning

The default is a rolling episode session: prepare the DJ introduction using the
DJ personality and show prompt, then choose one upcoming recording at a time.
The same dated episode retains the theme, producer notes, exclusions, kept tracks,
actual played history and spoken links. Each new dated episode starts fresh.
No full running order is generated automatically. Intro failure does not stop
music; actual playback also opens the session for subsequent track selection.

Episode details subscribe to committed preparation status updates without
replacing editing controls. Refresh running order reads the latest saved state;
it does not start another model request. A ready changed plan is announced so
producers can save edits before loading it. Failures show a safe explanation and
retry time. No model response content or credentials are exposed in errors.

A future, unaired occurrence can be cancelled and restored from its episode page.
Cancellation preserves the template, session, notes and other dates, invalidates
in-flight planning, suppresses preparation and public contributions, and resolves
the skipped slot to the station default playlist. Cancelled dates remain visibly
labelled on the producer schedule. Started or aired episodes cannot be cancelled
or restored; their transmission history remains available.
