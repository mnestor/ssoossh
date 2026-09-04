<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import '../app.css';
	import { logout } from '$lib/api/endpoints';
	import { errorMessage, goToLogin } from '$lib/auth';
	import { loadBranding, getBranding } from '$lib/branding.svelte';
	import BrandMark from '$lib/components/BrandMark.svelte';
	import Button from '$lib/components/Button.svelte';
	import Footer from '$lib/components/Footer.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import ThemeToggle from '$lib/components/ThemeToggle.svelte';
	import UserMenu from '$lib/components/UserMenu.svelte';
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

	const branding = $derived(getBranding());
	const version = $derived(getVersion());

	let signingOut = $state(false);
	let navOpen = $state(false);

	// The login page carries its own sign-in action, so the header's copy of
	// it would be a second button pointing at the screen already on show.
	const onLoginPage = $derived(page.url.pathname === resolve('/login'));

	// Route ids rather than URLs, resolved at each use site: resolve() checks
	// them against the actual route tree, so a link to a page that no longer
	// exists fails the build instead of 404ing in production.
	const navItems = [
		{ route: '/dashboard', label: 'Dashboard' },
		{ route: '/logs/me', label: 'History' },
		{ route: '/service-codes', label: 'Service codes' },
		// The console code box needs an entry point that is not a
		// transcribed URL: the whole premise is that the machine in front of
		// the user cannot print a link anyone will copy, so somebody already
		// signed in has to be able to find this from the app itself.
		{ route: '/console', label: 'Console login' }
	] as const;

	function closeNav() {
		navOpen = false;
	}

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

<div class="flex min-h-screen flex-col">
	<header class="border-b border-border-subtle bg-surface">
		<div class="flex items-center gap-3 px-4 py-4 sm:gap-4 sm:px-8">
			<!-- Leading edge, ahead of the brand: that is where a navigation
			     trigger is looked for, and it leaves the trailing edge to the
			     identity. It carried its own ml-auto before, which split the
			     free space with the trailing group and left the button
			     floating in the middle of the header. -->
			{#if session.signedIn}
				<button
					onclick={() => (navOpen = !navOpen)}
					aria-expanded={navOpen}
					aria-controls="mobile-nav"
					aria-label={navOpen ? 'Close navigation menu' : 'Open navigation menu'}
					class="-ml-1 flex shrink-0 p-1 text-ink-muted transition hover:text-ink sm:hidden"
				>
					<Icon name={navOpen ? 'x' : 'menu'} size="md" />
				</button>
			{/if}

			<a href={resolve('/')} class="flex min-w-0 items-center gap-2 font-semibold">
				<BrandMark size={22} />
				<span>ssoossh</span>
				<!-- Capped rather than dropped on a phone: a deployment that
				     set a name wants it on every screen, but an unbounded one
				     would push the controls off the row. -->
				{#if branding.org_name}
					<span
						class="ml-0.5 max-w-[8rem] truncate border-l border-border-subtle pl-2 text-xs font-normal text-ink-muted"
					>
						{branding.org_name}
					</span>
				{/if}
			</a>

			{#if session.signedIn}
				<!-- Desktop navigation (hidden on mobile) -->
				<nav class="hidden gap-4 text-sm sm:flex">
					{#each navItems as item (item.route)}
						<a
							href={resolve(item.route)}
							class="hover:text-ink"
							class:text-ink={page.url.pathname === resolve(item.route)}
							class:text-ink-muted={page.url.pathname !== resolve(item.route)}
						>
							{item.label}
						</a>
					{/each}
				</nav>
			{/if}

			<!-- The trailing edge is the identity and its theme, at every
			     width, and carries the row's only ml-auto. -->
			<div class="ml-auto flex shrink-0 items-center gap-3 text-sm">
				<ThemeToggle />
				{#if session.user}
					<UserMenu
						label={session.user.email || session.user.username}
						busy={signingOut}
						onsignout={signOut}
					/>
				{:else if session.resolved && !onLoginPage}
					<Button variant="ghost" onclick={() => goToLogin(page.url.pathname)}>Sign in</Button>
				{/if}
			</div>
		</div>

		<!-- Mobile navigation menu (shown when open on mobile) -->
		{#if session.signedIn && navOpen}
			<nav
				id="mobile-nav"
				class="border-t border-border-subtle bg-surface-muted px-4 py-3 sm:hidden"
			>
				<ul class="flex flex-col gap-2 text-sm">
					{#each navItems as item (item.route)}
						<li>
							<a
								href={resolve(item.route)}
								onclick={closeNav}
								class="block rounded px-2 py-1.5 transition hover:bg-surface"
								class:bg-accent={page.url.pathname === resolve(item.route)}
								class:text-accent-ink={page.url.pathname === resolve(item.route)}
								class:text-ink-muted={page.url.pathname !== resolve(item.route)}
							>
								{item.label}
							</a>
						</li>
					{/each}
				</ul>
			</nav>
		{/if}
	</header>

	<main class="flex w-full flex-1 flex-col items-center px-6 py-10">
		{@render children()}
	</main>

	<Footer {version} />
</div>
