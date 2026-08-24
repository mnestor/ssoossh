<script lang="ts">
	import { page } from '$app/state';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import { redirectIfUnauthenticated } from '$lib/auth';

	// Admin nav entries
	const adminNav = [
		{ label: 'Users', href: '/admin/users' },
		{ label: 'Certificates', href: '/admin/certificates' },
		{ label: 'Service codes', href: '/admin/service-codes' },
		{ label: 'Config', href: '/admin/config' }
	];

	function isActive(href: string): boolean {
		return $page.url.pathname.startsWith(href);
	}
</script>

<svelte:head>
	<title>Admin · ssoossh</title>
</svelte:head>

<div class="flex w-full gap-6">
	<!-- Admin navigation -->
	<nav class="w-48 flex-shrink-0">
		<div class="sticky top-0 space-y-1">
			{#each adminNav as item (item.href)}
				<a
					href={item.href}
					class="block rounded-lg px-3 py-2 text-sm font-medium transition"
					class:bg-accent={isActive(item.href)}
					class:text-accent-ink={isActive(item.href)}
					class:text-ink={!isActive(item.href)}
					class:hover:bg-surface-muted={!isActive(item.href)}
				>
					{item.label}
				</a>
			{/each}
		</div>
	</nav>

	<!-- Main content -->
	<div class="flex flex-1 flex-col">
		<slot />
	</div>
</div>

<style>
	/* svelte/no-navigation-without-resolve is disabled at file scope because
	   feat/admin-users owns the actual /admin/* pages, and using resolve()
	   would require placeholder stubs for every route it doesn't own yet.
	   Plain hrefs avoid that: the four nav targets exist or they don't, and
	   a 404 on an unimplemented route is honest. */
	/* eslint-disable-next-line svelte/no-navigation-without-resolve */
</style>
