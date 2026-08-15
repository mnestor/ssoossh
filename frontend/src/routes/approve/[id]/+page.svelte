<script lang="ts">
	import { page } from '$app/state';
	import { approveRequest, denyRequest, getRequestDetail } from '$lib/api/endpoints';
	import type { RequestDetail } from '$lib/api/types';
	import { describeLoadError, type LoadFailure } from '$lib/approval';
	import { errorMessage, startLogin } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import ApprovalView from '$lib/components/ApprovalView.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';

	// The page a client prints as approval_url. It is the only place a human
	// authorizes certificate issuance, so it does two things and nothing
	// else: show what would be granted, and record one decision.
	const id = $derived(page.params.id ?? '');

	let detail = $state<RequestDetail | null>(null);
	let failure = $state<LoadFailure | null>(null);
	let busy = $state(false);
	let actionError = $state<string | null>(null);
	let outcome = $state<'approved' | 'denied' | null>(null);

	// GET .../requests/:id is also what binds the request to the caller
	// server-side, so loading this page is itself the claim. A second person
	// opening the same link is refused here, before any button exists to
	// click.
	$effect(() => {
		const requestID = id;
		if (!requestID) {
			return;
		}

		const controller = new AbortController();
		detail = null;
		failure = null;
		outcome = null;
		actionError = null;

		getRequestDetail(requestID, controller.signal)
			.then((loaded) => {
				detail = loaded;
			})
			.catch((cause) => {
				// An aborted fetch is this effect being torn down, not a
				// failure worth rendering.
				if (controller.signal.aborted) {
					return;
				}
				failure = describeLoadError(cause);
			});

		return () => controller.abort();
	});

	/** decide records one approve-or-deny decision and reflects the result.
	 * The certificate itself never appears here — it is signed
	 * asynchronously and delivered on the waiting client's own SSE stream
	 * (docs/signing-pipeline.md). */
	async function decide(action: 'approved' | 'denied') {
		busy = true;
		actionError = null;
		try {
			if (action === 'approved') {
				await approveRequest(id);
			} else {
				await denyRequest(id);
			}
			outcome = action;
		} catch (cause) {
			actionError = errorMessage(cause);
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Approve a certificate request · ssoossh</title></svelte:head>

{#if failure}
	<Card title={failure.title}>
		<p class="text-sm text-ink-muted">{failure.message}</p>
		{#if failure.signIn}
			<div class="mt-5">
				<Button onclick={() => startLogin(`/approve/${id}`)}>Sign in to continue</Button>
			</div>
		{/if}
	</Card>
{:else if detail}
	<ApprovalView
		{detail}
		{busy}
		{actionError}
		{outcome}
		onapprove={() => decide('approved')}
		ondeny={() => decide('denied')}
	/>
{:else}
	<Alert>Loading request…</Alert>
{/if}
