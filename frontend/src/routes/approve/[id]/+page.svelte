<script lang="ts">
	import { page } from '$app/state';
	import { approveRequest, denyRequest, getRequestDetail } from '$lib/api/endpoints';
	import type { RequestDetail } from '$lib/api/types';
	import { describeLoadError, type LoadFailure } from '$lib/approval';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import { session } from '$lib/session.svelte';
	import Alert from '$lib/components/Alert.svelte';
	import ApprovalView from '$lib/components/ApprovalView.svelte';
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
	let selectedServiceAccount = $state<string | null>(null);
	let selectedPrincipals = $state<string[]>([]);
	let notificationEmail = $state('');

	// Build the list of principals the approver holds: username plus other accounts,
	// deduplicated and in that order.
	//
	// Deduplicated with indexOf rather than a Set: svelte/prefer-svelte-reactivity
	// rejects a built-in Set here, and reaching for SvelteSet instead would
	// imply reactive state this does not have — the collection is scratch,
	// built fresh on every evaluation of the derived and discarded once the
	// array is returned. The lists are a handful of entries.
	const userPrincipals = $derived.by(() => {
		const principals: string[] = [];
		const add = (name: string) => {
			if (name && !principals.includes(name)) {
				principals.push(name);
			}
		};
		if (session.user?.username) {
			add(session.user.username);
		}
		for (const account of session.user?.other_accounts ?? []) {
			add(account);
		}
		return principals;
	});

	// Initialize selected principals to the username on first load.
	$effect(() => {
		if (detail && !selectedPrincipals.length && session.user?.username) {
			selectedPrincipals = [session.user.username];
		}
	});

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
				// Guard against a stale response: if id has moved on to a
				// different request since this fetch started, a slow
				// response here must not overwrite what's now current.
				if (requestID !== id) {
					return;
				}
				detail = loaded;
			})
			.catch((cause) => {
				// An aborted fetch is this effect being torn down, not a
				// failure worth rendering.
				if (controller.signal.aborted) {
					return;
				}
				// A signed-out visitor is the ordinary way this page is
				// reached: the approval URL is printed by a client that has
				// no browser session. Send them to /login rather than
				// rendering a sign-in prompt here, so they get the real
				// login screen and, where a deployment sets one, the consent
				// notice that gates it.
				if (redirectIfUnauthenticated(cause)) {
					return;
				}
				failure = describeLoadError(cause);
			});

		return () => controller.abort();
	});

	/** decide records one approve-or-deny decision and reflects the result.
	 * The certificate itself never appears here — it is signed
	 * asynchronously and delivered on the waiting client's own SSE stream
	 * (docs/internals/signing-pipeline.md). */
	async function decide(action: 'approved' | 'denied') {
		busy = true;
		actionError = null;
		try {
			if (action === 'approved') {
				await approveRequest(id, {
					serviceAccount: selectedServiceAccount ?? undefined,
					principals: selectedPrincipals,
					notificationEmail: notificationEmail.trim() || undefined
				});
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
	<div class="w-full max-w-[560px]">
		<Card title={failure.title} testid="load-failure-{failure.kind}">
			<p class="text-sm text-ink-muted">{failure.message}</p>
		</Card>
	</div>
{:else if detail}
	<ApprovalView
		{detail}
		{busy}
		{actionError}
		{outcome}
		serviceAccounts={session.user?.service_accounts ?? []}
		bind:selectedServiceAccount
		bind:notificationEmail
		{userPrincipals}
		bind:selectedPrincipals
		onapprove={() => decide('approved')}
		ondeny={() => decide('denied')}
	/>
{:else}
	<div class="w-full max-w-[560px]">
		<Alert testid="loading-request">Loading request…</Alert>
	</div>
{/if}
