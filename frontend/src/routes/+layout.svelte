<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import '../app.css';
	import { logout } from '$lib/api/endpoints';
	import { errorMessage, startLogin } from '$lib/auth';
	import Button from '$lib/components/Button.svelte';
	import { session } from '$lib/session.svelte';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Loaded once for the whole app rather than per page: every screen wants
	// the same answer and it only changes at login or logout. Not awaited —
	// the nav renders signed-out until it resolves, and pages that actually
	// need an identity get their own 401 from their own call.
	session.load();

	let signingOut = $state(false);

	// Route ids rather than URLs, resolved at each use site: resolve() checks
	// them against the actual route tree, so a link to a page that no longer
	// exists fails the build instead of 404ing in production.
	const navItems = [
		{ route: '/dashboard', label: 'Dashboard' },
		{ route: '/logs/me', label: 'History' }
	] as const;

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
		<div class="mx-auto flex max-w-4xl flex-wrap items-center gap-4 px-4 py-3">
			<a href={resolve('/')} class="font-semibold">ssoossh</a>

			{#if session.signedIn}
				<nav class="flex gap-4 text-sm">
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

			<div class="ml-auto flex items-center gap-3 text-sm">
				{#if session.user}
					<span class="text-ink-muted">{session.user.email || session.user.username}</span>
					<Button variant="ghost" disabled={signingOut} onclick={signOut}>Sign out</Button>
				{:else if session.resolved}
					<Button variant="ghost" onclick={() => startLogin(page.url.pathname)}>Sign in</Button>
				{/if}
			</div>
		</div>
	</header>

	<main class="mx-auto w-full max-w-4xl flex-1 px-4 py-8">
		{@render children()}
	</main>

	<footer class="mx-auto w-full max-w-4xl px-4 py-6 text-xs text-ink-muted">
		Certificates issued here are short-lived. ssoossh never sees your private key.
	</footer>
</div>
