<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	// Where the server sends a document GET of /approve/<id> it refused to
	// serve (middleware.ApprovalClaimMiddleware): approval pages are bound
	// to the first browser that opens them, so a later client is turned
	// away here. `reason` distinguishes the cookie-blocked case, which
	// needs its own fix, from the ordinary spent link.
	const cookiesBlocked = $derived(page.url.searchParams.get('reason') === 'cookies');
</script>

<svelte:head><title>Approval link unavailable · ssoossh</title></svelte:head>

<!-- Neither case can be fixed from this page, so both lead back into the
     app rather than leave the reader on a dead end (DESIGN.md, "States"). -->
{#snippet wayOut()}
	<div class="mt-4">
		<a
			href={resolve('dashboard')}
			class="inline-flex min-h-target items-center text-dense font-medium text-accent hover:underline"
			data-testid="claim-dashboard">Go to your dashboard</a
		>
	</div>
{/snippet}

<PageShell width="focus">
	{#if cookiesBlocked}
		<div data-testid="claim-cookies-blocked" class="rounded-lg bg-surface p-5">
			<PageHeading title="This site needs cookies to approve requests">
				{#snippet sub()}
					Approval links are tied to the first browser that opens them, and that tie is carried by a
					cookie this browser did not send back. Allow cookies for this site, then run the client
					again to get a fresh link.
				{/snippet}
			</PageHeading>
			{@render wayOut()}
		</div>
	{:else}
		<!-- Neutral on purpose: the most common way to land here is a mail or
		     chat security scanner having fetched the link before the person
		     it was sent to could, so this page must not read as an
		     accusation. -->
		<div data-testid="claim-already-opened" class="rounded-lg bg-surface p-5">
			<PageHeading title="This approval link was already opened">
				{#snippet sub()}
					Approval links are single-use, and something opened this one first. If you did not open
					it, that is often security software scanning links in mail or chat. Nothing was approved.
					Run the client again to get a fresh link, and open it directly in your browser.
				{/snippet}
			</PageHeading>
			{@render wayOut()}
		</div>
	{/if}
</PageShell>
