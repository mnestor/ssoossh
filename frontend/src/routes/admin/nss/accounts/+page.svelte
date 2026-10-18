<script lang="ts">
	import NSSEntryList from '#lib/components/NSSEntryList.svelte';
	import NSSViewSwitch from '#lib/components/NSSViewSwitch.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	// The stored NSS accounts, for finding one by name, uid or DN. Read only:
	// accepting a changed uid and purging a tombstone stay on the Directory
	// page, for admins.
</script>

<svelte:head><title>Stored accounts · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="Stored accounts" testid="nss-accounts-heading">
		{#snippet sub()}
			The accounts the sync has stored for hosts. Open one to see what a host's lookup is served.
		{/snippet}
		{#snippet action()}<NSSViewSwitch current="accounts" />{/snippet}
	</PageHeading>

	<NSSEntryList kind="account" />
</PageShell>
