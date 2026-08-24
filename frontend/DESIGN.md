# Frontend Design System

This document describes the design tokens, typography, iconography, and component patterns that maintain visual and interaction consistency across the ssoossh frontend.

## Color Tokens

The app uses a curated set of CSS custom properties defined in `src/app.css`. All colors are specified in [OKLch color space](https://oklch.com/) for perceptually uniform results. The palette is the same in light and dark modes; the mode switch inverts lightness while preserving hue and chroma.

### Light Mode (default)

- `--color-surface`: `#ffffff` — main background
- `--color-surface-muted`: `oklch(97% 0.002 200)` — secondary surfaces, cards, light hover states
- `--color-border-subtle`: `oklch(88% 0.003 200)` — borders, dividers
- `--color-ink`: `oklch(20% 0.005 200)` — primary text
- `--color-ink-muted`: `oklch(45% 0.005 200)` — secondary text, placeholders, hints
- `--color-accent`: `oklch(45% 0.06 200)` — primary interactive element (teal)
- `--color-accent-hover`: `oklch(38% 0.07 200)` — accent pressed state
- `--color-accent-ink`: `#ffffff` — text on accent backgrounds
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

All sizes use CSS custom properties (lines 36–40 in `app.css`):

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

- `menu` — navigation menu trigger
- `chevron-down`, `chevron-left`, `chevron-right` — directional indicators
- `arrow-right` — forward movement in a primary action (the login button)
- `layout-grid` — the "All" option in a type filter
- `link` — a shareable link to the thing on screen
- `sun`, `moon`, `monitor` — the light, dark, and follow-the-system theme states

**Certificate Types:**

- `user` — user certificates
- `terminal` — PAM certificates
- `cog` — service certificates

**Utility:**

- `search` — the search box on a paged list
- `zap` — generic or all-category indicator

### Size Scale

The `sizeMap` in `Icon.svelte` defines pixel dimensions:

- `xs`: 12px
- `sm`: 16px
- `md`: 20px
- `lg`: 24px
- `xl`: 32px

Icon utilities (`.icon-xs` through `.icon-xl`) are defined in `src/app.css` lines 95–115 for manual sizing if needed outside the `Icon` component.

## Component Patterns

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

### Page Structure

Every screen is a single centred column inside the layout's `main`, and owns
its own width — the layout imposes none. The widths are chosen for what the
screen is for, not for consistency's sake:

| Screen                       | Width             | Why                                                       |
| ---------------------------- | ----------------- | --------------------------------------------------------- |
| Login                        | `380px`           | one action; a wider column makes it look unfinished       |
| Account                      | `680px`           | metadata cards showing identity and principals            |
| Approval, error, cert detail | `560px` / `600px` | a field list read top to bottom                           |
| Dashboard, history           | `680px`           | rows with a subject on the left and a status on the right |
| Service codes                | `680px`           | the same rows, each opened out into its own field list    |

Each column opens with a `PageHeading`: an accent eyebrow naming the area
("Activity", "History", "Certificate request") above the page's `h1`. The
eyebrow is what makes a screen identifiable at a glance without reading the
title, so it is required rather than optional, and there is exactly one `h1`
per page.

Lists are stacks of standalone cards — 1px border, 10px radius, `surface`
background, `2.5` gap — not divided rows inside one panel. A list of
certificates is a list of things that happened, and each one should read as
discrete.

### Common Components

- **Button**: Variants: `primary` (accent blue), `danger` (red), `ghost` (outline). Always includes `disabled` state via `opacity-50`. Lays its children out as a centered `inline-flex` row with a gap, so a label plus a trailing icon needs no wrapper. `full` stretches it to the container width, for a screen whose single primary action should span the column (the login button).
- **BrandMark**: The deployment logo slot — the mark left of the "ssoossh" wordmark in the header and above the login heading. Renders `branding.logo_url` when a deployment sets one (height-constrained, width free, since most organisation logos are wide wordmarks) and ssoossh's own check-in-circle mark otherwise, so the slot is never empty. Takes `size` in pixels; corner rounding follows the size.
- **Card**: Wraps content in a bordered, shadowed box with optional title/description header and footer slot.
- **Footer**: The bar closing every page — the running build's version, a link to the release it was cut from, and links back to the project on GitHub. Presentational: the build identity arrives as a prop, so the fetch happens once in the layout. Renders nothing at all while the version is unknown.
- **Alert**: Variants: `error`, `warning`, `info`. Each includes an icon and a color from the token set.
- **StatusBadge**: Maps request/certificate statuses (pending, approved, denied, etc.) to colored pills with status-appropriate icons. Rendered capitalised — the wire value is lowercase, the label is not.
- **DetailRow**: A label–value pair for metadata lists, with optional icon and monospace rendering. A 140px label column at 13px, stacking on narrow viewports.
- **PageHeading**: The eyebrow + `h1` pair every screen opens with, with an optional right-aligned action (the dashboard's "View all history →").
- **SectionLabel**: The small muted uppercase label that opens a group of fields inside a card. Quieter than `PageHeading`'s eyebrow, which takes the accent.
- **CertRow**: One certificate as a standalone, clickable card — type badge, subject, what happened and when, principals, and the decision badge.
- **ServiceCodeRow**: One approved service enrollment as the same kind of card — the account the code mints for, when it was approved and what it hands out, how often it has been redeemed, and an active/expired pill. Never the code.
- **ServiceCodeDetailModal**: The enrollment in full behind a row: what a redemption grants, the options fixed at approval, the code's own dates, and its redemption log. The server caps that log at its newest 100 rows and reports the true total, so the panel says what it is showing a slice of rather than letting the last row read as the first redemption. Read-only, and structurally unable to show a code.
- **TypeBadge**: The certificate type as a fixed 26×26 square. Fixed rather than content-sized so rows align vertically whatever the type is called, and always shown: on a row the type is the primary identifier, not decoration.
- **TypeChip**: The labelled form of `TypeBadge`, for detail views with room to name the type.
- **MonoChip**: One monospace value as a bordered chip — a principal, an IP. Chips rather than a comma-separated string so set boundaries are unambiguous.
- **ThemeToggle**: The header's theme control — one button cycling system → light → dark. See Dark Mode.
- **UserMenu**: The header's identity control — who you are acting as, with sign-out behind it. Closes on outside click and on Escape, which returns focus to the trigger.
- **OptionDiffList**: Shows granted vs. trimmed options (extensions, critical options) with strike-through for trimmed items.
- **ApprovalView**: Composite component rendering a full certificate-request approval form.
- **CertDetailModal**: Read-only certificate details in a modal, triggered by clicking a row in Dashboard or History. A service certificate also states where it was fetched from — the source address of the `service retrieve` that produced it, distinct from the approval's own IP — and links to the service code behind it. The open certificate lives in `page.state` via shallow routing, with a matching `?modal=<id>` in the address bar so a specific certificate is linkable without leaving the list behind it. It has to be both: SvelteKit's `pushState` updates `page.state` and the address bar but never reassigns `page.url`, so state drives the open modal and the search parameter is only the fallback a pasted link arrives with. Every close path — the button, Escape, the backdrop — goes through the dialog's own `close` event, which is what keeps that parameter in step with what is on screen.
- **ConsentModal**: Blocking login consent notice, shown above the login form until accepted.
- **Pager**: Offset pagination for the paged admin and auditor lists. The server sends the window it served (`webtypes.PageMeta`) and the pager asks for another one by offset, so neither side re-derives page arithmetic per list. Renders nothing when one page holds everything, keeps the first and last page reachable behind an ellipsis on long runs, and marks the current page with `aria-current`. Built from plain buttons rather than `Button`, which carries neither `aria-current` nor a per-page accessible name.
- **SearchInput**: The debounced search box those lists are filtered with. Reports the trimmed term once the typing settles, and only when it settled on something new, so a stray space does not re-run a query. Enter reports immediately; the clear button reports an empty term without waiting out the debounce. `value` seeds the box and is not watched afterwards — a page that needs to reset the term remounts it with a key.

### The service codes screen

`src/routes/service-codes/+page.svelte` lists the enrollments the signed-in
identity has approved, and never the codes themselves — the server has no
endpoint that returns one, by design (see
`webtypes.ServiceEnrollmentResponse`).

It is built the same way as the certificate history: a stack of
`ServiceCodeRow` cards, each opening `ServiceCodeDetailModal` through the
same shallow-routing arrangement (`page.state.modalEnrollmentId`, with
`?modal=<id>` as the fallback a pasted link arrives with). The two keys are
separate rather than shared, so a `?modal=` id belonging to one list cannot
resolve against the other.

A row carries what someone scanning the list is looking for: the account the
code mints for, when it was approved, what a redemption hands out, how often
anything has redeemed it, and whether it still works. The panel behind it
adds the key ID, the bound key's fingerprint, the options fixed at approval,
the code's dates, and the full redemption log fetched from
`/api/certs/requests/{id}/retrievals`.

Live codes come first and expired ones follow under their own section label
rather than dropping off the page: a job that stopped working is explained by
the code beneath it, and hiding the row hides the explanation.

## Dark Mode

Three states, not two: **system** (the default, follows the OS live), **light**, and **dark**. "Follow my OS" is a real choice rather than the absence of one — someone who has never picked should track their OS when it changes, and someone who picked light on a dark machine should stay on light.

`src/lib/theme.svelte.ts` owns the preference, persists it to `localStorage` under `ssoossh:theme`, and tracks `prefers-color-scheme` with a live `matchMedia` listener so a system theme change takes effect without a reload. The root layout starts it and applies the resolved theme to `<html>`.

`ThemeToggle` in the header steps system → light → dark → system. One cycling button rather than a switch, because a switch has nowhere to put the third state. Its accessible name states both where it is and where pressing it goes.

**Avoiding the flash:** an inline script in `src/app.html` resolves the theme and sets the class _before first paint_, so the page never shows light on its way to dark. It duplicates the resolution rule deliberately — it cannot import the store, because it has to run before any module does. If you change the storage key or the resolution rule in `theme.svelte.ts`, change it there too. ssoosshd injects a CSP nonce into every script tag it serves (`server/frontend/frontend_included.go`), so the inline script is allowed.

Storage access is wrapped everywhere: a browser with site data blocked throws on read rather than returning null, and a theme preference is not worth failing a page load over. That case follows the OS, same as a first visit.

All components respond automatically — no per-component dark-mode logic, because colors are tokenized. Test by switching your OS theme with the toggle on "system", and by picking each explicit state.

One caveat tokenization does not cover: a `<dialog>` is given `color: CanvasText` by the UA stylesheet, which **beats inheritance** from `body`. The shared `.modal-dialog` class sets `color` from a token for exactly this reason; any new dialog should use that class rather than assembling its box from utilities.

### The `.modal-dialog` class

Dialogs get their box from a single component class in `app.css`, not from utilities. The UA stylesheet gives `<dialog>` `width: fit-content`, `height: fit-content` and `margin: auto`, which `inset-0` does not undo — the box sizes to its content and lands wherever the over-constrained position resolves rather than centred. `.modal-dialog` overrides every one of those explicitly, along with the border, background, text color, and backdrop.

## Branding & Configuration

### Runtime Branding Endpoint

Deployment branding (org name, logo URL, login consent notice) is fetched at app startup via an unauthenticated API call to `/api/branding`. The fetch is non-blocking and fails closed — any error (404, network failure, timeout) treats it as "no branding configured," allowing the UI to work standalone and auto-enable branding once the backend endpoint exists.

The `branding.svelte.ts` store handles this:

```ts
export async function loadBranding(): Promise<void>;
export function getBranding(): BrandingConfig;
```

The expected response shape (to be reconciled with real backend webtypes once implemented):

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

A tagged build shows `v0.1.0` linked to its GitHub release. An untagged one has no release to point at, so it shows `development (7a3f9c1)` — the short commit is what identifies that build.

**Header Branding** (`src/routes/+layout.svelte`):

- Logo image (if `logo_url` is set) appears before the "ssoossh" wordmark.
- Org name (if `org_name` is set) appears as a small tag to the right of "ssoossh".
- Both elements are absent by default, keeping the header minimal when not deployed with branding.

**Login Consent** (`src/routes/login/+page.svelte`):

- If `login_notice` is set, a `ConsentModal` overlays the login form with a backdrop.
- The form stays inert — blurred, dimmed, and `pointer-events-none` — until the user clicks "I Accept", and the sign-in button is `disabled` for as long as it is.
- This is a blocking modal, not a dismissible banner. Escape is blocked so it cannot be dismissed unaccepted.
- The modal carries no visible title: the notice is each deployment's own approved wording, shown in full and never summarized, so a generic "Notice" heading would only compete with it. The heading stays in the accessibility tree (`sr-only` + `aria-labelledby`) to name the dialog, and a long notice scrolls inside a keyboard-reachable region rather than pushing "I Accept" off screen.
- The notice can only gate a sign-in it stands in front of, so every "you need to sign in" path lands on `/login` first: `auth.goToLogin` is what a 401 and the header's sign-in button call, carrying a `return_to`. `auth.startLogin` — the jump to the identity provider — is called from `/login` alone, downstream of acceptance. This matters most on `/approve/<id>`: a certificate request URL is how most people reach the app at all, and they arrive signed out.

### Environment Variables

The frontend build accepts only a few Vite build-time env vars (for development server settings). These do NOT include branding; branding is always runtime-fetched. See `.env.tpl` for the dev-server vars.

## Accessibility (WCAG 2.0 Level AA / Section 508)

The design and components are built for WCAG 2.0 Level AA compliance as a hard requirement:

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
- [WCAG 2.0 Level AA](https://www.w3.org/WAI/WCAG21/quickref/?currentsetting=level%20aa) — accessibility guidelines
- [Svelte Documentation](https://svelte.dev/docs) — framework reference
