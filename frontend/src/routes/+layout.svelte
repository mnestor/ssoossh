<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import '../app.css';
	import { logout } from '$lib/api/endpoints';
	import { errorMessage, startLogin } from '$lib/auth';
	import { loadBranding, getBranding } from '$lib/branding.svelte';
	import Button from '$lib/components/Button.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import { session } from '$lib/session.svelte';
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

	const branding = $derived(getBranding());

	let signingOut = $state(false);
	let navOpen = $state(false);

	// Route ids rather than URLs, resolved at each use site: resolve() checks
	// them against the actual route tree, so a link to a page that no longer
	// exists fails the build instead of 404ing in production.
	const navItems = [
		{ route: '/dashboard', label: 'Dashboard' },
		{ route: '/logs/me', label: 'History' }
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
		<div class="mx-auto flex max-w-4xl items-center gap-4 px-4 py-3">
			<a href={resolve('/')} class="flex items-center gap-2 font-semibold">
				{#if branding.logo_url}
					<img src={branding.logo_url} alt="Organization logo" class="h-6 w-6 object-contain" />
				{/if}
				<span>ssoossh</span>
				{#if branding.org_name}
					<span class="rounded bg-surface-muted px-2 py-0.5 text-xs font-medium text-ink-muted">
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

				<!-- Mobile menu button -->
				<button
					onclick={() => (navOpen = !navOpen)}
					aria-expanded={navOpen}
					aria-label="Toggle navigation menu"
					class="ml-auto flex sm:hidden"
				>
					<Icon name="menu" size="md" />
				</button>
			{/if}

			<div class="ml-auto flex items-center gap-3 text-sm sm:ml-0">
				{#if session.user}
					<span class="hidden text-ink-muted sm:inline">{session.user.email || session.user.username}</span>
					<Button variant="ghost" disabled={signingOut} onclick={signOut}>Sign out</Button>
				{:else if session.resolved}
					<Button variant="ghost" onclick={() => startLogin(page.url.pathname)}>Sign in</Button>
				{/if}
			</div>
		</div>

		<!-- Mobile navigation menu (shown when open on mobile) -->
		{#if session.signedIn && navOpen}
			<nav class="border-t border-border-subtle bg-surface-muted px-4 py-3 sm:hidden">
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

	<main class="mx-auto w-full max-w-4xl flex-1 px-4 py-8">
		{@render children()}
	</main>

	<footer class="mx-auto w-full max-w-4xl px-4 py-6 text-xs text-ink-muted">
		Certificates issued here are short-lived. ssoossh never sees your private key.
	</footer>
</div>
