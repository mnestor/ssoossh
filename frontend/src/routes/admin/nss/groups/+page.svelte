<script lang="ts">
	import NSSEntryList from '#lib/components/NSSEntryList.svelte';
	import NSSViewSwitch from '#lib/components/NSSViewSwitch.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	// The stored NSS groups, for finding one by name, gid or DN. Private
	// groups are never stored, so they are not here; each is on its account.
</script>

<svelte:head><title>Stored groups · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="Stored groups" testid="nss-groups-heading">
		{#snippet sub()}
			The groups the sync has stored for hosts. Private groups are never stored; each one is on its
			account's page.
		{/snippet}
		{#snippet action()}<NSSViewSwitch current="groups" />{/snippet}
	</PageHeading>

	<NSSEntryList kind="group" />
</PageShell>
