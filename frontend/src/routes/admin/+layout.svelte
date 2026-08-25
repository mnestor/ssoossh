<script lang="ts">
	/* eslint-disable svelte/no-navigation-without-resolve */
	// The nav below uses plain hrefs rather than resolve(). This nav names all
	// four admin sections, but each section's page lands on its own feature
	// branch, and resolve() is typed against the routes that exist in THIS
	// tree. Using it would force a placeholder page for every absent route,
	// and a placeholder sharing a path with another branch's real page is a
	// merge waiting to resolve the wrong way. Same file-level form as
	// Footer.svelte, whose links are external for a different reason.
	import { page } from '$app/state';
	import { session } from '$lib/session.svelte';
	import { goToLogin } from '$lib/auth';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Gate the entire admin area on auditor access. The server re-checks
	// every read, so this is display-only and provides no security.
	//
	// Only a signed-OUT visitor is sent to login. Someone signed in without
	// auditor access stays here and gets the explanation below: logging in
	// again cannot grant admin, so bouncing them to a screen they have
	// already satisfied is a loop rather than an answer.
	$effect(() => {
		if (session.resolved && !session.signedIn) {
			goToLogin(page.url.pathname);
		}
	});

	// The admin sections. This array includes routes managed by different feature branches.
	// Each branch owns a subset of these entries and expects all four to be present.
	const adminNav = [
		{ route: '/admin/users', label: 'Users' },
		{ route: '/admin/certificates', label: 'Certificates' },
		{ route: '/admin/service-codes', label: 'Service codes' },
		{ route: '/admin/config', label: 'Config' }
	] as const satisfies ReadonlyArray<{ route: string; label: string }>;
</script>

{#if session.user?.is_auditor}
	<div class="min-h-screen w-full">
		<!-- Admin sidebar navigation -->
		<div class="flex flex-col gap-1 border-b border-border-subtle bg-surface-muted px-6 py-4">
			<h2 class="mb-2 text-sm font-semibold text-ink">Admin</h2>
			<nav class="flex flex-col gap-1">
				{#each adminNav as item (item.route)}
					<a
						href={item.route}
						class="rounded px-3 py-2 text-sm transition"
						class:bg-accent={page.url.pathname === item.route}
						class:text-accent-ink={page.url.pathname === item.route}
						class:text-ink-muted={page.url.pathname !== item.route}
						class:hover:bg-surface={page.url.pathname !== item.route}
					>
						{item.label}
					</a>
				{/each}
			</nav>
		</div>

		<!-- Page content -->
		<div class="px-6 py-10">
			{@render children()}
		</div>
	</div>
{:else if session.resolved}
	<!-- Signed in, but without auditor access. -->
	<div
		data-testid="admin-access-denied"
		class="flex flex-col items-center justify-center gap-4 py-12"
	>
		<p class="text-ink-muted">You do not have access to the admin area.</p>
	</div>
{:else}
	<!-- Loading -->
	<div class="flex flex-col items-center justify-center gap-4 py-12">
		<p class="text-ink-muted">Loading...</p>
	</div>
{/if}
