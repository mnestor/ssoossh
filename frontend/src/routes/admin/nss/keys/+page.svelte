<script lang="ts">
	import { resolve } from '$app/paths';

	import NSSKeyAdminList from '#lib/components/NSSKeyAdminList.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { session } from '#lib/session.svelte.js';

	// Every fleet key, whoever manages it, for auditors and above (the admin
	// layout's gate). Read only: a row opens the key's own page, which
	// keeps every action. New key is the fleet key admin role's, and a
	// ghost so the search stays the one thing to reach for.
	const canCreate = $derived(session.user?.is_nss_key_admin ?? false);
</script>

<svelte:head><title>All NSS keys · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="All NSS keys" testid="nss-admin-keys-heading">
		{#snippet sub()}
			Every fleet key, whoever manages it. Find a key by its name, group, creator or id, or by an
			address a host used it from.
		{/snippet}
		{#snippet action()}
			{#if canCreate}
				<a
					href={`${resolve('/nss-keys/new')}?from=admin`}
					data-testid="nss-admin-keys-new"
					class="inline-flex min-h-target items-center justify-center gap-2 rounded-md border border-border-control px-4 py-2 text-sm font-medium text-ink transition duration-100 hover:bg-surface-muted active:scale-98"
				>
					New key
				</a>
			{/if}
		{/snippet}
	</PageHeading>

	<NSSKeyAdminList />
</PageShell>
