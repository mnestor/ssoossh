<script lang="ts">
	import { resolve } from '$app/paths';
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

	// A drawer that only closes by re-pressing its trigger is a trap for
	// anyone who opened it by accident. The trigger is behind the drawer
	// itself once it is open, so Escape and the scrim are the ways out.
	$effect(() => {
		if (!rail.drawerOpen) {
			return;
		}
		function onKeyDown(event: KeyboardEvent) {
			if (event.key === 'Escape') {
				rail.closeDrawer();
			}
		}
		document.addEventListener('keydown', onKeyDown);
		return () => document.removeEventListener('keydown', onKeyDown);
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
				class="fixed inset-0 z-40 bg-black/40 lg:hidden"
			></button>
			<!-- collapsed={false}: the icon-only width is a desktop
			     preference, and the control that undoes it is hidden at this
			     width. Inheriting it here would hand a phone a strip of
			     unlabelled icons with no way back. -->
			<div id="app-rail-drawer" class="fixed inset-y-0 left-0 z-50 lg:hidden">
				<AppRail orgName={branding.org_name} collapsed={false} {signingOut} onsignout={signOut} />
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
				<button
					type="button"
					onclick={() => rail.toggleDrawer()}
					aria-expanded={rail.drawerOpen}
					aria-controls="app-rail-drawer"
					aria-label={rail.drawerOpen ? 'Close navigation menu' : 'Open navigation menu'}
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

			<main class="flex flex-1 flex-col items-center px-4 py-8 sm:px-8 sm:py-10">
				{@render children()}
			</main>

			<Footer {version} />
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

		<main class="flex w-full flex-1 flex-col items-center px-4 py-8 sm:px-8 sm:py-10">
			{@render children()}
		</main>

		<Footer {version} />
	</div>
{/if}
