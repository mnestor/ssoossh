<script lang="ts">
	import { page } from '$app/state';
	import Card from '$lib/components/Card.svelte';

	// Where the server sends a document GET of /approve/<id> it refused to
	// serve (middleware.ApprovalClaimMiddleware): approval pages are bound
	// to the first browser that opens them, so a later client is turned
	// away here. `reason` distinguishes the cookie-blocked case, which
	// needs its own fix, from the ordinary spent link.
	const cookiesBlocked = $derived(page.url.searchParams.get('reason') === 'cookies');
</script>

<svelte:head><title>Approval link unavailable · ssoossh</title></svelte:head>

<div class="w-full max-w-[560px]">
	{#if cookiesBlocked}
		<Card title="This site needs cookies to approve requests" testid="claim-cookies-blocked">
			<p class="text-sm text-ink-muted">
				Approval links are tied to the first browser that opens them, and that tie is carried by a
				cookie this browser did not send back. Allow cookies for this site, then run the client
				again to get a fresh link.
			</p>
		</Card>
	{:else}
		<!-- Neutral on purpose: the most common way to land here is a mail or
		     chat security scanner having fetched the link before the person
		     it was sent to could, so this page must not read as an
		     accusation. -->
		<Card title="This approval link was already opened" testid="claim-already-opened">
			<p class="text-sm text-ink-muted">
				Approval links are single-use, and something opened this one first. If you did not open it,
				that is often security software scanning links in mail or chat. Nothing was approved. Run
				the client again to get a fresh link, and open it directly in your browser.
			</p>
		</Card>
	{/if}
</div>
