<script lang="ts">
	import { resolve } from '$app/paths';
	import { fade, fly } from 'svelte/transition';
	import { easeEnter, easeExit, enterMs, exitMs } from '$lib/motion';
	import { page } from '$app/state';
	import '../app.css';
	import { logout } from '$lib/api/endpoints';
	import { errorMessage, goToLogin } from '$lib/auth';
	import { loadBranding, getBranding } from '$lib/branding.svelte';
	import AppRail from '$lib/components/AppRail.svelte';
	import BrandMark from '$lib/components/BrandMark.svelte';
	import Button from '$lib/components/Button.svelte';
	import Footer from '$lib/components/Footer.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import ThemeToggle from '$lib/components/ThemeToggle.svelte';
	import { isFocusRoute } from '$lib/nav';
	import { rail } from '$lib/rail.svelte';
	import { session } from '$lib/session.svelte';
	import { theme } from '$lib/theme.svelte';
	import { loadVersion, getVersion } from '$lib/version.svelte';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Loaded once for the whole app rather than per page: every screen wants
	// the same answer and it only changes at login or logout. Not awaited —
	// the nav renders signed-out until it resolves, and pages that actually
	// need an identity get their own 401 from their own call.
	session.load();

	// Load branding config from the unauthenticated /api/branding endpoint.
	// Fails closed — any error treats it as "no branding configured".
	loadBranding();

	// Same deal for the build identity behind the footer: one unauthenticated
	// fetch for the whole app, and a failure just leaves the footer off.
	loadVersion();

	// The theme is already on <html> by now: app.html applies it before first
	// paint so there is no flash. This picks up the stored preference for the
	// toggle to render, and starts tracking the OS setting so "system"
	// follows it live rather than only at load.
	$effect(() => theme.start());

	// Kept in its own effect so it re-runs on every change to the preference
	// or the OS setting, which is what makes both the toggle and a system
	// theme switch take effect immediately.
	$effect(() => theme.apply());

	// The rail width preference. Nothing reads it before paint, unlike the
	// theme, so it runs here rather than in app.html.
	$effect(() => rail.start());

	const branding = $derived(getBranding());
	const version = $derived(getVersion());

	let signingOut = $state(false);

	// The login page carries its own sign-in action, so the header's copy of
	// it would be a second button pointing at the screen already on show.
	const onLoginPage = $derived(page.url.pathname === resolve('/login'));

	/**
	 * Whether this screen gets the rail.
	 *
	 * Two reasons it does not. A signed-out visitor has nowhere to go, so a
	 * column of destinations behind a 401 is furniture. And the focus routes
	 * — sign-in, an approval raised by a session elsewhere, a console code
	 * transcribed off a machine that cannot print a link — are single-task
	 * screens where a navigation column invites the reader to wander off
	 * mid-decision.
	 */
	const showRail = $derived(session.signedIn && !isFocusRoute(page.url.pathname));

	let drawerEl = $state<HTMLDivElement>();
	let drawerTrigger = $state<HTMLButtonElement>();

	/** drawerFocusables lists what the drawer can hand the caret to, in tab
	 * order. Recomputed per keystroke rather than cached: the identity menu
	 * inside the drawer opens and closes, and a stale list would trap the
	 * caret on a row that is no longer there. */
	function drawerFocusables(): HTMLElement[] {
		if (!drawerEl) {
			return [];
		}
		return [
			...drawerEl.querySelectorAll<HTMLElement>(
				'a[href], button:not([disabled]), input, select, textarea, [tabindex]:not([tabindex="-1"])'
			)
		];
	}

	// A drawer that only closes by re-pressing its trigger is a trap for
	// anyone who opened it by accident, and one the caret never enters is
	// furniture. Both were true here: the drawer renders earlier in the
	// document than the header that opens it, so focus stayed on the
	// trigger and the next Tab went straight past the menu into the page
	// behind it. The menu was reachable only by shift-tabbing backwards.
	//
	// So: the caret moves in on open, cycles inside while the drawer is up,
	// and goes back to the trigger on the way out — whichever way out was
	// taken, since the cleanup runs for Escape, for the scrim, and for a
	// tap on a destination alike.
	$effect(() => {
		if (!rail.drawerOpen) {
			return;
		}

		drawerFocusables()[0]?.focus();

		function onKeyDown(event: KeyboardEvent) {
			if (event.key === 'Escape') {
				rail.closeDrawer();
				return;
			}
			if (event.key !== 'Tab') {
				return;
			}
			const items = drawerFocusables();
			if (items.length === 0) {
				return;
			}
			const first = items[0];
			const last = items[items.length - 1];
			// Only the two ends are handled. Everything between them is the
			// browser's own tab order, which is the one a reader expects.
			if (event.shiftKey && document.activeElement === first) {
				event.preventDefault();
				last.focus();
			} else if (!event.shiftKey && document.activeElement === last) {
				event.preventDefault();
				first.focus();
			}
		}

		document.addEventListener('keydown', onKeyDown);
		return () => {
			document.removeEventListener('keydown', onKeyDown);
			drawerTrigger?.focus();
		};
	});

	/** signOut ends the server-side session, then reloads onto the login
	 * page. A full navigation, not goto(): it drops every bit of in-memory
	 * state belonging to the identity that just left. */
	async function signOut() {
		signingOut = true;
		try {
			await logout();
			session.clear();
			window.location.assign('/login');
		} catch (cause) {
			session.error = errorMessage(cause);
			signingOut = false;
		}
	}
