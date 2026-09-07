# Frontend Design System

This document describes the design tokens, typography, iconography, and component patterns that maintain visual and interaction consistency across the ssoossh frontend.

## Color Tokens

The app uses a curated set of CSS custom properties defined in `src/app.css`. All colors are specified in [OKLch color space](https://oklch.com/) for perceptually uniform results. The palette is the same in light and dark modes; the mode switch inverts lightness while preserving hue and chroma.

### Light Mode (default)

- `--color-surface`: `#ffffff` — main background
- `--color-surface-muted`: `oklch(97% 0.002 200)` — the ground of every `PageSection`, the identity strip, light hover states
- `--color-border-subtle`: `oklch(88% 0.003 200)` — borders, dividers
- `--color-ink`: `oklch(20% 0.005 200)` — primary text
- `--color-ink-muted`: `oklch(45% 0.005 200)` — secondary text, placeholders, hints
- `--color-accent`: `oklch(45% 0.06 200)` — primary interactive element (teal)
- `--color-accent-hover`: `oklch(38% 0.07 200)` — accent pressed state
- `--color-accent-ink`: `#ffffff` — text on accent backgrounds
- `--color-accent-wash`: `oklch(95% 0.018 200)` — the ground under the selected rail item; a tint of the accent light enough to keep an accent-coloured label legible on it
- `--color-granted`: `oklch(46% 0.08 155)` — success/approved status (green)
- `--color-granted-surface`: `oklch(95% 0.02 155)` — granted/success backgrounds
- `--color-trimmed`: `oklch(52% 0.08 75)` — warning/restricted status (yellow)
- `--color-trimmed-surface`: `oklch(95% 0.02 75)` — trimmed/warning backgrounds
- `--color-danger`: `oklch(48% 0.13 25)` — error/denied status (red)
- `--color-danger-surface`: `oklch(95% 0.03 25)` — danger/error backgrounds

### Dark Mode

The `.dark` class on `<html>` inverts all tokens while keeping the semantic meaning intact, and flips `color-scheme` with them. See the `.dark` block in `src/app.css`.

`color-scheme` is stated per theme (`light` on `:root`, `dark` on `.dark`) rather than as `light dark`, because exactly one theme is active at a time and the class is what selects it. Advertising both would let the browser paint its own defaults — a `<dialog>`'s `CanvasText`, form controls — from the OS preference while the tokens said otherwise.

## Typography

### Typefaces

- **UI (sans-serif)**: Public Sans 400, 500, 600, 700 weights (self-hosted via `@fontsource/public-sans`)
- **Monospace**: Fira Code 400, 500 weights (self-hosted via `@fontsource/fira-code`)

Both fonts are imported in `src/app.css` and serve the entire app; no fallback to Google Fonts or CDNs. Public Sans is a highly legible, geometric UI typeface suitable for a security product. Fira Code is used for SSH keys, fingerprints, principal names, and other cryptographic data where monospace clarity is essential.

### Font Sizes

All sizes use CSS custom properties (the `--font-size-*` block in `app.css`):

- `--font-size-xs`: `0.75rem` (12px) — auxiliary labels, helper text
- `--font-size-sm`: `0.875rem` (14px) — body text, table data, secondary text
- `--font-size-base`: `1rem` (16px) — default base size
- `--font-size-lg`: `1.125rem` (18px) — subheadings, prominent labels
- `--font-size-xl`: `1.25rem` (20px) — main headings

### Line Heights & Weights

- `--line-height-tight`: `1.25` — headings, dense content
- `--line-height-snug`: `1.375` — small text blocks
- `--line-height-normal`: `1.5` — body text (default)
- `--line-height-relaxed`: `1.625` — spacious, accessible reading
- `--font-weight-normal`: `400`
- `--font-weight-medium`: `500`
- `--font-weight-semibold`: `600`
- `--font-weight-bold`: `700`

## Iconography

Icons come from [@lucide/svelte](https://lucide.dev/), wrapped in the `Icon.svelte` component for consistent sizing and labeling. The component accepts a size token and an optional `aria-label` for semantic/meaningful icons (decorative icons hide from screen readers via `aria-hidden`).

### Supported Icons

The `iconComponents` map in `src/lib/components/Icon.svelte` includes:

**Semantic / Status:**

- `alert-circle` — information or notice
- `alert-triangle` — warning or caution
- `check`, `check-circle` — success or approved
- `x`, `x-circle` — error or denied
- `clock` — pending or waiting
- `loader` — in-progress or loading

**Navigation & UI:**

- `menu` — the drawer trigger below `lg`
- `chevron-down`, `chevron-left`, `chevron-right`, `chevron-up` — directional indicators
- `arrow-right` — forward movement in a primary action (the login button)
- `layout-grid` — the dashboard, and the "All" option in a type filter
- `link` — a shareable link to the thing on screen
- `sun`, `moon`, `monitor` — the light, dark, and follow-the-system theme states
- `panel-left` — collapse or expand the rail, from the control beside the wordmark
- `log-out` — end the session

**Rail destinations:**

Each entry in the rail carries an icon, because a collapsed rail is icons
only. They are chosen to be distinguishable at 16px rather than to be
literal:

- `layout-grid` — Dashboard
- `clock` — History
- `cog` — Service codes (user), matching the `service` certificate chip, since the two name the same object; `key-round` — Service codes (admin), kept distinct so a collapsed rail does not show the same icon twice
- `monitor` — Console login
- `shield-check` — the Admin group head
- `users` — Users
- `award` — Certificates
- `sliders-horizontal` — Config
- `book-open` — Directory
- `code` — Claims echo
- `file-text` — Audit log
- `activity` — Diagnostics
- `bell` — Preferences

**Certificate Types:**

- `user` — user certificates
- `terminal` — PAM certificates
- `cog` — service certificates

**Utility:**

- `search` — the search box on a paged list
- `copy` — the copy-to-clipboard affordance on a `CopyableId`
- `server` — a host or instance (in the map; no current screen renders it)
- `zap` — generic or all-category indicator

### Size Scale

The `sizeMap` in `Icon.svelte` defines pixel dimensions:

- `xs`: 12px
- `sm`: 16px
- `md`: 20px
- `lg`: 24px
- `xl`: 32px

Icon utilities (`.icon-xs` through `.icon-xl`) are defined in `src/app.css` (the `.icon-*` block) for manual sizing if needed outside the `Icon` component.

## Component Patterns

### Test ids: the prop is `testid`, not `data-testid`

Components that the e2e browser tier selects on (`Button`, `PageSection`,
`Alert`, `Pager`, `SearchInput`, `CertRow`, `PageHeading`, `ServiceCodeRow`)
each declare a `testid` prop and render `data-testid={testid}` on their root
element themselves:

```svelte
<PageSection testid="account-identity-card">   <!-- right -->
<PageSection data-testid="account-identity-card">  <!-- wrong: silently dropped -->
```

`data-testid` on a component is an unknown prop. Svelte drops it at runtime
with no error and no console output — the attribute never reaches the DOM and
every e2e selector for it fails, which presents as a selector bug rather than
as a dropped prop. Plain HTML elements take `data-testid=` normally, which is
what makes this easy to miss two lines away.

`make frontend-check` catches it (`'"data-testid"' does not exist in type
'Props'`) because the `Props` interfaces are typed, and it is a blocking merge
gate. If an e2e selector is not matching, run `make frontend-check` before
concluding the selector is wrong.

Do not "fix" this by spreading rest props onto the root element: that makes
`data-testid` a legal prop again and removes the typecheck that currently
catches the mistake.

### Variant Objects

Variant-bearing components (`Button`, `Alert`, `StatusBadge`, `OptionDiffList`) encode their variant→Tailwind-class mapping as a local `const variants` object literal inside the component file. This is deliberately not abstracted into a shared helper to avoid creating a dependency on a component library; the pattern is readable, self-contained, and requires no wrapper.

Example from `Button.svelte`:

```ts
const variants = {
	primary: 'bg-accent text-accent-ink hover:bg-accent-hover',
	danger: 'bg-danger-surface text-danger brightness-95',
	ghost: 'border border-border-subtle text-ink hover:bg-surface-muted'
};
```

When adding a new variant:

1. Add the variant name to the `variant?: ...` union in Props.
2. Add the `variant: { className: ... }` entry to the `variants` object.
3. Include the Tailwind classes in the element using `{variants[variant]}`.

All classes should reference CSS custom properties (e.g., `bg-accent`) rather than hardcoded colors or sizes.

### The app shell

The app has one navigation surface: a left rail, rendered by
`AppRail.svelte` and owned by the root layout. Above `lg` (1024px) it is the
chrome — there is no header at all — and it holds the brand, every
destination, the theme control, the identity and sign-out. Below `lg` the
same component becomes an off-canvas drawer behind a slim bar carrying only
the trigger, so the two can never come to disagree about what the app
contains.

The rail is 244px expanded and 60px collapsed. Collapsing hides the labels
visually but leaves them in the DOM, so the accessible name stays the words
at either width and a collapsed rail is still navigable rather than a column
of glyphs. The preference is kept in `localStorage` under
`ssoossh:rail-collapsed` by `lib/rail.svelte.ts`, which also holds the
drawer's open state — deliberately a separate field, and never persisted: a
phone visitor's open drawer is not a desktop preference.

The width control sits in the brand row, right-aligned beside the wordmark.
It is the one control in the rail that acts on the rail rather than on the
app, and the head of a column is where a control for that column is looked
for. Expanded, it is a `panel-left` button hidden below `lg` — a drawer has
nothing to collapse. Collapsed, the brand mark itself is the button: at 60px
that row has one slot and it has to do both jobs.

The rail's bottom edge is one row — the identity — and everything done to
the session hangs off it in a drop-up (`RailUserMenu.svelte`): account,
preferences, the theme control, sign out. It is a disclosure, not a
`role="menu"`: the contents are ordinary links and buttons, and declaring
the menu role would promise arrow-key roving these rows do not implement.
The popover opens upward over the rail when there is width for labels and
flies out to the right when the rail is collapsed, where a 60px-wide popover
would truncate every row it exists to spell out. Escape is handled on the
popover's own wrapper rather than on the document, so the key closes the
popover first and the drawer underneath only once the popover has gone. With
those rows behind a shut popover, the trigger carries the selected state for
every page they lead to — otherwise standing on `/preferences` would leave
the rail with nothing marked.

Admin is a group inside the same rail, gated on `is_auditor`. It is open by
default and remembers being shut (`ssoossh:rail-admin-open`), and an
`/admin` route forces it open whatever was last chosen — arriving in the
admin area with the section list hidden is the one case where the stored
preference cannot be what the viewer meant. It started shut everywhere but
`/admin` at first, which read as the admin menu having gone missing. It
replaced a horizontal tab strip in the admin layout that was reachable only
from a line inside the account dropdown.

The collapsed width is a separate key, and the drawer overrides it: the
control that expands the rail is hidden below `lg`, so a drawer inheriting a
desktop collapse would be a strip of unlabelled icons with no way back.

Four routes render with no rail at all — sign-in, an approval, a console
code, the approval-unavailable notice — as does any screen for a signed-out
visitor. `isFocusRoute` in `lib/nav.ts` decides. These are single-task
screens where a column of destinations either cannot be followed or invites
the reader to wander off mid-decision. The match is on a path boundary, so
`/console` is not treated as the `/c` console-code screen.

`lib/nav.ts` holds the whole model. The admin entries are plain `href`s
rather than `resolve()`: the model names every admin section, but the
sections land on separate feature branches, and `resolve()` is typed against
the routes present in the tree it is compiled in — using it would force a
placeholder page for every absent route, and a placeholder sharing a path
with another branch's real page is a merge waiting to resolve the wrong way.
The lint rule that wants `resolve()` fires where the href reaches the DOM,
so the exemption lives in `RailItem.svelte`.

### Page Structure

Every page is a `PageShell`. It owns the container, so no page spells out
its own `max-w-`, and it names three widths rather than the nine that had
accumulated across twenty-two hand-rolled containers:

| Width   | Cap      | Used by                                              |
| ------- | -------- | ---------------------------------------------------- |
| `focus` | `560px`  | sign-in, an approval, a console code, the error page |
| `wide`  | `1120px` | every page inside the app — the default              |
| `full`  | none     | the admin tables — users, user detail, audit         |

`wide` is the default because it is what the app is: every screen the rail
navigates between, every list, and every page a list opens. `focus` is not a
narrower version of that — it is for the screens outside the app entirely,
signed out or holding a single decision and nothing else. `full` is not a
fourth number: it opts out of the cap, for the screens where a horizontal
scrollbar inside a centred column is worse than using the glass.

There was a fourth, `default`, at 760px. By the end only `/account` and
`/preferences` were still on it, which made them 360px narrower than
everything else the rail reaches: the content column jumped inward on the way
to them and back out on the way to anything else, and both are reached from
the user menu, so that happened on every visit. They are `wide` now, and the
width is gone rather than left unused — an unused width is how a fifth one
gets added.

Width is not a licence to stretch prose. A `DetailRow`'s value starts after
its 140px label column and wraps where it wraps, but a paragraph or a form
caps itself at a readable measure: the notification toggles on `/preferences`
sit in a `70ch` column inside a full-width card, because a sentence set
1100px wide is one the eye loses its place in on the way back to the left.

`PageShell` also takes an optional `aside` snippet — a secondary column that
sits beside the main one above `xl` and stacks under it below — and a
`center` flag. Only sign-in sets `center`: it is one short column with
nothing above or below it, and every other page starts at the top so it does
not move as content loads.

Every page opens with a `PageHeading`, and the whole of the opening lives in
it: an optional back chip, an accent eyebrow naming the area ("Activity",
"History", "Admin"), the page's `h1`, an optional `sub` line, and an optional
right-aligned `action`. The eyebrow is what makes a screen identifiable at a
glance without reading the title, so it is required rather than optional, and
there is exactly one `h1` per page.

All four parts are in the component because every one of them had been
rebuilt by hand somewhere, and the copies had drifted:

- The **back chip** was the same nine-class string on `/certs/<id>`,
  `/service-codes/<id>` and `/admin/service-codes/<id>` as an `<a>`, and on
  `/service-codes` as a `<button>` — each with a `-mb-2` cancelling
  `PageShell`'s gap. It is now `back={{ href, label, testid }}`, or
  `back={{ onclick, ... }}` for the one list that opens an account without
  changing route.
- The **sub line** existed in four shapes: the `sub` snippet, a `-mt-2
text-sm` paragraph after the heading, a `-mt-2 text-[13px]` one, and a
  `text-sm` one with no pull at all. The snippet is the only one now, so
  every subtitle is the same size at the same distance from its title.
- The **`sub` line is a sentence about the screen, never a field lifted out
  of it.** `/service-codes/<id>` briefly put the key ID there, which is a
  labelled row in "What it hands out" a few lines below, and
  `/admin/users/<id>` led its line with the username whenever the title was
  a display name, restating the "Username" field in the OIDC record directly
  under it. A heading that pre-announces a field the page is about to state
  properly is saying it twice and labelling it neither time. The one
  identifier a sub line may carry is one stated nowhere else — the user
  page's email address.
- The **`h1`** was written out with its four classes on five pages that had
  no eyebrow — the error page, both approval-unavailable states, and the two
  load-failure screens. A change to the heading scale would have moved most
  of the app and left those behind. They take an eyebrow now ("Error 404",
  "Approval", "Console login", "Certificate request"), which is what they
  were missing rather than a reason to skip the component.

Sign-in is the one screen that does not use it, and deliberately: it is a
centred 380px column with the `BrandMark` above a 22px `h1`, addressed to
somebody who is not signed in and has no rail, no area to name and no page to
go back to. An eyebrow and a left-aligned 26px title would break the lockup
rather than unify it.

### Breakpoints

Three, and they mean specific things:

- `sm` (640px) — phone to tablet, inside a component
- `lg` (1024px) — the rail becomes a persistent column instead of a drawer
- `xl` (1280px) — a cert row's fields become aligned columns. The fold is at
  `xl` rather than `lg` because the rail takes 244px off the front: 1280px is
  where the page column reaches about 980px and a row has space to lay its
  fields out side by side.

### Sections are subtle cards

Two frames, meaning two different things.

A **row card** — 1px border, 10px radius, `surface` (white) background — says
"this is one discrete thing among several". `CertRow`, `ServiceCodeRow` and
`ServiceAccountRow` are each one event standing beside others of the same
kind, and a list of them is a list of things that happened.

A **section card** — 1px border, `rounded-lg`, `bg-surface-muted`, `p-4`, no
shadow — says "this is one group of fields within this page". That is
`PageSection`, and it is what every screen's own content sits in. It replaced
`Card`, which was a white panel with a shadow and a ruled-off header: that
read as a component floating over the page rather than as a region of it, and
stacking a border, a shadow, a tint and a header divider said the same thing
four times. The tint alone says it once.

Inside a section, nothing gets a frame of its own. A card inside a card says
the inner thing is separate from the page, which is the one thing it is not.
So a `dl` uses `divide-y divide-border-subtle` for its rows, a `SectionLabel`
opens a sub-group, and the hairline is the only rule needed below the section
level. Two consequences worth naming: the approval screen is one card with
labelled groups inside it, not six cards, because an approval is a single
decision read straight through; and the "Last pass" block on
`/admin/directory` is flat, because the Sync section it sits in is already
the card.

Above the section level, `PageShell` puts `gap-5` between sections and
nothing else. There is no card grid: `/admin/config` and `/admin/diagnostics`
used to balance boxes two abreast by weight, which reordered content by
height. A configuration is read in the order the file is written and a
diagnostics run in the order the checks ran, so both are one column of
sections.

The identity strip a certificate or service-code page opens with — type
chip, status badge, full id — keeps the same tint and border as a section
but carries no heading, which is what marks it as the page's subject rather
than a part of it.

### Destructive actions: the heading, then a dialog

An action that ends something lives in `PageHeading`'s `action` slot, in the
page's top right, and opens a `ConfirmModal`. Disabling an account,
re-enabling one, and retiring a service code are all the same shape: a
button where the eye starts, a modal over the page naming the consequence, a
required reason, and Cancel beside the confirming button.

The alternative, which two of these used, was a panel that expanded inline
partway down the page. That put the control that ends a service code
wherever the sections above it happened to finish, and left the reason field
competing with the page behind it while it was being typed. It also meant a
reader had to scroll to discover the page had an action at all.

Every one of these reasons is server-validated (`enrollment.expired`,
`user.disabled`, `user.enabled`), so `ConfirmModal` keeps the confirming
button disabled until a non-empty one is typed. A button whose only possible
outcome is a 400 is not a button.

`ConfirmModal` is a native `<dialog>` opened with `showModal()`, which is
what gives top-layer stacking, a `::backdrop` and a focus trap; Escape fires
`cancel` and is routed to the same handler as the Cancel button. The two
hand-rolled `fixed inset-0` overlays it replaced on `/admin/users/<id>` had
none of that.

### Common Components

- **Button**: Variants: `primary` (accent blue), `danger` (red), `ghost` (outline). Always includes `disabled` state via `opacity-50`. Lays its children out as a centered `inline-flex` row with a gap, so a label plus a trailing icon needs no wrapper. `full` stretches it to the container width, for a screen whose single primary action should span the column (the login button).
- **BrandMark**: The deployment logo slot — the mark left of the "ssoossh" wordmark in the header and above the login heading. Renders `branding.logo_url` when a deployment sets one (height-constrained, width free, since most organisation logos are wide wordmarks) and ssoossh's own mark otherwise, so the slot is never empty. Takes `size` in pixels and `strokeWidth` (1.75 by default, lighter on the 40px login mark); corner rounding follows the size.

  The mark is a shell prompt and a key inside a cloud — single sign-on and an SSH session in one glyph — drawn to the same rules as the rail's icons: a 24x24 box, stroke only, round caps and joins, one colour from `--color-accent`. It replaced a check-in-circle, which was the same glyph the `check-circle` status icon uses for an approved request; a brand mark that doubles as a status icon teaches the wrong thing twice. `static/favicon.svg` carries the identical geometry knocked out of a filled accent tile, so the tab icon reads on a light or a dark tab strip without an internal `prefers-color-scheme` rule.

- **AppRail**: The app's one navigation surface — brand, the rail's own width control, destinations, the admin group, and the identity drop-up. See The app shell.
- **RailItem**: One destination in the rail. Keeps its label in the DOM when the rail is collapsed, so the accessible name never becomes an unlabelled icon.
- **RailGroup**: A named, collapsible band of rail items. The head is a button, not a link: the group has no page of its own, and giving it one would mean inventing an admin landing screen whose only content is the list already on show.
- **RailUserMenu**: The rail's bottom row — the identity — and the drop-up behind it holding account, preferences, theme and sign out. A disclosure rather than a `role="menu"`; see The app shell.
- **PageShell**: The container every page sits in. Three named widths, an optional `aside` column, and the `center` flag sign-in uses. See Page Structure.
- **PageSection**: One group of fields as a subtle card — `rounded-lg border border-border-subtle bg-surface-muted p-4` — opening with a quiet uppercase `h2` and an optional line of copy. What replaced `Card`; see Sections are subtle cards. A real heading rather than a styled `div`, because a section is a landmark a screen reader navigates by and it was the card's header, not its border, that said so. Never nested inside another.
- **Footer**: The bar closing every page — the running build's version, a link to the release it was cut from, and links back to the project on GitHub. Presentational: the build identity arrives as a prop, so the fetch happens once in the layout. Renders nothing at all while the version is unknown.
- **Alert**: Variants: `error`, `warning`, `info`. Each includes an icon and a color from the token set.
- **ConfirmModal**: The one shape a confirmation takes — a native `<dialog>` holding the consequence, a required reason, Cancel and the confirming button. Callers supply the wording and the endpoint and nothing else. See Destructive actions.
- **ExpireCodeAction**: The retire-this-code button and its `ConfirmModal`, for a page heading's `action` slot. One component for both sides of a code — an admin on `/admin/service-codes/<id>` and a holder on `/service-codes/<id>` — because the two differ only in which endpoint they call, and the reason field, the confirmation and the error wording are what is worth keeping identical.
- **StatusBadge**: Maps request/certificate statuses (pending, approved, denied, etc.) to colored pills with status-appropriate icons. Rendered capitalised — the wire value is lowercase, the label is not.
- **DetailRow**: A label–value pair for metadata lists, with optional icon and monospace rendering. A 140px label column at 13px, stacking on narrow viewports.
- **PageHeading**: The whole of a page's opening — back chip, eyebrow, `h1`, `sub` line, and a right-aligned `action` where a page's destructive control goes (see Destructive actions). `sub` is a snippet rather than a string because two lists put a `<code>` in theirs. See Page Structure for why all four parts live here.
- **SectionLabel**: The same small muted uppercase label as `PageSection`'s heading, but as a plain `div` with no frame, for a group _inside_ a section — a form group, the lifetime-policy block within a certificate's decision, the three groups inside the approval card. Not a heading, and must not become one: nesting `h2`s under each other would flatten the page's real structure. Quieter than `PageHeading`'s eyebrow, which takes the accent.
- **DeniedRow**: One refusal in a decision history, laid out to line up with the `CertRow` beside it and deliberately not a link: a denial issues nothing, so there is no certificate page to open. Its own component rather than a mode of `CertRow` because a denial genuinely has none of what a certificate row shows — no serial, no key id, no fingerprint, no principals granted, no validity window — and feeding empty strings into `CertRow` would render a row that reads like a certificate whose details nobody can find. `/logs/me` reads two endpoints for this: `/api/certs` is the certificates table and a denial never writes a row there, so `/api/decisions/denied` serves the other half and the page interleaves the two by time.
- **CertRow**: One certificate as a standalone, clickable card — type badge, subject, what happened and when, principals, and the decision badge. Stacked below `xl`, aligned columns above it: a list of rows is the same fields over and over, and stretching a stacked row only pushes the last field further from the first.
- **ServiceCodeRow**: One approved service enrollment as the same kind of card — the account the code mints for, when it was approved and what it hands out, how often it has been redeemed, and an active/expired pill. Never the code.
- **ServiceCodeDetail**: The enrollment in full, the body of `/service-codes/<id>`: what a redemption grants, the options fixed at approval, the code's own dates, its redemption log, who else holds the account, and where notifications go. Retiring the code is not here — that is the route's own action, in the heading. The server caps that log at its newest 100 rows and reports the true total, so the page says what it is showing a slice of rather than letting the last row read as the first redemption. Structurally unable to show a code.
- **AdminServiceCodeDetail**: The same enrollment as an operator sees it, the body of `/admin/service-codes/<id>` — the approver's name and address as well, and the notification address an operator can set. Expiring the code is the route's action, in the heading. The detail is handed in as a prop rather than fetched here: `GET /api/admin/enrollments/:id` is audited, and a component that fetched on mount would write a second `admin.enrollment_viewed` event for the one look the route already recorded.
- **TypeBadge**: The certificate type as a fixed 26×26 square. Fixed rather than content-sized so rows align vertically whatever the type is called, and always shown: on a row the type is the primary identifier, not decoration.
- **TypeChip**: The labelled form of `TypeBadge`, for detail views with room to name the type.
- **CopyableId**: The identifier a detail page is about, in full and one click onto the clipboard. Every identity strip carries one in the same position, top right. The five-character prefix survives only as the sub-`sm` fallback — a 36-character UUID needs about 260px at this size, which a phone does not have to spare beside the badges — and both forms are rendered with CSS choosing between them, since "does this row have room" is a question CSS answers before the first paint and script only answers after it. `/certs/<id>` used to print the raw value with no way to copy it, and the approval screen a five-character stub with the rest in a `title` tooltip, which is unreachable from a keyboard and invisible on a touch screen.
- **MonoChip**: One monospace value as a bordered chip — a principal, an IP. Chips rather than a comma-separated string so set boundaries are unambiguous.
- **ThemeToggle**: One button cycling system → light → dark, in two shapes. `rail` is a full-width row matching every other row in the rail; `icon` is the square button the signed-out header uses. See Dark Mode.
- **OptionDiffList**: Shows granted vs. trimmed options (extensions, critical options) with strike-through for trimmed items.
- **ApprovalView**: Composite component rendering a full certificate-request approval form.
- **Rows are links, not buttons**: every `CertRow` and `ServiceCodeRow` is an `<a>` carrying an href its list resolved. Opening one is a navigation to the thing's own page, so middle-click, ctrl-click and "copy link address" all work, and the browser's own Back leaves it. Both used to open a `<dialog>` over the list through shallow routing (`page.state` plus a `?modal=<id>` fallback), which could not be reloaded, linked to, or dismissed with Back, and which had to hold a screenful of fields inside a scrolling dialog.
- **Certificate Detail Page**: The only view of a single certificate, at `/certs/<id>`. Accessible to the user who approved the underlying request and to auditors/admins. Opens with an identity strip — type chip, decision badge, full id — above three sections: the certificate itself, the decision audit record (who approved or denied it, when, from where, and the approver's groups), and what the requesting host claimed about itself. A service certificate adds a fourth for the redemption that produced it and the link back to its service code.

  Extensions and critical options are rows in the certificate section rather than a section of their own. They are two more things signed into this certificate, exactly like the key id and the validity window above them, and splitting them off made a reader cross a heading to finish reading one certificate. They stay one addressable block (`cert-grants`) because the whole pair is dropped for a PAM or console certificate that carries neither — stated as "None" when empty rather than omitted, for every certificate that could carry them. Returns 404 (uniformly for both "not found" and "not authorized") to prevent existence leakage.

  It is `wide`, the width of the lists that open it, rather than the narrower reading width: both are centred in the same space, so a detail page 360px narrower moves the left edge inward on every click and opens a gap beside it. It opens with a back chip naming the list the reader came from, chosen by a `from` search parameter the linking list writes — `history`, `dashboard` or `admin`. The parameter is only ever looked up in that table: it is a hint from our own links, not a destination to follow because a URL said so. A certificate reached from a notification or an audit line carries no `from` and falls back to the reader's own history.

  `DetailRow` carries vertical padding only, on the assumption that its horizontal padding comes from the container. That container is `PageSection`'s `p-4`: a bare `dl` inside a bordered box would put every label and value flush against the border.

  The two option columns are decoded server-side (`setIssuedOptionsOnCertificate` in `server/controller/responses.go`) and reach the browser as an array and an object, not as strings of JSON. Only the detail endpoint fills them — a list row does not show them, and a hundred-row page should not carry them. `CertificateService.GetByID` has to name both columns in its `Select` for any of that to happen: it builds `model.Certificate` from an explicit column list rather than the whole row, so a column left out of the query arrives empty however faithfully it was written.

### Timestamps and serials

`formatDateTime` names the timezone it rendered in, and the client's `expiryPhrase` names its own. These are audit timestamps read against something outside the browser — `ssh-keygen -L`, a client's "valid until 06:24", a log line on the host — and the machine printing that other line is routinely in a different zone. Without the zone on both, the two disagree and the reader cannot tell an offset from a wrong timestamp. Naming it costs four characters. (`timeZoneName` cannot be combined with `dateStyle`/`timeStyle`, which is why `formatDateTime` spells its components out.)

Certificate serials cross the wire as decimal strings, never JSON numbers. They are 63 bits of randomness, so all but a vanishing fraction exceed `Number.MAX_SAFE_INTEGER` and a browser parsing one as a number rounds it silently: `3260700569889958163` reads back as `3260700569889958400`, which matches no certificate and cannot be searched for. The Go side carries both `json:",string"` and `tstype:"string"` — the first decides what is written, the second what tygo tells the browser to expect.

- **ConsentModal**: Blocking login consent notice, shown above the login form until accepted.
- **AuditTimeline**: The audit feed as a list of sentences — who did what, to whom. Each row puts the sentence on the line and pins the action name and the timestamp together as one muted group to its right, so a long sentence pushes the pair onto its own line intact rather than stranding the time on whichever line it happened to reach. Rows are separated by a rule, not only by space: an event is a sentence, a reason and a field list, and two of them stacked with nothing between read as one paragraph that changed subject halfway down. Details render as an aligned two-column field list, so a long request id wraps under the value column rather than under whatever pair preceded it. Unknown actions fall back to the raw namespaced name: the taxonomy grows without a wire change, and a client that rendered only the actions it knew would silently drop the new ones. Timestamps go through `formatDateTime`, zone named, for the reason every audit timestamp does.
- **Pager**: Offset pagination for the paged admin and auditor lists. The server sends the window it served (`webtypes.PageMeta`) and the pager asks for another one by offset, so neither side re-derives page arithmetic per list. Renders nothing when one page holds everything, keeps the first and last page reachable behind an ellipsis on long runs, and marks the current page with `aria-current`. Built from plain buttons rather than `Button`, which carries neither `aria-current` nor a per-page accessible name.
- **SearchInput**: The debounced search box those lists are filtered with. Reports the trimmed term once the typing settles, and only when it settled on something new, so a stray space does not re-run a query. Enter reports immediately; the clear button reports an empty term without waiting out the debounce. `value` seeds the box and is not watched afterwards — a page that needs to reset the term remounts it with a key.

### The admin area

`src/routes/admin/+layout.svelte` gates the whole area on auditor access and
does nothing else. The gate is display-only — the server re-checks every
read — and a signed-in identity without auditor access is told so rather
than bounced to a login it has already satisfied.

It used to own two things it had no business owning. It carried a horizontal
tab row naming the eight sections, which the rail now carries alongside every
other destination in the app; and it pinned `1100px` on pages that then
re-declared `680px` or `max-w-full` inside it, so an admin page's width was
settled in two files that disagreed. Both are gone. Each admin page states
its own width through `PageShell`, the same way every other page does, and
the section it belongs to is marked in the rail — for its own path and for
anything beneath it, so `/admin/users/<id>` keeps `Users` marked while
`/admin/certificates-archive` does not light up `Certificates`.

The eight sections, and what each screen is. Three of them have their own
subsection below; the rest in brief:

- **Users** (`/admin/users`, `full` width) — the directory of every account
  with search and the disable/enable controls; **one user**
  (`/admin/users/<id>`) shows everything the server stores about them: the
  OIDC record as the ID token last carried it, with each field a configured
  `ldap.fields` entry overrides struck through beside the value actually
  used and an `oidc`/`ldap` source badge per row; group membership with
  source and first/last seen; the directory record, with a
  `directory_enabled` flag separating "never enriched" from "the directory
  is off"; changed notification preferences; and what disabled the account.
- **Certificates** and **Config** — see the subsections below.
- **Service codes** (`/admin/service-codes`, and one code at
  `/admin/service-codes/<id>`) — see "The service codes screen" below; the
  detail is a page, shared with the holder-facing view through
  `AdminServiceCodeDetail`/`ServiceCodeDetail`, carrying the Expire control
  for SOC.
- **Directory** (`/admin/directory`) — the LDAP console: sync status and the
  last pass's counts for any auditor; _Sync now_ (with a dry-run option) and
  the read-only _Probe_ for admins only, since both make the server reach
  out to the directory.
- **Claims echo** (`/admin/identity/echo`, admin-only) — re-authenticates
  against the provider with `prompt=login` and renders the decoded ID token,
  each claim annotated by the field that consumes it, plus the
  `fields.extra` block that would capture the unread ones. Nothing is
  stored; the token arrives in the URL fragment and the page clears it.
- **Audit log** (`/admin/audit`, `full` width) — the bounded table copy of
  the audit stream, with a "load more" pager. `AuditTimeline` is unboxed
  here: it already rules between rows and bars the side of each, so a frame
  around it would be a border around a border.
- **Diagnostics** (`/admin/diagnostics`, admin-only) — four read-only
  deployment self-checks run on demand (public URL reachability, proxy
  trust and client IP, security header hygiene, CORS/edge header
  attribution), one card per check in a single column, each with an OK /
  Warning / Critical / Skipped badge and a remediation note. Skipped is
  deliberately not a pass.

### The effective configuration screen

`src/routes/admin/config/+page.svelte` renders every configuration key in
effect on the server, grouped into the sections of the config file, filtered
by key or by value.

It names no key of its own. The server builds the answer by reflecting over
its own config struct (`server/config/effective.go`), so the page renders
whatever arrives: a screen that lists fields by hand is wrong the moment a
key is added and nobody remembers to add it here, and an operator reading it
cannot tell an unset key from an unlisted one.

Two rules make the volume readable. Keys sitting at their defaults are hidden
until "Show unset keys" is checked, because a wall of them buries the handful
someone actually set. A typed filter overrides that entirely — asking for a
key by name is asking whether it is set, and answering "no match" to a key
that exists would be a lie.

Secrets are redacted at their declaration in Go (`secret:"true"`), never by
this page. A redacted key still reports whether a value is set: "is the
client secret configured" is an operational question that discloses nothing.

### The admin certificate issuance list

`src/routes/admin/certificates/+page.svelte` lists all issued certificates
across all users, searchable and filterable by certificate type, expiration
status, and any of: key ID, principals, serial number, fingerprint, owner
username, or owner email. Offset-paginated using `Pager` and filtered with
`SearchInput`, drawing data from `/api/admin/certificates/history`.

Each `CertRow` is clickable and navigates to the canonical certificate detail
page at `/certs/<id>`. Orphaned certificates (whose owner was deleted) still
appear in the list via LEFT JOIN, so an auditor can track them.

### The service codes screen

`src/routes/service-codes/+page.svelte` lists the enrollments the signed-in
identity has approved, and never the codes themselves — the server has no
endpoint that returns one, by design (see
`webtypes.ServiceEnrollmentResponse`).

It has two levels and a route. The page lists the service accounts the
identity holds; opening one filters the page down to that account's codes,
which is shallow-routed (`page.state.accountName`, with `?account=<name>` as
the fallback a pasted link arrives with) because it is this same list,
filtered, against codes already loaded. Opening a code is a navigation to
`/service-codes/<id>`.

A row carries what someone scanning the list is looking for: the account the
code mints for, when it was approved, what a redemption hands out, how often
anything has redeemed it, and whether it still works. `ServiceCodeDetail`
behind it adds the key ID, the bound key's fingerprint, the options fixed at
approval, the code's dates, and the full redemption log fetched from
`/api/certs/requests/{id}/retrievals`.

`/service-codes/<id>` resolves the id out of the caller's own enrollment
list rather than from an endpoint of its own: there is no per-id route for a
holder, and `GET /api/certs/service/enrollments` is already scoped to the
accounts the identity holds, so a code absent from that answer is a code this
reader may not see — one message covers both, since telling them apart would
report the existence of codes they have no access to. It opens with a back
chip to the account's own codes, the same shape as the account view's chip
back to the account list.

Live codes come first and expired ones follow under their own section label
rather than dropping off the page: a job that stopped working is explained by
the code beneath it, and hiding the row hides the explanation.

## Dark Mode

Three states, not two: **system** (the default, follows the OS live), **light**, and **dark**. "Follow my OS" is a real choice rather than the absence of one — someone who has never picked should track their OS when it changes, and someone who picked light on a dark machine should stay on light.

`src/lib/theme.svelte.ts` owns the preference, persists it to `localStorage` under `ssoossh:theme`, and tracks `prefers-color-scheme` with a live `matchMedia` listener so a system theme change takes effect without a reload. The root layout starts it and applies the resolved theme to `<html>`.

`ThemeToggle` — inside the rail's identity drop-up (`RailUserMenu`) for a signed-in visitor, in the slim header on the signed-out screens — steps system → light → dark → system. One cycling button rather than a switch, because a switch has nowhere to put the third state. Its accessible name states both where it is and where pressing it goes.

**Avoiding the flash:** an inline script in `src/app.html` resolves the theme and sets the class _before first paint_, so the page never shows light on its way to dark. It duplicates the resolution rule deliberately — it cannot import the store, because it has to run before any module does. If you change the storage key or the resolution rule in `theme.svelte.ts`, change it there too. ssoosshd injects a CSP nonce into every script tag it serves (`server/frontend/frontend_included.go`), so the inline script is allowed.

Storage access is wrapped everywhere: a browser with site data blocked throws on read rather than returning null, and a theme preference is not worth failing a page load over. That case follows the OS, same as a first visit.

All components respond automatically — no per-component dark-mode logic, because colors are tokenized. Test by switching your OS theme with the toggle on "system", and by picking each explicit state.

One caveat tokenization does not cover: a `<dialog>` is given `color: CanvasText` by the UA stylesheet, which **beats inheritance** from `body`. The shared `.modal-dialog` class sets `color` from a token for exactly this reason; any new dialog should use that class rather than assembling its box from utilities.

### The `.data-table` class

The three admin tables had each invented their own: `px-3 py-2` cells with
bold sentence-case headers in one, `pr-4` and no horizontal padding in
another, and a header treatment that matched nothing else in the app. One
component style in `src/app.css` now states it — mono uppercase headers,
consistent cell padding, hairline row rules, and `tabular-nums` so dates and
counts line up down a column.

It is a component style rather than utilities because the rules reach
descendants: every `th` and `td` of a table, without a class on each of a few
hundred generated cells. Sitting in the components layer means a page can
still override one property on one cell with a utility.

### The `.modal-dialog` class

Dialogs get their box from a single component class in `app.css`, not from utilities. The UA stylesheet gives `<dialog>` `width: fit-content`, `height: fit-content` and `margin: auto`, which `inset-0` does not undo — the box sizes to its content and lands wherever the over-constrained position resolves rather than centred. `.modal-dialog` overrides every one of those explicitly, along with the border, background, text color, and backdrop.

## Branding & Configuration

### Runtime Branding Endpoint

Deployment branding (org name, logo URL, login consent notice) is fetched at app startup via an unauthenticated API call to `/api/branding`. The fetch is non-blocking and fails closed — any error (404, network failure, timeout) treats it as "no branding configured," so the UI works standalone against a server that has set nothing.

The `branding.svelte.ts` store handles this:

```ts
export async function loadBranding(): Promise<void>;
export function getBranding(): BrandingConfig;
```

The response shape (`BrandingResponse` in `server/webtypes`, from which the TypeScript type is generated):

```ts
interface BrandingConfig {
	org_name?: string; // Shown in header as a tag next to "ssoossh"
	logo_url?: string; // Image URL, rendered as <img> before "ssoossh"
	login_notice?: string; // Full-text consent notice, blocks login form
}
```

### Runtime Version Endpoint

The footer's build identity is fetched the same way, from an unauthenticated `/api/version`, and for the same reason: the frontend is prerendered once and served by whatever binary is running, so the version cannot be baked in at build time. It comes from the Go build stamp (`internal/version`), which goreleaser and the Makefile set via ldflags.

The `version.svelte.ts` store handles this:

```ts
export async function loadVersion(): Promise<void>;
export function getVersion(): VersionResponse | null;
```

It fails closed like branding, but to `null` rather than an empty object: the repository URL is served rather than hardcoded, so with no response there is nothing honest to render and the footer is omitted entirely.

A tagged build shows `v1.1.3` linked to its GitHub release. An untagged one has no release to point at, so it shows `development (7a3f9c1)` — the short commit is what identifies that build.

What the endpoint reveals is the operator's choice, through `version.mode`
(`server/config/types_version.go`). `show`, the default, is the above.
`hide` returns every field empty, and the footer then renders no build
identity at all. Any other string is served _as_ the version, with no
commit, repository, or release link that would give the real build away —
so `show` and `hide` are reserved words that cannot be used as a fake
version. The endpoint is unauthenticated and its exact version plus full
commit is enough to match a deployment against known issues, which is the
disclosure this trades away.

**Brand placement** (`AppRail.svelte`, `src/routes/+layout.svelte`):

- The logo image (if `logo_url` is set) appears before the "ssoossh"
  wordmark, at the top of the rail and, on the screens that have no rail, at
  the leading edge of the header.
- The org name (if `org_name` is set) appears as a small tag to the right of
  "ssoossh", capped and truncated — `6.5rem` in the rail, `8rem` in the
  header. A deployment that set a name wants it at every width, but an
  unbounded one would push the wordmark out of its own row.
- Both are absent by default, keeping the brand minimal when a deployment
  sets no branding.
- Expanded, the brand is a link home and the rail's width control sits at
  the far end of the same row.
- Collapsed, the wordmark and org tag drop and the mark stands alone —
  dropping the mark too would leave the column starting mid-list. At 60px
  there is one slot in that row and it has to do both jobs, so the mark
  becomes the button that brings the rail back and home gives up its link:
  Dashboard is the row directly below, and stranding someone at icon width
  costs more than one redundant path to `/`.

**The narrow-viewport bar** (`src/routes/+layout.svelte`):

Below `lg` the chrome shrinks to the one control that brings the rail back:
a trigger, then the brand. Everything else — theme, identity, sign out — is
inside the drawer, one tap away and unable to drift out of step with the
desktop copy. The trigger sits ahead of the brand because that is where a
navigation control is looked for.

The trigger names the state it will move to rather than the one it is in, so
`aria-expanded` and the accessible name agree. The scrim behind an open
drawer dismisses it with a pointer but is `aria-hidden`, so only one element
in the accessibility tree offers to close the drawer; Escape is the keyboard
path, handled in the layout.

**Login Consent** (`src/routes/login/+page.svelte`):

- If `login_notice` is set, a `ConsentModal` overlays the login form with a backdrop.
- The form stays inert — blurred, dimmed, and `pointer-events-none` — until the user clicks "I Accept", and the sign-in button is `disabled` for as long as it is.
- This is a blocking modal, not a dismissible banner. Escape is blocked so it cannot be dismissed unaccepted.
- The modal carries no visible title: the notice is each deployment's own approved wording, shown in full and never summarized, so a generic "Notice" heading would only compete with it. The heading stays in the accessibility tree (`sr-only` + `aria-labelledby`) to name the dialog, and a long notice scrolls inside a keyboard-reachable region rather than pushing "I Accept" off screen.
- The notice can only gate a sign-in it stands in front of, so every "you need to sign in" path lands on `/login` first: `auth.goToLogin` is what a 401 and the header's sign-in button call, carrying a `return_to`. `auth.startLogin` — the jump to the identity provider — is called from `/login` alone, downstream of acceptance. This matters most on `/approve/<id>`: a certificate request URL is how most people reach the app at all, and they arrive signed out.

### Environment Variables

The frontend build accepts only a few Vite build-time env vars (for development server settings). These do NOT include branding; branding is always runtime-fetched. The one that matters is `DEVELOPMENT_BACKEND_URL`, read from `.env` / `.env.local` by `vite.config.ts` (see `frontend/README.md`).

## Accessibility (WCAG 2.1 Level AA / Section 508)

The design and components are built for WCAG 2.1 Level AA compliance as a hard requirement:

- **Contrast**: All text meets AA contrast ratios (4.5:1 for body text, 3:1 for large text). Light text on light accent is avoided.
- **Focus Visibility**: Interactive elements have visible `:focus` or `:focus-visible` states via browser defaults or explicit outline/background changes.
- **Keyboard Navigation**: All buttons and interactive elements are keyboard-accessible via semantic HTML (`<button>`, `<a>`) and logical tab order.
- **Icon Usage**: Meaningful icons always carry an `aria-label`. Decorative icons have `aria-hidden="true"`.
- **Color Alone**: Status is never conveyed by color alone; icons, text, or other markers supplement it.
- **Modals**: The native `<dialog>` element with `.showModal()` provides focus management and an inert background.

Run a contrast checker against the light and dark palettes before deploying new colors.

## Testing

Tests for components use `@testing-library/svelte` and `vitest`. Pattern:

```ts
import { render, screen } from '@testing-library/svelte';
import MyComponent from './MyComponent.svelte';

describe('MyComponent', () => {
	it('should [action] when [condition]', () => {
		render(MyComponent, { prop: 'value' });
		expect(screen.getByText('expected text')).toBeInTheDocument();
	});
});
```

- One assertion per test when possible.
- Use descriptive test names: "should [action] when [condition]".
- Mock external dependencies; never call real APIs.

## Contributing

When adding or modifying styles:

1. **Check tokens first**: Before hardcoding a color, spacing, or size, check if a token already exists in `app.css`. Reuse it.
2. **Follow the variant pattern**: For variant-bearing components, use the local `const variants` object, not a centralized helper or Tailwind arbitrary values.
3. **Test both modes**: Verify your changes in light and dark mode.
4. **Verify keyboard access**: Tab through interactive elements; ensure focus is always visible.
5. **Run the test suite**: `corepack pnpm run test`.
6. **Run the build**: `corepack pnpm run build` — ensures fonts and tokens are prerendered correctly.
7. **Run type check**: `corepack pnpm run check` — catches TypeScript errors.

## References

- [OKLch Color Space](https://oklch.com/) — perceptual color uniformity
- [Public Sans](https://www.opensans.com/about) — UI typeface (OFL license)
- [Fira Code](https://github.com/tonsky/FiraCode) — monospace typeface (OFL license)
- [Lucide Icons](https://lucide.dev/) — icon library (@lucide/svelte)
- [WCAG 2.1 Level AA](https://www.w3.org/WAI/WCAG21/quickref/?currentsetting=level%20aa) — accessibility guidelines
- [Svelte Documentation](https://svelte.dev/docs) — framework reference
