<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { getBranding } from '#lib/branding.svelte.js';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	// Where the server sends a document GET of /approve/<id> it refused to
	// serve (middleware.ApprovalClaimMiddleware): approval pages are bound
	// to the first browser that opens them, so a later client is turned
	// away here. `reason` distinguishes the cookie-blocked case, which
	// needs its own fix, from the ordinary spent link.
	const cookiesBlocked = $derived(page.url.searchParams.get('reason') === 'cookies');

	// The deployment's support contact, for the reader who was sent this
	// link by someone else: that is the case the server treats as most
	// likely (certrequest_claim.go), and an operator wants to hear of it.
	const branding = $derived(getBranding());
	const supportEmail = $derived(branding.support_email ?? '');
	const supportLabel = $derived(branding.support_label || supportEmail);
</script>

<svelte:head><title>Approval link unavailable · ssoossh</title></svelte:head>

<!-- Neither case can be fixed from this page, so both lead back into the
     app rather than leave the reader on a dead end (DESIGN.md, "States"). -->
{#snippet wayOut(label: string)}
	<div class="mt-4">
		<a
			href={resolve('dashboard')}
			class="inline-flex min-h-target items-center text-dense font-medium text-accent hover:underline"
			data-testid="claim-dashboard">{label}</a
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
			<!-- The dashboard needs a session cookie too, so the way out says
			     when it will work rather than leading into the same loop. -->
			{@render wayOut('Go to your dashboard once cookies are allowed')}
		</div>
	{:else}
		<!-- Neutral on purpose: the most common way to land here is a mail or
		     chat security scanner having fetched the link before the person
		     it was sent to could, so this page must not read as an
		     accusation. -->
		<div data-testid="claim-already-opened" class="rounded-lg bg-surface p-5">
			<PageHeading title="This approval link was already opened">
				{#snippet sub()}
					Approval links work once, in the browser that opens them first. Nothing was approved.
				{/snippet}
			</PageHeading>
			<!-- Two readers land here, and the next step differs. The requester
			     who copied their own link somewhere runs the client again. A
			     person someone else sent this link to must not ask the sender
			     for another: the scanner that burned this one is what stopped
			     an approval of the sender's request, and a fresh link opened
			     directly would get round it. -->
			<ul class="mt-4 flex flex-col gap-3 text-sm text-ink" data-testid="claim-next-steps">
				<li>
					<strong>If you ran ssoossh yourself</strong> and copied the link into mail, chat or another
					device, something there opened it first, often security software scanning links. Run the client
					again and open the new link straight from your terminal.
				</li>
				<li data-testid="claim-sent-by-someone">
					<strong>If someone else sent you this link,</strong> do not ask them for another one.
					Approval links only ever come from your own terminal.
					{#if supportEmail}
						<!-- A mailto:, not a route: resolve() has nothing to check a
						     mail client handoff against. -->
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
						Tell
						<a class="font-medium text-accent hover:underline" href="mailto:{supportEmail}"
							>{supportLabel}</a
						>.
					{:else}
						Tell your administrator.
					{/if}
				</li>
			</ul>
			{@render wayOut('Go to your dashboard')}
		</div>
	{/if}
</PageShell>
