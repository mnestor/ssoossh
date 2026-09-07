<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { expireOwnEnrollment, listServiceEnrollments } from '$lib/api/endpoints';
	import type { ServiceEnrollment } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import ExpireCodeAction from '$lib/components/ExpireCodeAction.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import ServiceCodeDetail from '$lib/components/ServiceCodeDetail.svelte';
	import { isExpired } from '$lib/format';

	// One service enrollment code, at its own address. This used to be a
	// dialog over the list, which meant a code could not be linked to,
	// reloaded, or left with the browser's own Back — and the panel is a
	// screenful of fields, a redemption log and two controls, which is a page
	// rather than something to read over the top of a list.
	//
	// Resolved out of the caller's own enrollment list rather than from an
	// endpoint of its own: the server has no per-id route for a holder, and
	// GET /api/certs/service/enrollments is already scoped to the accounts
	// the identity holds, so a code that is not in that answer is a code this
	// reader may not see. Looking it up here therefore lands on the same
	// authorization the list does.
	const id = $derived(page.params.id ?? '');

	let enrollments = $state<ServiceEnrollment[]>([]);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	// The remaining lifetime needs a clock that moves, the same one the list
	// behind this page runs.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	const enrollment = $derived(enrollments.find((e) => e.id === id));

	// An already-expired code needs no retire control: the outcome it would
	// produce is already true.
	const expired = $derived(enrollment ? isExpired(enrollment.expires_at, now) : false);

	// The account owning the code, which is both what the back chip returns
	// to and, on a code whose account has since left the identity's claim,
	// still the right label for where it came from.
	const account = $derived(
		enrollment ? enrollment.service_account || enrollment.principals[0] || '' : ''
	);

	// Back to the account's own codes when there is an account to go back to,
	// and to the account list when there is not — a code that could not be
	// resolved has no account to name.
	const accountQuery = $derived(account ? `?account=${encodeURIComponent(account)}` : '');
	const backHref = $derived(`${resolve('/service-codes')}${accountQuery}`);
	const backLabel = $derived(account ? `All codes for ${account}` : 'All service accounts');

	$effect(() => {
		const controller = new AbortController();

		listServiceEnrollments(controller.signal)
			.then((result) => {
				enrollments = result.enrollments;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) {
					return;
				}
				loadError = errorMessage(cause);
				hasLoaded = true;
			});

		return () => controller.abort();
	});

	// A retired code stays readable, but the page it was retired from is the
	// account's list: the row there now says so, which is the confirmation.
	function afterExpired() {
		// backHref is already a resolved path with the account appended as a
		// search parameter, which resolve() does not take.
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		goto(backHref);
	}
</script>

<svelte:head><title>Service code · ssoossh</title></svelte:head>

<PageShell width="wide">
	<!-- A visible heading, where there used to be an sr-only one. The page
	     needs somewhere to hang its action: retiring the code is the one
	     thing here that ends it rather than describing it, and it belongs in
	     the top right, the same place an account is disabled from.
	
	     No sub line. It briefly carried the key ID, which is a row in "What
	     it hands out" a few lines below — the heading is not the place to
	     say a thing the page is about to say properly, with a label on it.
	     Every other sub line in the app is a sentence about the screen, not
	     a field lifted out of it. -->
	<PageHeading
		eyebrow="Service code"
		title={account || 'Service code'}
		testid="service-code-heading"
		back={{ href: backHref, label: backLabel, testid: 'service-code-back' }}
	>
		{#snippet action()}
			{#if enrollment && !expired}
				{@const enrollmentId = enrollment.id}
				<ExpireCodeAction
					testid="expire-code"
					expire={(reason) => expireOwnEnrollment(enrollmentId, reason)}
					onexpired={afterExpired}
				/>
			{/if}
		{/snippet}
	</PageHeading>

	{#if loadError}
		<Alert variant="error" title="Could not load this service code">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if !enrollment}
		<!-- One message for "no such code" and "not yours": the list this
		     resolves against is already scoped to the accounts the identity
		     holds, so the two are the same answer from here, and telling them
		     apart would report the existence of codes the reader may not
		     see. -->
		<Alert variant="error" title="No such service code" testid="service-code-missing">
			No code with that ID is approved for any service account you have access to.
		</Alert>
	{:else}
		<ServiceCodeDetail {enrollment} {now} />
	{/if}
</PageShell>
