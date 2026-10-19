# Shared HTML and CSS components

StationOS components are semantic server-rendered HTML plus shared CSS. Feature
code supplies content and domain behaviour; it should not redefine basic fields,
form actions, results framing or feedback colours.

## Panels

`section` is visually neutral. Opt into `.panel` only when a surface groups a
separate task. Use `.form-panel` with `.panel` for the standard editing width.
Existing specialist layouts (broadcast cards, calendar, mixer, player and public
programme) retain their own composition classes. Nested semantic content does not
need another panel. Audience filters sit outside its results surface.

## Routine form actions

Use `{{template "form-actions" (formActions cancelURL label)}}` for a routine
Cancel/Save or creation footer. `formActions` returns escaped, typed presentation
data. `templates/components.html` owns the markup. It uses:

```html
<div class="form-footer">
  <div class="form-actions">
    <a class="button secondary" href="…">Cancel</a>
    <button>Save</button>
  </div>
</div>
```

Desktop actions align right. At 650px and below, actions occupy two equal columns,
stretch to equal heights and allow wrapping labels. Keep Cancel first in DOM
order. Special footers with reset actions, public Back controls or selection
state use this same structure with their additional controls. Delete launchers
remain outside the editing panel. Dialog and single-action utility forms use
`.form-actions` without an editing divider.

## Results

`.results-panel` owns the surface, outline and responsive padding. Feature classes
such as `.table-results`, `.history-card` and `.audience-table-panel` supply only
specific layout/behaviour. Compose `results-toolbar`, a labelled scroll region or
intentional compact-record table variant, then `pagination`. Shared templates
live in `templates/components.html`, independently of catalogue pages.

The `pagination` presentation model supplies ranges, links, sizes and labels.
`Target` optionally selects a local HTMX region; empty means normal main-workspace
navigation. Every link retains a normal GET destination. Audience uses the same
model with repeated status filters, 25/50/100 choices, a 50-row default and legacy
offset-link support. Row decisions retain page size, page and statuses. Filtering
starts at page one. Out-of-range pages clamp and reload the correct rows.

At 700px and below, numbered pages become Previous / Page X of Y / Next.
Page-size choices wrap onto a labelled toolbar row and remain reachable. Paging
controls have 44px targets. Omit pagination for a single page. Wide Audience tables
scroll inside their labelled region; history/incident mobile records keep their
specialist presentation. Paging moves focus to the results heading; quiet live
updates preserve focus and scroll.

### Table frame

Every data table uses this shared HTML/CSS scaffold, including Audience,
Catalogue, playlist tracks, schedule agenda, Timeline, Incidents and Prompts:

```html
<div class="table-scroll table-outline" tabindex="0" role="region" aria-label="Descriptive table name">
  <table>…</table>
</div>
```

`.table-outline` owns the curved border, surface and clipping. `.table-scroll`
owns keyboard focus and local scrolling. Feature classes may set columns or a
compact mobile record layout; they must not remove the frame's border or radius.
The results toolbar and pagination sit outside this frame, inside `.results-panel`
when the screen needs them. Empty results reuse `.table-outline`. Assistant-rendered
Markdown tables also use this frame within the conversation; model-supplied raw
HTML remains disabled.

## Status, feedback and controls

`.status`, `.badge`, `.dj-card-status`, `.audience-status-badge` and
`.incident-status` share badge anatomy. Domain classes retain their meanings and
state colours. Use `--tone-error-*`, `--tone-warning-*`, `--tone-neutral-*`,
`--tone-info-*`, and `--tone-success-*` for repeated semantic colours. Add text or
accessible names; colour alone is insufficient. `.notice.error` gives validation
errors an explicit error treatment. Preserve appropriate alert/status semantics
and local request indicators.

`.icon-button` supplies a 44px hit area and 18px decorative SVG. Reuse `ui-icon`
for common symbols. Specialist player/photo visuals can retain their own sizes.
Ordinary text-control rules exclude hidden, checkbox, radio, range and file
inputs; those controls have explicit component styles. Image-picker input styling
belongs to the image editor, independent of whether it appears in a DJ form.

## Responsive verification

