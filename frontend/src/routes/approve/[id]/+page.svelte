<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { approveRequest, denyRequest, getRequestDetail } from '#lib/api/endpoints.js';
	import type { RequestDetail } from '#lib/api/types.js';
	import { describeLoadError, type LoadFailure } from '#lib/approval.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { session } from '#lib/session.svelte.js';
	import Button from '#lib/components/Button.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import ApprovalView from '#lib/components/ApprovalView.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { coverWithDetail } from '#lib/detail.svelte.js';

	// The page a client prints as approval_url. It is the only place a human
	// authorizes certificate issuance, so it does two things and nothing
	// else: show what would be granted, and record one decision.
	const id = $derived(page.params.id ?? '');

	// One of the pages the detail level covers; ApprovalView reads the level
	// (DESIGN.md, "Detail levels").
	coverWithDetail();

	let detail = $state<RequestDetail | null>(null);
	let failure = $state<LoadFailure | null>(null);
	let busy = $state(false);
	let actionError = $state<string | null>(null);
	let outcome = $state<'approved' | 'denied' | null>(null);
	let selectedServiceAccount = $state<string | null>(null);
	let selectedPrincipals = $state<string[]>([]);
	// Seeded from the request itself, never from the ceiling — see the
	// $effect below.
	let selectedExtensions = $state<string[]>([]);
	let extensionsSeeded = $state(false);
	let notificationEmail = $state('');
	// Bumped by Try again; the load below reads it, so a bump re-runs it.
	let attempt = $state(0);

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

	// Initialize selected principals on first load. For a PAM or console
	// request that is the local account being acted as, when the approver
	// holds it: the host matches the certificate's principals against that
	// account, so it is the one the approver almost always means. Otherwise,
	// and for a user request, it is the username. Either way the choice
	// stays on the page.
	$effect(() => {
		if (detail && !selectedPrincipals.length && session.user?.username) {
			const target = detail.target_account;
			const actsLocally = detail.type === 'pam' || detail.type === 'console';
			selectedPrincipals =
				actsLocally && target && userPrincipals.includes(target)
					? [target]
					: [session.user.username];
		}
	});

	// The extension picker starts at what the enrollment asked for, so an
	// approver who touches nothing approves exactly the request. That is
	// deliberately NOT the principal preselection above, which infers a
	// sensible default from the approver's own accounts: an extension is a
	// permission, and inferring one nobody asked for is how a ceiling turns
	// into a grant by accident.
	//
	// Seeded once rather than whenever the selection empties, because
	// clearing every extension is a legitimate choice here and must stick.
	$effect(() => {
		if (detail && !extensionsSeeded) {
			selectedExtensions = detail.granted?.extensions ?? [];
			extensionsSeeded = true;
		}
	});

	// GET .../requests/:id is also what binds the request to the caller
	// server-side, so loading this page is itself the claim. A second person
	// opening the same link is refused here, before any button exists to
	// click.
	$effect(() => {
		const requestID = id;
		void attempt;
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

	// The way out of a failure a second attempt can fix: the same request,
	// fetched again. The effect clears the failure and shows the loading
	// shape while it runs.
	function retry() {
		attempt += 1;
	}

	/** decide records one approve-or-deny decision and reflects the result.
	 * The certificate itself never appears here — it is signed
	 * asynchronously and delivered on the waiting client's own SSE stream
	 * (https://mnestor.github.io/ssoossh/internals/architecture/). */
	async function decide(action: 'approved' | 'denied') {
		busy = true;
		actionError = null;
		try {
			if (action === 'approved') {
				await approveRequest(id, {
					serviceAccount: selectedServiceAccount ?? undefined,
					principals: selectedPrincipals,
					// Sent only where a picker was actually offered. Elsewhere
					// the field stays absent, which the server reads as "the
					// approver expressed no choice" and leaves the request's
					// own set alone.
					extensions: detail?.type === 'service' ? selectedExtensions : undefined,
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

<!-- `wide`, the width every signed-in page uses, and with the rail beside
     it (see #lib/nav's focusRoutes). Reaching this page means signing in, so
     its reader is already inside the app; a 560px column floating in an
     otherwise empty window read as a different site rather than as a focused
     screen of this one. -->
<PageShell width="wide">
	{#if failure}
		<!-- Bound once: a snippet is its own closure, so the `failure` the
		     branch narrowed does not reach inside one. -->
		{@const loadFailure = failure}
		<div data-testid="load-failure-{failure.kind}" class="rounded-lg bg-surface p-5">
			<!-- The failure names itself: "This link was already used" is
			     the whole answer, and the card it sits in is what marks it
			     as a refusal rather than the screen it replaced. -->
			<PageHeading title={loadFailure.title}>
				{#snippet sub()}{loadFailure.message}{/snippet}
			</PageHeading>
			<!-- Every failure has a way out (DESIGN.md, "States"). One a
			     second attempt can fix offers it; a claimed or missing request
			     cannot be fixed from here, so it leads back into the app. -->
			<div class="mt-4">
				{#if loadFailure.kind === 'unknown'}
					<Button variant="ghost" onclick={retry} testid="load-failure-retry">Try again</Button>
				{:else}
					<a
						href={resolve('dashboard')}
						class="inline-flex min-h-target items-center text-dense font-medium text-accent hover:underline"
						data-testid="load-failure-dashboard">Go to your dashboard</a
					>
				{/if}
			</div>
		</div>
	{:else if detail}
		<ApprovalView
			{detail}
			{busy}
			{actionError}
			{outcome}
			serviceAccounts={session.user?.approvable_service_accounts ?? []}
			userOwnServiceAccounts={session.user?.user_own_service_accounts ?? []}
			bind:selectedServiceAccount
			bind:notificationEmail
			{userPrincipals}
			bind:selectedPrincipals
			bind:selectedExtensions
			onapprove={() => decide('approved')}
			ondeny={() => decide('denied')}
		/>
	{:else}
		<LoadingBlock shape="lines" count={5} label="Loading request…" testid="loading-request" />
	{/if}
</PageShell>