</script>

<!-- The first focusable thing in the document, on every screen.

     Without it the signed-in shell puts the whole rail — brand, four
     destinations, the admin group and its eight, the identity menu — ahead
     of the page, and a keyboard user walked all of it again on every
     navigation. `sr-only` keeps it out of the way of a mouse; `skip-link`
     brings the whole control back the moment it takes focus, because a
     bypass a sighted keyboard user cannot see is one they cannot trust.

     Outside the branch below so there is exactly one of these, whichever
     shell renders. -->
<a href="#main-content" class="skip-link sr-only">Skip to main content</a>

{#if showRail}
	<div class="flex min-h-screen">
		<!-- The rail proper, above `lg`. Sticky rather than scrolling with
		     the page: it is the app's chrome now, and chrome that scrolls
		     away leaves a long admin table with no way out of it. -->
		<div class="sticky top-0 h-screen shrink-0 max-lg:hidden">
			<AppRail orgName={branding.org_name} {signingOut} onsignout={signOut} />
		</div>

		<!-- The same rail as an off-canvas drawer below `lg`. One component,
		     so the two can never come to disagree about what the app
		     contains. -->
		{#if rail.drawerOpen}
			<button
				type="button"
				aria-hidden="true"
				tabindex="-1"
				onclick={() => rail.closeDrawer()}
				in:fade={{ duration: enterMs(), easing: easeEnter }}
				out:fade={{ duration: exitMs(), easing: easeExit }}
				class="fixed inset-0 z-40 bg-scrim lg:hidden"
			></button>
			<!-- collapsed={false}: the icon-only width is a desktop
			     preference, and the control that undoes it is hidden at this
			     width. Inheriting it here would hand a phone a strip of
			     unlabelled icons with no way back.

			     `aria-modal` rather than `inert` on the page behind it: the
			     Tab cycle above already keeps the caret inside, and setting
			     inert on the wrapper would make the trigger unfocusable at
			     the exact moment the drawer closes and wants to hand focus
			     back to it. -->
			<div
				bind:this={drawerEl}
				id="app-rail-drawer"
				role="dialog"
				aria-modal="true"
				aria-label="Navigation menu"
				in:fly={{ x: -260, opacity: 1, duration: enterMs(), easing: easeEnter }}
				out:fly={{ x: -260, opacity: 1, duration: exitMs(), easing: easeExit }}
				class="fixed inset-y-0 left-0 z-50 lg:hidden"
			>
				<AppRail orgName={branding.org_name} collapsed={false} {signingOut} onsignout={signOut} />

				<!-- The way out that is not a keystroke. The scrim behind the
				     drawer is decorative and cannot take focus, so without
				     this the only exit from the keyboard was Escape — which
				     is a shortcut, not an affordance. Sits over the rail's
				     brand row, where the collapse control lives above `lg`
				     and nothing does below it. -->
				<button
					type="button"
					onclick={() => rail.closeDrawer()}
					aria-label="Close navigation menu"
					class="absolute top-3 right-2 flex h-8 w-8 items-center justify-center rounded-md text-ink-muted transition hover:bg-surface-muted hover:text-ink"
				>
					<Icon name="x" size="sm" />
				</button>
			</div>
		{/if}

		<div class="flex min-w-0 flex-1 flex-col">
			<!-- Below `lg` there is no room for a 244px column beside the
			     page, so the chrome shrinks to the one control that brings
			     it back. Everything else — theme, identity, sign out — is
			     inside the drawer, where it is one tap away and cannot
			     drift out of step with the desktop copy. -->
			<header
				class="flex items-center gap-3 border-b border-border-subtle bg-surface px-4 py-3 lg:hidden"
			>
				<!-- One name, not two. This label used to flip to "Close
				     navigation menu" while the drawer was up, from when this
				     really was the only way back — but the scrim covers it at
				     z-40 and the drawer's Tab cycle holds the caret, so while
				     the drawer is open this button can be neither clicked nor
				     focused. The drawer carries its own close button now, and
				     two controls both announcing "Close navigation menu" is
				     one more than a reader can tell apart. `aria-expanded` is
				     what says which way this one currently sits. -->
				<button
					bind:this={drawerTrigger}
					type="button"
					onclick={() => rail.toggleDrawer()}
					aria-expanded={rail.drawerOpen}
					aria-controls="app-rail-drawer"
					aria-label="Open navigation menu"
					class="-ml-1 flex shrink-0 p-1 text-ink-muted transition hover:text-ink"
				>
					<Icon name="menu-2" size="md" />
				</button>

				<a href={resolve('/')} class="flex min-w-0 items-center gap-2 font-semibold">
					<BrandMark size={20} />
					<span>ssoossh</span>
					{#if branding.org_name}
						<span
							class="ml-0.5 max-w-[8rem] truncate border-l border-border-subtle pl-2 text-xs font-normal text-ink-muted"
						>
							{branding.org_name}
						</span>
					{/if}
				</a>
			</header>

			<main id="main-content" class="flex flex-1 flex-col items-center px-4 py-8 sm:px-8 sm:py-10">
				{@render children()}
			</main>

			<Footer {version} {branding} />
		</div>
	</div>
{:else}
	<!-- No rail: a signed-out visitor, or one of the single-task screens.
	     The header carries only what such a screen can act on. -->
	<div class="flex min-h-screen flex-col">
		<header class="border-b border-border-subtle bg-surface">
			<div class="flex items-center gap-3 px-4 py-4 sm:gap-4 sm:px-8">
				<a href={resolve('/')} class="flex min-w-0 items-center gap-2 font-semibold">
					<BrandMark size={22} />
					<span>ssoossh</span>
					{#if branding.org_name}
						<span
							class="ml-0.5 max-w-[8rem] truncate border-l border-border-subtle pl-2 text-xs font-normal text-ink-muted"
						>
							{branding.org_name}
						</span>
					{/if}
				</a>

				<div class="ml-auto flex shrink-0 items-center gap-3 text-sm">
					<ThemeToggle />
					{#if session.resolved && !session.signedIn && !onLoginPage}
						<Button variant="ghost" onclick={() => goToLogin(page.url.pathname)}>Sign in</Button>
					{/if}
				</div>
			</div>
		</header>

		<main
			id="main-content"
			class="flex w-full flex-1 flex-col items-center px-4 py-8 sm:px-8 sm:py-10"
		>
			{@render children()}
		</main>

		<Footer {version} {branding} />
	</div>
{/if}