Check components at 320, 390, 650, 700, 768, 900, 1024, 1280 and 1440 CSS pixels,
including long text, unknown/empty results, errors and wrapped primary labels.
Check intermediate widths as well as phones/desktops: the sidebar changes the
available content width. Verify local overflow, keyboard/focus order, ordinary
links/forms, HTMX navigation/back and quiet SSE updates. Do not introduce polling.

This foundation pass preserves specialist card, mixer, calendar, player and public
show interactions. Further consolidation of fields, choice groups and specialist compositions
should follow the same ownership rules.

### Foundation verification, 18 October 2026

The priority component changes were checked in an isolated local instance with
synthetic data. Browser-rendered HTML/CSS fixtures covered new station, station
Settings, Audience, Overview, Add source, Create DJ, public station, empty playlist
Tracks and empty Timeline at 320/768/1440px. Document width matched viewport width
on all 27 combinations. Additional form and Audience checks covered
390/650/700/900/1024/1280px. At 320px a wrapping creation label and Cancel have equal
width and height. Audience row-size choices remain visible and wide data scrolls
inside the table region; numbered pagination switches to the compact phone view.
These fixture checks covered layout without scripts; the real local app separately
verified enhanced paging, result-heading focus, filtered page-size retention,
review actions preserving page/statuses, and browser back navigation. Existing
subscription regression checks cover live-update behaviour; this pass did not
exercise active audio or every populated specialist screen in the browser.

## Headings and navigation

`templates/components.html` owns `page-heading`, `breadcrumbs` and
`workspace-tabs`. Typed builders in `navigation.go` supply escaped presentation
values; feature templates compose these components rather than reproducing markup.

Use `heading(title)` for a plain heading or
`headingLink(title, metadata, label, destination)` for a heading with a link action.
Playlist refresh and Assistant conversation builders supply ordinary protected POST
forms, including CSRF and return/station fields. A heading has one h1, optional
wrapping metadata and a separate actions cluster. Below 700px the title occupies
its own row. Actions retain 44px targets and allow long labels to wrap.

Breadcrumbs use a labelled `nav` with an ordered list. Each item keeps its separator
with its text, long names wrap, and only the current page has `aria-current=page`.
Parent workspaces remain links, including a source's Overview from its Settings.
Public station/show breadcrumbs use this same structure; their branded heading
remains an intentional public-page composition.

Station, source, playlist and Assistant navigation all use `.workspace-tabs`.
These are ordinary links with `aria-current=page`, not ARIA tab widgets. They scroll
horizontally within their own region at every size and use 44px targets. Give each
workspace a stable `data-tab-scroll-key`; `static/navigation.js` retains its scroll
position across HTMX navigation/history and reveals the active link without
scrolling the page. Both shells load this versioned script. The station Incidents
item retains its existing stable indicator ID and subscription target.

Verify changes on the actual app pages at phone, tablet and desktop sizes,
including long names, headings with/without actions, direct loads, back/forward,
keyboard focus and protected form fallbacks.

### Navigation verification, 18 October 2026

Rendered station Settings, source Settings, playlist Tracks/Details, Assistant
endpoints and public station pages at 320, 390, 700, 768, 1024 and 1440px with long
names. All 36 checks retained the viewport width without page overflow; actions
wrapped onto a separate row on narrow screens. The shared navigation script
revealed active items on narrow direct loads. Live app checks covered station,
source, playlist and Assistant navigation, heading metadata/POST fields, main
focus after HTMX navigation and browser back. Regression tests cover full-page,
fragment and history restoration, escaped labels and the live Incidents target.

### State icons

Use the `state-icon` template for Audience review states, Timeline outcomes and incident status. The shared button has a 44px target and a 30px coloured icon mark. Hover, keyboard focus or tap shows a short state label, such as “Awaiting AI review” or “Expired”. Escape dismisses the tooltip. Additional review reasons remain available as accessible descriptions. Tooltips render outside scrolling table frames so they are not clipped.

Table frames own scrolling and header backgrounds; keep the inner table in its normal table layout. A panel’s first section heading has no extra top margin.

For short checkbox groups inside routine forms, `.choice-list.compact` grows with its rows up to 300px, then scrolls. It retains the shared choice-row, search and bulk-action styles without reserving a full library-picker height.
