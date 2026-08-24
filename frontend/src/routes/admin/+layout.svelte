<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { session } from '$lib/session.svelte';
	import { goToLogin } from '$lib/auth';
	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();

	// Gate the entire admin area on auditor access. The server re-checks
	// every read, so the client-side gate is display-only and does not
	// provide security. Redirect to login if not auditor.
	$effect(() => {
		if (session.resolved && !session.user?.is_auditor) {
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
						href={resolve(item.route)}
						class="rounded px-3 py-2 text-sm transition"
						class:bg-accent={page.url.pathname === resolve(item.route)}
						class:text-accent-ink={page.url.pathname === resolve(item.route)}
						class:text-ink-muted={page.url.pathname !== resolve(item.route)}
						class:hover:bg-surface={page.url.pathname !== resolve(item.route)}
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
	<!-- Not authorized; go to login -->
	<div class="flex flex-col items-center justify-center gap-4 py-12">
		<p class="text-ink-muted">You do not have access to the admin area.</p>
	</div>
{:else}
	<!-- Loading -->
	<div class="flex flex-col items-center justify-center gap-4 py-12">
		<p class="text-ink-muted">Loading...</p>
	</div>
{/if}
