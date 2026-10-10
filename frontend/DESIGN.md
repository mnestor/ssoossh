# Design System: ssoossh

Character: an instrument for handing keys to machines. Precise, quiet, and
unambiguous about the one thing to press.

Rewritten 2026-10-10 from that day's Character decisions, the token layer in
`src/app.css`, and the August and September notes this file used to carry.
Where they disagreed, the Character decisions won. The product owner's
rulings from the 2026-10-10 review are written into the sections they govern;
"Settled questions" lists the ones a later audit would otherwise raise again.
"Not yet in code" lists where the components still trail this spec.

**Using this file**

- Every colour, size, space, radius, shadow and duration in the UI comes from
  a token named here, and every token named here exists under the same name
  in `src/app.css` (or, where marked, in Tailwind's default theme).
- A value outside the system is either an **extension** (add it here with its
  reason, in the same commit that uses it) or **off-system** (flag it in the
  change and use the nearest token). Never invent a value silently.
- A new component variant is an extension too: add it to Components in the
  same change.
- Precedence: an explicit instruction from the product owner, then this
  file, then the UX Engine defaults.
- This file is versioned with the code. When a token changes, list the
  screens it affects in the commit.

## 1. Character

One decision per axis. Every later section applies these four.

| Axis   | Decision                                                                                                                                                                                                                 | Why                                                                                                                                   |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------- |
| Type   | The contrast is a change of family. IBM Plex Sans carries prose and controls; IBM Plex Mono is the voice, used for the page heading and for every identifier a reader compares. The heading is 28px against a 14px body. | One superfamily, so the two share proportions while differing in class. Mono then means one thing: "this page" or "this exact value". |
| Colour | One cool signature, cobalt, on warm stone neutrals. Cobalt marks the primary action, links, the focus ring and the current rail item, and nothing else.                                                                  | The signature is the only cool thing on the page, so the thing to press is found before anything is read.                             |
| Space  | Compressed inside, released between. Rows inside a panel sit tight with hairlines between them; panels stand `gap-8` apart.                                                                                              | The release between panels is what makes the density inside them read as intended rather than cramped.                                |
| Finish | Planes, not frames. Three planes: `ground`, `surface`, `surface-muted`. A panel is told apart by plane alone, with no border and no shadow. Radii are sharp.                                                             | A border, a shadow and a tint on one box say the same thing three times. The plane says it once.                                      |

## 2. Colour

All colours are OKLCH custom properties in the `@theme` block of
`src/app.css`, restated for dark mode in the `.dark` block. Dark mode keeps
each token's job and hue and moves its lightness.

### Tokens

| Token                          | Light                                         | Dark                                          | Job and why                                                                                                                                                   |
| ------------------------------ | --------------------------------------------- | --------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--color-ground`               | `oklch(95.5% 0.006 75)`                       | `oklch(14.5% 0.006 70)`                       | The page, behind every panel. Warm stone so the cobalt is the only cool thing on screen.                                                                      |
| `--color-surface`              | `#ffffff`                                     | `oklch(22.5% 0.008 70)`                       | A panel on the ground: a section, a row, the rail, a dialog, an identity strip, a page-level alert.                                                           |
| `--color-surface-muted`        | `oklch(97.2% 0.005 75)`                       | `oklch(26.5% 0.008 70)`                       | A tint inside a panel: hover, code wells, request-line tiles, chip fills. Never on the ground.                                                                |
| `--color-border-subtle`        | `oklch(90% 0.006 75)`                         | `oklch(31% 0.008 70)`                         | Hairlines between rows inside a panel and the edge of a value chip. Decoration only.                                                                          |
| `--color-border-control`       | `oklch(62% 0.01 70)`                          | `oklch(56% 0.01 70)`                          | The edge of something a reader operates. Held to WCAG 1.4.11 (3:1) on every plane.                                                                            |
| `--color-ink`                  | `oklch(22% 0.01 60)`                          | `oklch(93% 0.006 75)`                         | Primary text, and the fill of a selected toggle.                                                                                                              |
| `--color-ink-muted`            | `oklch(46% 0.012 65)`                         | `oklch(71% 0.01 70)`                          | Secondary text, labels, placeholders, hints.                                                                                                                  |
| `--color-accent`               | `oklch(50% 0.19 264)`                         | `oklch(74% 0.13 264)`                         | Cobalt, the signature. See "Where cobalt appears".                                                                                                            |
| `--color-accent-hover`         | `oklch(43% 0.19 264)`                         | `oklch(80% 0.11 264)`                         | Cobalt pressed or hovered.                                                                                                                                    |
| `--color-accent-ink`           | `#ffffff`                                     | `oklch(17% 0.03 264)`                         | Text on a cobalt fill.                                                                                                                                        |
| `--color-accent-wash`          | `oklch(94% 0.03 264)`                         | `oklch(29% 0.05 264)`                         | The ground under the current rail item, light enough that a cobalt label stays legible on it.                                                                 |
| `--color-granted` / `-surface` | `oklch(45% 0.11 150)` / `oklch(95% 0.03 150)` | `oklch(76% 0.13 150)` / `oklch(28% 0.04 150)` | Success: approved, valid, granted.                                                                                                                            |
| `--color-trimmed` / `-surface` | `oklch(50% 0.11 70)` / `oklch(95% 0.035 80)`  | `oklch(80% 0.12 80)` / `oklch(28% 0.04 80)`   | Warning: trimmed, narrowed, restricted, a live action's consequence.                                                                                          |
| `--color-danger` / `-surface`  | `oklch(50% 0.18 27)` / `oklch(95% 0.03 27)`   | `oklch(74% 0.15 27)` / `oklch(29% 0.05 27)`   | Error, denial, destruction.                                                                                                                                   |
| `--color-scrim`                | `rgb(0 0 0 / 0.55)`                           | same                                          | The dim behind a dialog or the drawer. Not restated for dark: a scrim is an absence of light in both themes, and a lighter one would read as another surface. |
| `--color-border`               | alias of `border-subtle`                      | alias                                         | No users. Do not use it; remove it the next time the token layer changes.                                                                                     |

### Measured contrast

Computed from the OKLCH values (WCAG 2.x relative luminance), not eyeballed.
Run a check against both themes before adding or moving a colour.

| Pair                             | Light | Dark  | Needs                          |
| -------------------------------- | ----- | ----- | ------------------------------ |
| ink on surface                   | 17.34 | 13.92 | 4.5                            |
| ink on ground                    | 15.20 | 16.10 | 4.5                            |
| ink-muted on surface             | 7.14  | 6.64  | 4.5                            |
| ink-muted on surface-muted       | 6.59  | 5.94  | 4.5                            |
| ink-muted on ground              | 6.26  | 7.68  | 4.5                            |
| accent on surface                | 6.26  | 7.35  | 4.5                            |
| accent on ground                 | 5.49  | 8.50  | 4.5                            |
| accent on accent-wash            | 5.24  | 6.08  | 4.5 (the narrowest text pair)  |
| accent-ink on accent             | 6.26  | 8.21  | 4.5                            |
| surface on ink (selected toggle) | 17.34 | 13.92 | 4.5                            |
| granted on granted-surface       | 6.17  | 7.05  | 4.5                            |
| trimmed on trimmed-surface       | 5.31  | 7.75  | 4.5                            |
| danger on danger-surface         | 5.60  | 5.86  | 4.5                            |
| border-control on surface        | 3.65  | 3.67  | 3.0                            |
| border-control on surface-muted  | 3.36  | 3.29  | 3.0                            |
| border-control on ground         | 3.20  | 4.25  | 3.0                            |
| border-subtle on surface         | 1.35  | 1.30  | decoration                     |
| surface on ground                | 1.14  | 1.16  | plane separation               |
| surface-muted on surface         | 1.08  | 1.12  | tint inside a panel            |
| surface-muted on ground          | 1.05  | 1.29  | never used: too close in light |

### Where cobalt appears

Exactly four jobs:

1. The primary action: a `primary` Button, one per screen region.
2. Links: text links and the back chip in `PageHeading`.
3. The focus ring.
4. The current rail item: cobalt label on `accent-wash`, with
   `aria-current="page"`.

Nowhere else: not on icons, labels, stats, selected chips, the current page
in a pager, tabs, checkboxes or decorative marks. The one exception is
`BrandMark`, which draws the product's own mark in cobalt.

### Selected, current and status

- **Selected** (a toggle: a pressed principal chip, a selected FilterChip, a
  DetailToggle segment, a segmented filter): ink fill (`bg-ink
text-surface`), the `check` glyph before the label, and `aria-pressed`.
  Never cobalt.
- **Current** (where the reader is): the rail item as above. The current page
  in a `Pager` is an ink fill with `aria-current="page"` and no glyph: it is
  a location, not a choice.
- **Status** colours are semantic only. `granted` means approved, valid or
  granted; `trimmed` means narrowed, restricted or a warning; `danger` means
  an error, a denial or destruction. They never mark "configured", "came from
  LDAP", "overridden" or emphasis; those are `ink-muted` text, or a label.
- Every state also carries a glyph or a word. Colour is never the only
  signal (WCAG 1.4.1).

## 3. Typography

### Families

- **IBM Plex Sans** 400, 500, 600 (`@fontsource/ibm-plex-sans`), token
  `--font-sans`. Prose, labels and controls.
- **IBM Plex Mono** 400, 500, 600 (`@fontsource/ibm-plex-mono`), token
  `--font-mono`. The voice.

Both are self-hosted and imported at the top of `src/app.css`, with
`system-ui` and `ui-monospace` fallbacks. No CDN, no Google Fonts.

### Where the mono goes (decided 2026-10-10)

Mono marks "this page" or "this exact value", and nothing else.

- **Mono:** the `PageHeading` `h1`; the sign-in heading; and every value a
  reader compares character by character or copies into a terminal or a
  config file: principals, usernames and account names, key ids, serials,
  fingerprints, public keys, request, certificate and enrollment ids, IP
  addresses, hostnames, commands, config keys, claim names, LDAP DNs and
  attribute names, extension and option names.
- **Sans:** everything else, including section titles, `SectionLabel`,
  `.data-table` column heads, rail group heads, buttons, chips that carry
  words, status badges, timestamps, durations, counts, display names and
  email addresses.

Timestamps and durations are sans with `tabular-nums`: they are read, not
copied, and `formatGoDuration` already turns `8h0m0s` into `8h`.

### Scale

Every size is a `--text-*` token with a utility of the same name.
`--text-*` and `--leading-*` are Tailwind v4's own namespaces; a size added
under any other name emits no utility.

| Token            | Size | Use                                                                                       |
| ---------------- | ---- | ----------------------------------------------------------------------------------------- |
| `--text-micro`   | 10px | Uppercase micro-labels inside a chip.                                                     |
| `--text-meta`    | 11px | Section titles, `SectionLabel`, table column heads, rail group heads, eyebrow-sized meta. |
| `--text-xs`      | 12px | Helper text, chip labels, footer.                                                         |
| `--text-dense`   | 13px | The body of a dense row, detail values, table cells.                                      |
| `--text-sm`      | 14px | Body text. The size everything else is measured from.                                     |
| `--text-base`    | 16px | Simplified-level facts and request-line values; inputs on touch.                          |
| `--text-lg`      | 18px | Prominent labels; rarely needed.                                                          |
| `--text-xl`      | 20px | The page heading below `sm`.                                                              |
| `--text-2xl`     | 24px | The sign-in heading. Tailwind's default, not restated in `app.css`.                       |
| `--text-display` | 28px | The page heading above `sm`. The largest size in the app.                                 |

`micro`, `meta` and `dense` carry no paired line height; the row sets it.

### Weights, leading, tracking

- Weights: 400 body; 500 buttons and row titles; 600 headings, section
  titles, labels and table heads. `--font-weight-bold` (700) has no use; a
  heading is 600.
- Leading: `--leading-tight` 1.25 for headings and dense rows,
  `--leading-snug` 1.375 for small blocks, `--leading-normal` 1.5 for body,
  `--leading-relaxed` 1.625 for long notices.
- Tracking, two values and one exception:
  - `--tracking-heading` -0.02em: the two display headings, in the mono.
  - `--tracking-label` 0.06em: small uppercase labels and chips.
  - `tracking-widest` (Tailwind's 0.1em): the widest uppercase labels,
    `.data-table` column heads and rail group heads.
  - The console pairing-code input spaces its characters at 0.2em so a code
    can be read back one character at a time. Letter-spacing doing a
    different job, not a fourth step.

## 4. Spacing and layout

Base unit 4px, Tailwind's spacing scale.

### Rhythm

| Where                                 | Value                                                                  | Why                                                           |
| ------------------------------------- | ---------------------------------------------------------------------- | ------------------------------------------------------------- |
| Inside a control or chip              | `gap-1.5` to `gap-2`                                                   | Glyph and label read as one thing.                            |
| Rows inside a panel                   | `gap-2` / `gap-3`, or `divide-y divide-border-subtle` with `py-2` rows | Compressed: a panel is one group.                             |
| Rows inside a panel, simplified level | `gap-4`, facts at `text-base`                                          | Fewer facts, so each gets more room.                          |
| A list of row panels                  | `gap-2`                                                                | Each row is one event among several.                          |
| Section panel padding                 | `p-5`                                                                  | One value for every `PageSection`.                            |
| Row panel padding                     | `px-4 py-3`                                                            | One value for every row; the certificate rows already use it. |
| Between panels                        | `gap-8`, set by `PageShell`                                            | Released: the contrast with the inside is the rhythm.         |

### Layout constants

The only values allowed as bracketed utilities. Anything else bracketed is
off-system.

| Constant               | Value                                                    | Where                                           |
| ---------------------- | -------------------------------------------------------- | ----------------------------------------------- |
| Rail width             | 244px expanded, 60px collapsed                           | `AppRail`                                       |
| Shell `focus`          | 560px                                                    | sign-in, console code, error page               |
| Shell `wide`           | 1120px                                                   | every page inside the app (default)             |
| Shell `full`           | no cap                                                   | admin tables: users, user detail, audit         |
| Aside column           | 280px above `xl`                                         | `PageShell` `aside`                             |
| DetailRow label column | 140px (`8.75rem`)                                        | `DetailRow` above `sm`                          |
| Prose measure          | `70ch`                                                   | paragraphs and form columns inside a wide panel |
| Sign-in column         | 380px                                                    | `/login`                                        |
| Dialog panel           | `max-w-md` (ConfirmModal), 520px (ConsentModal)          | dialogs                                         |
| Field cap              | 420px                                                    | the select and email field in `ApprovalView`    |
| Row grid               | `minmax(0,1fr) minmax(0,1.1fr) minmax(0,1fr)` above `xl` | `CertRow`, `DeniedRow`                          |

### Breakpoints

- `sm` 640px: phone to tablet, inside a component.
- `lg` 1024px: the rail becomes a column instead of a drawer.
- `xl` 1280px: a certificate row's fields become aligned columns. The fold is
  at `xl`, not `lg`, because the rail takes 244px: 1280px is where the page
  column reaches about 980px.

## 5. Shape and elevation

### Planes

| Plane           | What sits on it                                                                                                                                                                                                                                               | Why                                                                     |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `ground`        | Only `surface` panels.                                                                                                                                                                                                                                        | The page.                                                               |
| `surface`       | Section panels (`PageSection`), row panels (`CertRow`, `DeniedRow`, `ServiceCodeRow`, `ServiceAccountRow`), `EmptyState`, `LoadingBlock` row shapes, tables, identity strips, page-level `info` alerts, the rail, the narrow-viewport bar, dialogs, popovers. | One device for "this is a thing on the page".                           |
| `surface-muted` | Hover states, code wells, request-line tiles, chip fills, the directory probe box, `info` alerts inside a panel.                                                                                                                                              | A tint inside a panel. On the ground it measures 1.05:1 and disappears. |

Rule: anything that sits directly on the ground is `bg-surface`; anything
tinted is inside a surface panel. A card inside a card says the inner thing is
separate from the page, which is the one thing it is not, so nothing inside a
panel gets a frame: a `dl` uses `divide-y divide-border-subtle`, a
`SectionLabel` opens a sub-group.

### Borders: three jobs

1. A hairline between rows inside a panel: `border-subtle`.
2. The edge of a control a reader operates (a field, a ghost button, a pager
   control, an unselected chip): `border-control`, 3:1 or better on every
   plane.
3. The edge of a value chip (`MonoChip`): `border-subtle`. A set of
   principals has to show where one value ends; a fill alone is 1.08:1.

Nothing else takes a border: no panel edges, no rail edge, no header or
footer rule, no alert outline, no side stripe.

### Radii

| Utility                  | Token                      | Value | For                                                       |
| ------------------------ | -------------------------- | ----- | --------------------------------------------------------- |
| `rounded` / `rounded-sm` | `--radius` / `--radius-sm` | 3px   | Chips, badges, `TypeBadge`, `StatusBadge`                 |
| `rounded-md`             | `--radius-md`              | 4px   | Controls: buttons, fields, segmented controls, pager      |
| `rounded-lg`             | `--radius-lg`              | 6px   | Panels: sections, rows, strips, alerts, tables            |
| `rounded-xl`             | `--radius-xl`              | 8px   | Dialogs                                                   |
| `rounded-full`           |                            |       | Only a circular glyph container: `EmptyState`'s icon disc |

Sharper than Tailwind's defaults on purpose: a technical finish.

### Shadow

One token, `--shadow-overlay`, for a layer that floats over a panel of the
same plane: the rail's identity drop-up and any future menu or popover.
Panels never take it. A dialog does not need it: the scrim separates it.

## 6. Motion

Two durations, two curves, one rule: **everything that appears has to
disappear on purpose.**

- `--duration-enter` 220ms, eased out (`cubicOut`): slow enough to follow,
  short enough not to wait on.
- `--duration-exit` 130ms, eased in (`cubicIn`): about 40% faster, because a
  reader who dismissed something has stopped looking at it.
- `linear` only for a spinner and the skeleton shimmer.

The same two numbers live in `app.css` (for CSS) and as `ENTER_MS` /
`EXIT_MS` in `#lib/motion` (for Svelte transitions), and must agree. Use
`enterMs()` / `exitMs()` from `#lib/motion`, which return 0 under reduced
motion, rather than the constants.

| Surface                               | Entrance                        | Exit                                                             |
| ------------------------------------- | ------------------------------- | ---------------------------------------------------------------- |
| Navigation drawer (below `lg`)        | fly from -260px plus scrim fade | the same, reversed                                               |
| Rail identity drop-up                 | scale from 0.96 plus fade       | the same, reversed                                               |
| `ConfirmModal` / `ConsentModal` panel | scale from 0.96 plus fade       | the same, reversed                                               |
| `.modal-dialog::backdrop`             | `@starting-style` fade          | none: callers unmount the dialog, so the backdrop leaves with it |
| `Button` press                        | `active:scale-98` at 100ms      | released at the same speed                                       |
| `StatusBadge` signing spinner         | `motion-safe:animate-spin`      | n/a                                                              |
| `.skeleton` shimmer                   | `linear` sweep                  | n/a                                                              |

`RailGroup` expands with no transition: a height animation would fight the
rail's width transition on collapse. Only its caret moves.

**Reduced motion** has two halves because there are two engines. A block in
`app.css` sets every animation and transition duration to `0.01ms` (near zero
rather than none, so `animationend` still fires; `::backdrop` is named
because `*` does not match it). `enterMs()` and `exitMs()` cover Svelte.
`.skeleton` is the exception: its sweep stops outright, because a near-zero
infinite loop is a strobe.

## 7. Interaction

- **Targets: 36px with a mouse, 44px on touch.** One token,
  `--spacing-target`: 2.25rem, raised to 2.75rem under
  `@media (pointer: coarse)` in `app.css`. Every standalone control takes
  it: buttons, every chip a reader presses (filter, principal, extension),
  segmented controls, pager controls, icon buttons (`size-target`), rail
  rows, menu rows, checkbox and radio rows (the whole label is the target),
  the copy button and the back chip. Use `min-h-target` (and
  `min-w-target` where the control is narrow); no control states its own
  height. The glyph inside stays at its size; the target grows around it.
  Inline links inside a sentence are exempt. Why: approvals happen in a
  desktop browser, where 36px keeps rows dense and clears WCAG 2.2's 24px
  minimum; the rare phone (a console login) gets the touch size from the
  same token, on every control at once.
- **Focus:** the base layer's `:focus-visible` outline (2px cobalt, 2px
  offset) is the focus ring. Never cancel it with `focus:outline-none`:
  utilities are emitted in a later layer and silently win.
- **Hover:** a surface panel or control tints to `surface-muted`. A primary
  button moves to `accent-hover`.
- **Press:** `Button` scales to 98% for 100ms.
- **Disabled:** `opacity-50` and a default cursor. Disabled controls are
  exempt from text contrast, but a disabled control is never the only
  explanation: the reason sits beside it.
- **Busy:** `Button busy` swaps the label for its busy wording and blocks a
  second press. `ConfirmModal` takes `busy` and `busyLabel`.
- **Selected and current:** see Colour.

## 8. Iconography

Icons come from `@tabler/icons-svelte` through `Icon.svelte`, which maps
names to components and sizes. The `iconComponents` map is the whole
vocabulary; an unmapped name renders a visible red `?`.

### Rules

- **Noun, then state.** A state is a modifier on a noun, not a different
  noun: `certificate` / `certificate-off`, `clock` / `clock-cancel`,
  `filter` / `filter-off`, `copy` / `copy-check`. Learn the noun, get every
  state of it.
- **One glyph, one job.** No glyph appears in two semantic domains. When
  adding an icon, find the noun in the map and take its modifier; add a new
  noun only when the app gained a new object.
- **Severity walks the road-sign ladder:** `info-circle`, `alert-triangle`,
  `alert-octagon`. A triangle asks the reader to act; it is not for a settled
  fact like an expired certificate.
- Decorative icons are `aria-hidden`. A meaningful one gets its words in the
  document (`sr-only`), not an `aria-label` on a bare `span`, which browsers
  drop.

### Vocabulary

- **Certificate types** (`TypeBadge`, `TypeChip`, type filters): `id-badge`
  user, `terminal-2` PAM, `server-cog` service, `device-desktop` console.
- **Request status** (`StatusBadge`): `hourglass-high` pending, `loader-2`
  signing (the only animated icon), `circle-check` approved, `circle-key`
  enrolled, `circle-x` denied, `clock-cancel` expired, `alert-octagon`
  failed.
- **Certificate validity** (`CertRow`, status filters): `certificate` valid,
  `certificate-off` expired.
- **Alert severity:** `info-circle`, `alert-triangle`, `alert-octagon`.
- **Filters:** `filter-off` for the "All" / "Any" / "Both" chip.
- **Selected toggle:** `check`. It means "chosen" and nothing else.
- **Copy** (`CopyableId`): `copy`, then `copy-check` once copied (present in
  `@tabler/icons-svelte` 3.48.0; add it to the map).
- **Rail destinations:** `layout-dashboard` Dashboard, `history` History,
  `key` Service codes (own), `keyboard` Console login, `shield-lock` the
  Admin group, `users` Users, `file-certificate` Certificates, `circle-key`
  Service codes (admin, a different enclosure of the same noun, because a
  collapsed admin row has no visual parent), `adjustments-horizontal`
  Config, `address-book` Directory, `braces` Claims echo, `logs` Audit log,
  `activity-heartbeat` Diagnostics, `user-cog` Preferences.
- **Account, theme and chrome:** `user-circle` identity, `logout`, `sun` /
  `moon` / `device-laptop` theme states (a laptop, because a monitor is what
  a console certificate is), `menu-2` drawer trigger, `layout-sidebar` rail
  width, `chevron-down/left/right/up`, `arrow-right` forward in a primary
  action, `search`, `x` clear.
- **Detail rows:** `user` a person, `clock` a span of time.
- **Fallback:** `help-circle` for a certificate type outside the four. It
  marks a contract break.

### Sizes

`sizeMap` in `Icon.svelte`: `xs` 12px, `sm` 16px, `md` 20px, `lg` 24px,
`xl` 32px. Matching `.icon-*` utilities live in `app.css` for use outside the
component.

## 9. App shell and page structure

### The shell

One navigation surface: the left rail, `AppRail.svelte`, owned by the root
layout.

- **Above `lg`** the rail is the chrome; there is no header. It holds the
  brand, every destination, the admin group and the identity.
- **Below `lg`** the same component is an off-canvas drawer behind a slim
  `surface` bar carrying only the trigger, then the brand. The trigger names
  the state it moves to, so `aria-expanded` and its name agree. The scrim
  dismisses the drawer with a pointer and is `aria-hidden`; Escape is the
  keyboard path.
- **Width:** 244px expanded, 60px collapsed. Collapsing hides labels
  visually but keeps them in the DOM, so a collapsed rail is still named.
  The preference is `ssoossh:rail-collapsed` in `lib/rail.svelte.ts`; the
  drawer's open state is a separate, unpersisted field, and the drawer
  ignores a desktop collapse because the expand control is hidden there.
- **Width control:** in the brand row. Expanded, a `layout-sidebar` button at
  the row's end (hidden below `lg`). Collapsed, the brand mark itself is the
  button, and home gives up its link: Dashboard is the next row down.
- **Identity:** the rail's bottom row opens a drop-up (`RailUserMenu`)
  holding account, preferences, the theme control and sign out. It is a
  disclosure, not `role="menu"`, because it does not implement arrow-key
  roving. It opens upward when expanded and flies out to the right when
  collapsed. Escape is handled on its own wrapper so it closes before the
  drawer does. The trigger carries the current state for every page behind
  it.
- **Admin group:** gated on `is_auditor`, open by default, remembered shut
  (`ssoossh:rail-admin-open`), forced open on any `/admin` route.
- **Focus routes** render with no rail: `/login`, `/c` (a console code) and
  `/approval-unavailable`, plus any screen for a signed-out visitor.
  `isFocusRoute` in `lib/nav.ts` decides, matching on a path boundary so
  `/console` is not `/c`. The approval page is inside the app: reaching it
  means signing in.
- **Model:** `lib/nav.ts` holds every destination. Admin entries are plain
  `href`s rather than `resolve()`, because admin sections land on separate
  branches and `resolve()` is typed against the routes in the tree it is
  built in; the lint exemption lives in `RailItem.svelte`.

### Page structure

- **`PageShell`** is every page's container and owns its width: `focus`,
  `wide` (default) or `full` (see Layout constants). No page spells out its
  own `max-w-`. Optional `aside` column beside the main one above `xl`.
  `center` is for sign-in only; every other page starts at the top so it
  does not move as content loads. `PageShell` puts `gap-8` between panels.
- **`PageHeading`** is the whole opening: an optional back chip, the `h1`, an
  optional `sub` line and an optional right-aligned `action`.
  - Exactly one `h1` per page, and at most one line above it (the back
    chip). No eyebrow.
  - The `h1` is IBM Plex Mono 600, `text-xl` below `sm` and `text-display`
    above, `tracking-heading`, `leading-tight`.
  - The title stands on its own: "All certificates", "Certificate details",
    "Service code".
  - The back chip names where the page was opened from: `back={{ href,
label, testid }}`, or `back={{ onclick, ... }}` for the one list that
    opens an account without changing route.
  - The `sub` line is a sentence about the screen, never a field lifted out
    of it. The one identifier it may carry is one stated nowhere else (the
    user page's email address). It is a snippet because two lists put a
    `<code>` in theirs.
  - `action` holds the page's destructive control (see Patterns).
- **Width is not a licence to stretch prose.** A `DetailRow` value wraps
  where it wraps; a paragraph or a form caps itself at `70ch`.
- **Sign-in** is the one page without `PageHeading`: a centred 380px column,
  `BrandMark` above a `text-2xl` mono 600 heading, addressed to someone with
  no rail and no page to go back to.

## 10. Components

Each component below states its target. "Not yet in code" lists the ones
that still trail it.

### Foundations

- **Button**: variants in a local `const variants` object.
  - `primary`: `bg-accent text-accent-ink hover:bg-accent-hover`. One per
    region.
  - `ghost`: `border border-border-control text-ink hover:bg-surface-muted`.
  - `danger`: `bg-danger-surface text-danger`, for the confirming button of
    a destructive dialog.
  - All: `inline-flex` row with a gap, `rounded-md`, `min-h-target px-4`,
    `text-sm font-medium`, `active:scale-98`, `disabled:opacity-50`. `full`
    stretches it to the column (the sign-in button). `busy` shows the busy
    wording.
- **Icon**: see Iconography.
- **PageShell**, **PageHeading**: see Page structure.
- **PageSection**: one group of fields as a panel, `rounded-lg bg-surface
p-5`, no border, opening with an `h2` (`text-meta font-semibold
tracking-label uppercase text-ink-muted`, sans) and an optional line of
  copy. A real heading, because a section is a landmark. Never nested.
- **SectionLabel**: the same small label as `PageSection`'s heading, as a
  plain `div` (or a `label` when `for` names one control), for a group
  inside a section. Not a heading: nesting `h2`s would flatten the page's
  real structure.
- **DetailRow**: a label and value, 140px label column at `text-dense`
  above `sm`, stacked below it, vertical padding only (the panel gives the
  horizontal). `mono` for identifiers. Optional leading icon (`user`,
  `clock`).
- **Alert**: `error` (`alert-octagon`, `bg-danger-surface text-danger`,
  `role="alert"`), `warning` (`alert-triangle`, `bg-trimmed-surface
text-trimmed`, `role="status"`), `info` (`info-circle`, `bg-surface` on the
  ground or `bg-surface-muted` inside a panel, `role="status"`).
  `rounded-lg`, no border. An error Alert for a failed load carries its way
  out (see States): `onretry` adds the one "Try again" ghost button, with
  the test id `<testid>-retry`.

### Rows and lists

- **Row panel**: `rounded-lg bg-surface px-4 py-3`, no border, hover
  `bg-surface-muted`, stacked `gap-2`. A row is one thing among several.
- **Rows are links**: every `CertRow` and `ServiceCodeRow` is an `<a>` with
  an href its list resolved, so middle-click, copy-link and Back all work.
- **CertRow**: one certificate as a clickable row: `TypeBadge`, subject,
  what happened and when, principals, and the validity glyph
  (`certificate` in `granted` or `certificate-off` in `ink-muted`), the one
  field that moves with the clock. Stacked below `xl`, aligned columns above.
  `showOutcome` adds the outcome glyph (`circle-check` / `circle-x`), set
  only by `/logs/me` above `sm`, the one list that mixes approvals and
  denials. Expired is not a warning triangle: it is a settled fact.
- **DeniedRow**: one refusal, aligned to `CertRow` with the outcome glyph in
  the same column, and not a link (a denial issued nothing). Its third column
  is what the request **claimed** (PAM service, terminal, remote host, or the
  reporting client), labelled "claimed" because it is self-reported. It is
  copied onto the decision at decision time, so it cannot go blank later.
- **ServiceCodeRow**: one approved enrollment: the account it mints for,
  when it was approved, what it hands out, how often it was redeemed, and an
  Active/Expired chip. Never the code.
- **ServiceAccountRow**: one service account a reader holds, opening that
  account's codes.
- **EmptyState**: a surface panel with an icon disc (`rounded-full
bg-surface-muted`, decorative), a short title, the sentence that says what
  would fill the list, and optionally one control. Two cases, always told
  apart: nothing has happened yet (say what fills it) and the filters exclude
  everything (offer Clear filters).
- **LoadingBlock**: shapes `rows`, `table`, `lines`, matching what is coming
  so the page does not jump. Nothing renders for the first 300ms (a flash
  shorter than that reads as flicker); the gate re-arms on every mount. Bars
  are `aria-hidden`; `label` adds an `sr-only` `role="status"` sentence and
  is empty by default, because lists announce through `ListStatus` instead.
- **ListStatus** (with `describeList`): announces what a list became after a
  search, filter or page turn, silent until the request settles so a
  debounced search does not announce on every keystroke (WCAG 4.1.3). Every
  searchable or paged list carries one.
- **Pager**: offset pagination for the admin lists from the server's
  `PageMeta`. Renders nothing when one page holds everything; keeps first and
  last reachable behind an ellipsis; current page is an ink fill with
  `aria-current="page"`. Plain buttons with per-page names (`Page 3`), each
  `min-h-target min-w-target rounded-md`, unselected edge `border-control`.
- **SearchInput**: debounced search reporting the trimmed term once typing
  settles and only when it changed. Enter reports immediately; the clear
  button (`x`, `size-target`) reports an empty term at once. `value` seeds the
  box and is not watched; remount with a key to reset.
- **FilterGroup**: one named group of chips ("Type: All User PAM ..."). The
  name is not decoration: below `sm` a chip is its icon alone. Every group
  has its own "any" chip (`filter-off`) rather than clearing by pressing the
  selected chip again.
- **FilterChip**: glyph, label, pressed state. `rounded`, `min-h-target`,
  unselected `border-control text-ink-muted`, selected ink fill plus `check`
  plus `aria-pressed`. Named from `sm` up; below `sm` the label is `sr-only`,
  never `hidden`. Collapsing is the component's decision, not a prop.
- **AuditTimeline**: the audit feed as sentences: who did what to whom. Each
  row holds the sentence and, pinned together as one muted group on the
  right, the action name and timestamp. Rows are ruled. Details render as an
  aligned two-column list. Unknown actions fall back to the raw name.
  Timestamps go through `formatDateTime`.

### Identifiers and badges

- **CopyableId**: the identifier a detail page is about, in full, one press
  onto the clipboard, top right of every identity strip. A `min-h-target` button
  with the value in mono and the `copy` glyph, `copy-check` once copied. The
  five-character prefix is the sub-`sm` fallback, both forms rendered and
  chosen by CSS. A rejected copy says so ("Copy failed. Select the value
  instead.").
- **MonoChip**: one mono value in a set (a principal, an IP), `rounded`,
  `bg-surface-muted`, hairline `border-subtle`, `text-xs`.
  Chips rather than a comma list so set boundaries are unambiguous.
- **TypeBadge**: the certificate type as a fixed 26px square, `rounded`,
  `bg-surface-muted`, glyph only, type in `sr-only` words. Fixed so rows
  align whatever the type is called.
- **TypeChip**: the labelled form of `TypeBadge` for detail views.
- **StatusBadge**: request or certificate status as a chip: glyph plus
  capitalised word, `rounded`, `text-xs font-semibold`, coloured by status.
  The signing spinner is the only animated glyph.
- **OptionDiffList**: granted against trimmed options, trimmed struck
  through.

### Overlays

- **`.modal-dialog`** (in `app.css`): the box every dialog uses. It undoes
  the UA `<dialog>` sizing (`fit-content`, `margin: auto`), sets the text
  colour from a token (the UA stylesheet's `CanvasText` beats inheritance),
  centres the panel and draws the scrim on `::backdrop`.
- **ConfirmModal**: the one shape a confirmation takes. A native `<dialog>`
  opened with `showModal()` (top layer, focus trap, Escape routed to Cancel)
  whose panel is `rounded-xl bg-surface p-6`, no border, no shadow: the
  consequence, a reason field, Cancel and the confirming button.
  - The reason is required whenever the server records one (every current
    caller), and the confirming button stays disabled until it is non-empty.
  - A `reasonless` mode, with no field and the button enabled, for an action
    the server takes no reason for (the live LDAP sync).
  - It captures `document.activeElement` before opening and restores it on
    teardown, because callers dismiss it by unmounting and the caret would
    otherwise land on `<body>`.
- **ConsentModal**: the blocking sign-in notice, typically a government
  system-use warning that a reader must accept before anything works. No
  visible title (the notice is the deployment's own approved wording; the
  heading is `sr-only` and names the dialog). A long notice scrolls inside a keyboard-reachable
  region so "I Accept" is never pushed off screen. Escape is blocked.
- **ExpireCodeAction**: the retire-this-code button and its `ConfirmModal`
  for a page heading's `action` slot, shared by the holder's and the
  operator's service code pages, which differ only in endpoint.
- **RailUserMenu** drop-up: `rounded-lg bg-surface p-1.5 shadow-overlay`,
  rows `min-h-target`.

### Chrome

- **AppRail**, **RailItem**, **RailGroup**: see The shell. Rail rows are
  `min-h-target rounded-md`; the current row is cobalt on `accent-wash`. A
  `RailGroup` head is a button (the group has no page of its own) labelled in
  sans `text-meta font-semibold tracking-widest uppercase text-ink-muted`.
- **BrandMark**: the deployment's `branding.logo_url` when set
  (height-constrained, width free), ssoossh's own mark otherwise, so the slot
  is never empty. The mark is a shell prompt and a key inside a cloud, drawn
  to the rail's icon rules (24px box, stroke only, round caps) in cobalt.
  `static/favicon.svg` carries the same geometry knocked out of a cobalt
  tile.
- **ThemeToggle**: one button cycling system, light, dark. `rail` shape is a
  full-width row; `icon` shape is a `size-target` square for the signed-out
  header. Its name says where it is and where pressing goes.
- **Footer**: closes every page with the running version, a link to its
  release and links to the project, on the ground with no rule. Renders
  nothing while the version is unknown. Links are `min-h-target` targets.
- **DetailToggle** (to build): a two-segment control, "Simplified" and
  "Advanced", in the heading row of each covered page. Selected segment per
  Colour; writes the same preference as `/preferences`.

### Composites

- **ApprovalView**: the approval page body. One panel with labelled groups
  inside it, not a stack of cards: an approval is one decision read straight
  through. Principal and extension chips are toggles (`min-h-target`,
  selected per Colour). Approve and Deny act on one press, with no
  confirmation: the page itself is the review step (see Settled questions). The select and email field cap at 420px. See Detail levels.
- **ServiceCodeFacts**: the identity strip and facts shared by both service
  code pages: type, status, id, principal, certificate life, key id, bound
  key, validity, approver, options.
- **ServiceCodeDetail**: the body of `/service-codes/<id>`: what a
  redemption grants, the options fixed at approval, the code's dates, its
  redemption log, the other holders and the notification address. Never the
  code. The server caps the log at the newest 100 rows and reports the true
  total, so the page says it shows a slice.
- **AdminServiceCodeDetail**: the same enrollment as an operator sees it,
  with the approver's name and address and an editable notification address.
  The detail arrives as a prop: the admin endpoint is audited, and fetching
  again on mount would write a second view event.
- **AccountHoldersPanel**: who else holds a service account.
- **RedemptionHistory**: the redemption log rows, with the "showing N of
  total" note.

### Component classes in `app.css`

Component styles rather than utilities, because their rules reach
descendants. A page can still override one property with a utility.

- **`.data-table`**: the admin tables. Column heads are sans `text-meta`, 600,
  `tracking-widest`, uppercase, `ink-muted`, with a hairline under them;
  cells are `text-dense`, `px-3 py-2`, hairlines between rows and none after
  the last, `tabular-nums`. Headers are `<th scope="col">`. The table sits
  in a surface panel; it has no frame of its own.
- **`.skeleton`**: one loading bar in `border-subtle`, with a `linear` sweep
  that stops outright under reduced motion.
- **`.modal-dialog`**: see Overlays.
- **`.skip-link`**: brings the skip link into view on focus.
- **`.icon-xs` to `.icon-xl`**: icon sizes outside `Icon`.

## 11. Patterns

### States

Every surface that fetches or submits handles its states explicitly.

- **Loading:** a `LoadingBlock` in the shape of what is coming.
- **Empty:** an `EmptyState`, telling "nothing yet" from "filtered out".
- **Error:** an error `Alert` saying what failed, with a way out.
  - A failed load carries "Try again", which re-runs the load.
  - A detail page keeps its `PageHeading` and back chip in every state.
  - A page that a retry cannot fix (an expired or claimed approval) links to
    the dashboard.
  - The empty state never renders under a load error.
  - A load clears the previous error when it starts.
- **Partial:** say what is missing. The redemption log is omitted for a 404
  or 403 (the facts stand without it); a network failure says the log could
  not load.
- **Stale:** a list guards against out-of-order responses (a load counter),
  so fast typing never lands an older answer last.
- **Refresh after an action:** refresh the data in place. Do not swap the
  page for a loading state, which would unmount an open dialog and drop
  focus.
- **Success:** say what happened in the place it happened ("Saved",
  announced through `role="status"`).

### Destructive actions

An action that ends something (disabling an account, re-enabling one,
retiring a service code) lives in `PageHeading`'s `action` slot and opens a
`ConfirmModal`: the consequence, a required reason, Cancel beside the
confirming button. A button where the eye starts is found without scrolling,
and the dialog keeps the reason field from competing with the page.

A live LDAP sync is not an ending: it stays in the directory page's Sync
section, defaults to dry run, shows the live-pass warning when dry run is
off, and confirms through a reasonless `ConfirmModal`.

### Lists

- **The two certificate lists are one design.** `/logs/me` and
  `/admin/certificates` open the same way: search, then the filter groups on
  one line, then rows, then paging. `#lib/filters` states the vocabulary once
  (`typeFilters`, `statusFilters`, `outcomeFilters`). Groups are ordered by
  how often a reader reaches for them: Type, Status, then Outcome (history
  only, because a denial writes no certificate). Outcome's third option is
  "Both", because the type group already has an "All".
- **Filtering is server-side**, under the same parameter names (`q`, `type`,
  `status`), so narrowing asks the server rather than sieving loaded rows.
  An unrecognised value is dropped, not refused.
- **The outcome group picks the endpoint:** Approved reads certificates,
  Denied reads decisions, Both reads each and interleaves by time. A validity
  filter returns no denials.
- **Narrow screens:** below `sm` there is no outcome group and `/logs/me` is
  pinned to approvals, so a row never carries an unlabelled outcome. The
  width is stated once in `#lib/viewport` (`NARROW_QUERY`, `watchNarrow`).
- **Paging:** the admin lists page by offset with `Pager`; the caller's own
  history by cursor with "Load more".
- **Service codes:** live codes first, expired ones under their own label
  rather than dropped, because a job that stopped working is explained by the
  code beneath it. Opening an account filters the same list by shallow
  routing (`page.state.accountName`, `?account=` as the pasted-link
  fallback); opening a code navigates to `/service-codes/<id>`.

### Detail pages

- **Identity strip:** the first panel, `bg-surface`, carrying
  the type chip, the status and the full id (`CopyableId`, top right), and no
  heading: it is the page's subject, not a part of it.
- **Width:** `wide`, the width of the list that opened it, so the left edge
  does not move on every click.
- **Back chip:** names the list the reader came from, chosen from a `from`
  search parameter that is only ever looked up in a fixed table (`history`,
  `dashboard`, `admin`), never followed as a destination. No `from` falls
  back to the reader's own history.
- **Certificate details** (`/certs/<id>`): readable by the approver of the
  underlying request and by auditors and admins; "not found" and "not
  allowed" both return 404, so existence does not leak. Sections:
  - The certificate: principals, key id, serial, fingerprint, the valid
    period as one row (both ends plus what is left, with the validity
    glyph), and extensions and critical options as rows (stated "None"
    when empty, the whole pair dropped for PAM and console certificates).
  - The decision: who and when as one row ("approver at time", time
    muted), source address, approver groups, options granted, lifetime
    policy.
  - What the requesting host claimed about itself, marked as reported by
    the client.
  - For a service certificate, the redemption that produced it and a link
    to its code.
- **Lifetime policy rows:** the server records Go `time.Duration` strings
  (`8h0m0s`); `formatGoDuration` renders them like every other lifetime
  (`8h`, `1h 30m`, `7d`). Unparseable or sub-second values pass through.
- **Option columns** arrive decoded (an array and an object), filled only by
  the detail endpoint; `CertificateService.GetByID` must name both columns in
  its `Select`.
- **Service code pages** resolve the id from the caller's own enrollment
  list, so one message covers "absent" and "not yours".

### Timestamps and serials

- `formatDateTime` names the timezone it rendered in, and so does the
  client's expiry line: these times are read against `ssh-keygen -L` and
  host logs in other zones. (`timeZoneName` cannot combine with
  `dateStyle`, so the components are spelled out.)
- Certificate serials cross the wire as decimal strings (`json:",string"`
  and `tstype:"string"`), never numbers: 63 random bits exceed
  `Number.MAX_SAFE_INTEGER` and would round silently.

### Forms

- Every control has a name from a `<label>`, a wrapping label or
  `aria-label`. A placeholder is not a name.
- Labels sit above their field; a field caps its width to what it holds.
- A reason the server records is required and validated before submit.
- Checkbox and radio rows make the whole label the target (`min-h-target`).

## 12. Detail levels: simplified and advanced

A per-reader preference for the approval page and the three information
pages (certificate, service code, admin service code). `advanced` is the
default and is the full page. `simplified` keeps what a reader needs to
decide or to understand the result and drops the diagnostic detail an expert
compares.

- **Store:** `src/lib/detail.svelte.ts`, localStorage `ssoossh:detail`,
  modelled on `rail.svelte.ts` (one frame of the default before it is read
  is accepted).
- **Controls:** a "Display" section on `/preferences` and a `DetailToggle` in
  each covered page's heading row. Both write the same preference.
- **Never hidden at either level:** a trimmed critical option (shown in
  simplified as a one-line warning naming it), the client-policy forbidden
  note, the console remote-host warning, the own-account notice, blocked and
  outcome alerts, action errors, the "reported by the client" marking on any
  client-reported value still shown, a skewed host clock, the pam-headless
  note ("this looks like a script or a service"), and expired or active
  status. A section whose only remaining content is one of these keeps it.
- **Dropped in simplified:** request, certificate and enrollment ids; key
  ids, serials and fingerprints; public keys and host trusts; process ids,
  platform and client rows; registered IPs; request and decision timestamps
  other than validity; approver groups, lifetime-policy detail and
  granted-option echoes; critical options with nothing trimmed; non-extension
  option chips (`from=`, `command=`, `no-touch-required`); the redemption
  history; and the approval page's decision record (Decision, Decided by,
  Email, From IP, Decided at). Who decided is history, not part of a
  decision: `/certs/<id>` keeps "Approved by" or "Denied by" at both levels,
  and a decided approval page in simplified shows only its outcome alert.
- **Kept in simplified:** the claimed-context rows (Service, Terminal,
  Remote host, Invoked by), "Principals granted" on `/certs/<id>`, and the
  "View the code this came from" link on a service certificate.
- **Simplified approval:**
  - Opens with one request line in place of the description and the
    Account, Command, Host and Requested-from rows: three `surface-muted`
    tiles side by side (wrapping on a phone), each a sans `text-meta`
    uppercase eyebrow over a `text-base` mono value: **Who**
    `user@source-ip`, **Runs** `command`, **On** `host`. (The eyebrow was
    mono in the first draft of this decision; the mono-scope decision makes
    it sans, since it is a label, not a value.)
  - One muted line under them: the values are reported by the client;
    approve only if you just ran it.
  - Then Principals, Valid for, the extensions the certificate will carry
    (granted only; trimmed extensions and the narrowed warning are
    advanced-only, because the approver still sees exactly what is
    issued), the remaining warnings and the actions.
  - Critical options are advanced-only unless one was trimmed.
  - "Approvable until" is advanced-only except in its last minute, when a
    live countdown sits beside the actions ("Expires in 0:42").
  - An expired request has its own notice: "This request expired before it
    was approved. Run the client again."
- **Simplified service code pages** keep principal, certificate life,
  validity, approver, granted extensions, the editable notification address
  and the redemption count; the holders list and the redemption history are
  advanced-only.
- **Simplified type:** kept facts one step larger (`text-sm` to
  `text-base`) with `gap-4` rows.
- **Hint line** closing every simplified page: "Showing the essentials.
  Switch to Advanced for ids, fingerprints and policy detail."
- **Remembered principals:** the principals an approver last approved with
  are stored per signed-in user in this browser (`ssoossh:principals:<user>`,
  `src/lib/principals.svelte.ts`). The picker pre-ticks the stored ones the
  request offers and ignores the rest; with nothing stored the existing
  default applies. Chips stay visible and editable at both levels: a
  remembered selection pre-fills, it never approves. Written on Approve only.
  Every principal is rememberable, break-glass ones included. There is no
  "forget" control: unticking a chip on the next approval is the way out.
- **Roles** (from the 2026-10-10 review): SOC may disable and enable users;
  `/api/users/me` gains `is_nss_key_admin`; NSS keys are one top-level rail
  item, read-only for auditors. Admin, SOC and auditor powers sit behind
  admin mode (`docs/proposals/admin-elevation.md`).
- **`/preferences` order:** Email notifications, Login shell (the NSS login
  shell setting), Display.

## 13. The admin area

`src/routes/admin/+layout.svelte` gates the area on auditor access and does
nothing else. The gate is display-only (the server re-checks every read),
and a signed-in identity without auditor access is told so rather than sent
to a login it already passed. Each admin page sets its own width through
`PageShell`; the rail marks the section for its own path and everything
beneath it.

- **Users** (`/admin/users`, `full`): every account, with search and a
  segmented state filter (a toggle group, per Colour). **One user**
  (`/admin/users/<id>`): the OIDC record as the ID token last carried it,
  with each field an `ldap.fields` entry overrides struck through beside the
  value used and an `oidc`/`ldap` source chip; group membership with source
  and first/last seen; the directory record, with `directory_enabled`
  separating "never enriched" from "directory off"; changed notification
  preferences; what disabled the account; the user's audit timeline. Disable
  and re-enable are heading actions.
- **Certificates** (`/admin/certificates`): every issued certificate,
  searchable by key id, principals, serial, fingerprint, owner username or
  email, filtered by type and validity, offset-paged, from
  `/api/admin/certificates/history`. Orphaned certificates still appear (a
  LEFT JOIN) so an auditor can trace them. Rows open `/certs/<id>`.
- **Service codes** (`/admin/service-codes`, and `/<id>`): every
  enrollment; the detail carries Expire for SOC.
- **Config** (`/admin/config`): every key in effect, grouped by the config
  file's sections, filtered by key or value. The server reflects over its
  own config struct (`server/config/effective.go`), so the page lists no key
  itself. Keys at their defaults are hidden until "Show unset keys" is
  checked; a typed filter overrides that. Secrets are redacted at their Go
  declaration (`secret:"true"`) and still report whether they are set.
  Keys render in mono.
- **Directory** (`/admin/directory`): LDAP sync status and the last pass's
  counts for any auditor; Sync now (dry run by default) and the read-only
  Probe for admins. The probe's three views (entry as returned, field
  mapping, merge and allowlist) are a segmented toggle group, not tabs; a
  mapped attribute carries a "mapped" label, not a colour.
- **Claims echo** (`/admin/identity/echo`, admin only): re-authenticates
  with `prompt=login` and renders the decoded ID token, each claim annotated
  by the field that reads it, plus the `fields.extra` block that would
  capture the unread ones. Nothing is stored; the token arrives in the URL
  fragment and the page clears it. The claims table is a `.data-table` built
  for a token of any shape, since the claim count, the value lengths and
  the value types all vary by identity provider and by user:
  - Every claim, with no cap and no paging: the ones a field reads first,
    then the rest alphabetically. The section description states the count
    ("23 claims").
  - Columns: Claim (mono, `wrap-anywhere`, so a long namespaced name wraps
    only when it has to), Value (mono, the remaining width, `wrap-anywhere`)
    and Used by (sans, `whitespace-nowrap`, sized to its text).
  - Values by type: text, numbers and booleans as they are; an empty string
    as `(empty string)` and null as `null`, both `ink-muted`, so the two are
    told apart; an array one item per line (a comma list hides an item that
    contains a comma); an object as JSON indented two spaces in a wrapping
    `pre`. An array past 10 items or an object past 10 lines shows the first
    10 and a "Show all N" button (`aria-expanded`) that expands it in place.
  - Used by names the reading field in plain sans text, and an unread claim
    says "unmapped" in `ink-muted`. No status colour: being read is not a
    status.
  - The table's own `overflow-x-auto` wrapper stays as a fallback; the page
    never scrolls sideways.
- **Audit log** (`/admin/audit`, `full`): the bounded table copy of the
  audit stream, with Load more. `AuditTimeline` sits in a surface panel and
  needs no frame of its own.
- **Diagnostics** (`/admin/diagnostics`, admin only): four read-only
  deployment checks run on demand (public URL reachability, proxy trust and
  client IP, security headers, CORS and edge header attribution), one panel
  per check in a single column, in the order they ran, each with an OK,
  Warning, Critical or Skipped chip and a remediation note. Skipped is not a
  pass.

## 14. Dark mode

- **Three states:** system (default, follows the OS live), light, dark.
  "Follow my OS" is a real choice.
- `src/lib/theme.svelte.ts` owns the preference (`ssoossh:theme`) and tracks
  `prefers-color-scheme` with a live listener. The root layout applies the
  resolved theme as the `.dark` class on `<html>`.
- `color-scheme` is stated per theme (`light` on `:root`, `dark` on
  `.dark`), never `light dark`, so the browser cannot paint its own defaults
  from the OS while the tokens say otherwise.
- **No flash:** an inline script in `src/app.html` resolves the theme before
  first paint. It duplicates the resolution rule because it runs before any
  module; change both together. ssoosshd injects a CSP nonce into every
  script tag (`server/frontend/frontend_included.go`).
- Storage access is wrapped everywhere: blocked site data throws, and that
  case follows the OS.
- Components need no dark-mode logic; tokens carry it. The one exception is
  a `<dialog>`'s UA `CanvasText`, which `.modal-dialog` overrides.
- Test with the toggle on system while switching the OS theme, and with each
  explicit state.

## 15. Branding and runtime configuration

- **Branding** comes from the unauthenticated `/api/branding` at startup
  (`branding.svelte.ts`: `loadBranding()`, `getBranding()`), non-blocking
  and failing closed to "no branding". The shape is `BrandingResponse` in
  `server/webtypes`: `org_name`, `logo_url`, `login_notice`.
- **Version** comes from the unauthenticated `/api/version`
  (`version.svelte.ts`: `loadVersion()`, `getVersion()`), failing closed to
  `null`, which omits the footer's build identity. A tagged build shows
  `v1.1.3` linked to its release; an untagged one shows `development
(7a3f9c1)`. `version.mode` (`server/config/types_version.go`) is `show`
  (default), `hide` (every field empty), or any other string, served as the
  version with no commit or link. The exact version and commit are enough to
  match a deployment against known issues, which is the disclosure `show`
  trades away.
- **Brand placement:** the logo (if set) before the "ssoossh" wordmark, at
  the top of the rail and at the leading edge of the signed-out header. The
  org name (if set) as a small tag after the wordmark, truncated at 6.5rem in
  the rail and 8rem in the header. Collapsed, only the mark remains.
- **Sign-in consent:** with `login_notice` set, a `ConsentModal` overlays
  the form, which stays inert, blurred and dimmed with its button disabled
  until "I Accept". The notice is typically a government system-use warning:
  nothing on the page may be used before it is accepted, and the dimmed form
  shows that rather than hiding what accepting leads to. Every "you need to sign in" path lands on `/login` first
  (`auth.goToLogin`, carrying `return_to`), and only `/login` calls
  `auth.startLogin`, so the notice always stands in front of the identity
  provider. This matters most on `/approve/<id>`, the URL most people
  arrive on, signed out.
- **Build-time environment:** only Vite development settings, chiefly
  `DEVELOPMENT_BACKEND_URL` from `.env` / `.env.local` (see
  `frontend/README.md`). Branding is never build-time.

## 16. Voice

- **Tone:** plain and exact, like a colleague who runs the system. No
  apology, no cheer, no "Oops".
- **Titles stand alone:** "All certificates", "Certificate details",
  "Service code". Nothing above the title explains it.
- **Sentence case** everywhere except the small uppercase labels, which are
  uppercased in CSS, not in the source text.
- **Buttons are verbs** naming the outcome: "Approve", "Deny", "Expire this
  code", "Re-enable", "Try again", "Clear filters".
- **Errors** say what failed and what to do next, in the reader's terms:
  "Could not load your certificates", then the reason, then "Try again".
  Never "Something went wrong".
- **Empty states** teach: say what would fill the list ("Run `ssoossh
login` to request a certificate. It appears here as soon as it is
  issued.").
- **Name things by what the reader knows:** "service code", not
  "enrollment"; "claimed", for what a client reported about itself.
- **Adjacent controls never share a word:** "Both" beside "All".
- **Times name their zone**, and a sub line is a sentence, never a field.

## 17. Accessibility

WCAG 2.1 Level AA and Section 508 are hard requirements, plus the target
rule above (36px with a mouse, 44px on touch). `make a11y` holds them.

- **Bypass:** the layout's first focusable element is a skip link to
  `#main-content`, visible on focus (`.skip-link`).
- **Names:** every control has an accessible name; see Forms.
- **Contrast:** every text pair is AA in both themes (see Measured contrast);
  every control edge is 3:1 (`border-control`).
- **Focus:** always visible; never `outline-none`.
- **Icons:** decorative ones `aria-hidden`; meaningful ones carry `sr-only`
  words.
- **Colour alone:** never. A toggle is filled when pressed and carries
  `aria-pressed`; a mutually exclusive set of views is a group of such
  toggles, not tabs.
- **Status messages:** lists announce through `ListStatus` (4.1.3).
- **Dialogs and drawers:** native `<dialog>` with `showModal()` for focus
  management and an inert background; a dialog dismissed by unmounting
  restores focus itself. The mobile drawer moves focus in on open, cycles
  Tab inside, and returns focus to its trigger on every way out.
- **Tables:** `<th scope="col">`.
- **Page titles:** every route sets `<svelte:head><title>Name ·
ssoossh</title>`.
- **Reduced motion:** honoured in both engines; see Motion.

**Keeping it true:** `make a11y` is a required CI job. `make a11y-frontend`
runs axe over every component from `src/lib/components/a11y.test.ts`; a new
component needs a case there, and a roster test fails the build when one is
missing. `make a11y-docs` runs axe over every page `astro build` writes
(`user-docs/scripts/axe-check.mjs`). Tooling reaches about a third of WCAG:
still check contrast for a new colour in both themes, and tab through every
change.

## 18. Engineering conventions

- **Test ids:** components the e2e tier selects on (`Button`, `PageSection`,
  `Alert`, `Pager`, `SearchInput`, `CertRow`, `PageHeading`,
  `ServiceCodeRow`) take a `testid` prop and render `data-testid` on their
  root. Passing `data-testid` to a component is an unknown prop that Svelte
  drops silently; `make frontend-check` catches it because `Props` are typed.
  Do not spread rest props onto the root to "fix" it: that removes the check.
- **Variant objects:** `Button`, `Alert`, `StatusBadge` and `OptionDiffList`
  map variants to classes in a local `const variants` object. Add a variant
  by extending the `variant` union in `Props` and the object, with token
  classes only.
- **Token namespaces:** `--color-*`, `--font-*`, `--text-*`, `--leading-*`,
  `--tracking-*`, `--font-weight-*`, `--radius-*`, `--shadow-*`,
  `--spacing-*` are Tailwind v4 namespaces and emit utilities. A token under any other spelling
  (`--font-family-*`, `--font-size-*`) emits nothing.
- **Tests:** `@testing-library/svelte` and `vitest`, colocated, named
  "should [action] when [condition]", one assertion where possible, external
  dependencies mocked.
- **Before a UI change lands:** check tokens first; verify light and dark;
  tab through it; run `corepack pnpm run test`, `corepack pnpm run check` and
  `corepack pnpm run build`.

## 19. Settled questions

Ruled by the product owner on 2026-10-10, each against a check an audit
runs by default. A later audit should not raise them again.

- **Approve and Deny take one press, with no confirmation.** The approval
  page is itself the review step, and a dialog in front of every approval
  trains readers to click through it. Deny sits first as the ghost button,
  Approve last as the primary.
- **The own service codes page says nothing when the caller's held accounts
  fail to load.** The accounts that appear on codes still render; only an
  account with no codes is missing. Deliberate, and commented in
  `src/routes/service-codes/+page.svelte`.
- **The sign-in form behind the consent notice stays dimmed and blurred.**
  See Branding: the notice must be accepted before anything is usable.
- **Disabled controls fade to `opacity-50`.** WCAG 1.4.3 exempts them from
  text contrast; the reason a control is disabled sits beside it.
- **Targets are 36px with a mouse and 44px on touch,** not 44px everywhere.
  Approvals happen in a desktop browser; see Interaction.

No rulings are open.

## 20. Not yet in code

The tokens and this spec are ahead of the components. Remove a line when its
change lands.

- Panels are still framed: the row components and `EmptyState` (`border`,
  `rounded-xl`), the identity strips, `Alert`, both dialogs (`border`,
  `shadow-lg`), the rail edge, the narrow bar and the footer rule.
- `RailUserMenu` uses `shadow-lg`, not `shadow-overlay`.
- `PageHeading`'s `h1` and the sign-in heading are sans `font-bold`.
- `.data-table` column heads and `RailGroup` heads are in the mono.
- Selected states are cobalt fills (`FilterChip`, `ApprovalView` chips,
  `Pager`, the users and directory segments); `FilterChip` has no `check`.
- No control uses the `target` utilities yet: chips and pager controls are
  about 28px, and buttons, icon buttons and rail rows state their own
  heights.
- `copy-check` is not in the icon map; `CopyableId` still shows `check`.
- `PageShell` puts `gap-5` between panels; several lists use `gap-2.5`.
- Chips and badges on `rounded-full`; several fields on `rounded`.
- Cobalt outside its four jobs: stat figures and LDAP override notes on the
  user page, a label and a checkbox on the directory and config pages.
- The directory probe views are tabs with no announced state, and a mapped
  attribute is marked by colour alone.
- `ConfirmModal` has no reasonless mode, and a live LDAP sync runs unconfirmed.
- The claims echo table is hand-rolled (its own frame, tinted head, green
  `rounded-full` pills), joins arrays with commas, prints objects as one line
  of JSON, and shows an empty string as a blank cell.
- The detail-level feature, `DetailToggle`, `principals.svelte.ts` and the
  Login shell and Display preference sections are not built.

## References

- [OKLCH](https://oklch.com/)
- [IBM Plex](https://github.com/IBM/plex) (OFL)
- [Tabler Icons](https://tabler.io/icons) (`@tabler/icons-svelte`, MIT)
- [WCAG 2.1 AA](https://www.w3.org/WAI/WCAG21/quickref/?currentsetting=level%20aa)
- [Svelte](https://svelte.dev/docs)
