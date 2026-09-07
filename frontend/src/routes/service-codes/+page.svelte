<script lang="ts">
	import { pushState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { getCurrentUser, listServiceEnrollments } from '$lib/api/endpoints';
	import type { ServiceEnrollment } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import SectionLabel from '$lib/components/SectionLabel.svelte';
	import ServiceAccountRow from '$lib/components/ServiceAccountRow.svelte';
	import ServiceCodeRow from '$lib/components/ServiceCodeRow.svelte';
	import { isExpired } from '$lib/format';

	// The service codes for every account this identity holds: the accounts,
	// then one account's codes. One code is a page of its own,
	// /service-codes/[id].
	//
	// Account first because that is what ownership is now — a code belongs to
	// its service account, and everyone holding the account holds the code
	// (see docs/proposals/enrollment-group-ownership.md). The codes themselves
	// are never part of any level: `service enroll` prints one once and the
	// server keeps it only to match a redemption against.
	let enrollments = $state<ServiceEnrollment[]>([]);
	let heldAccounts = $state<string[]>([]);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	// Rows say how long ago something was and how long a code has left, so
	// they need a clock that moves.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	function accountOf(enrollment: ServiceEnrollment): string {
		return enrollment.service_account || enrollment.principals[0] || '';
	}

	// One entry per account the identity holds, including accounts with no
	// codes at all. That zero state is the reason this level exists: an
	// account with nothing redeemable is exactly the unattended job about to
	// stop working, and a list built only from enrollments would not mention
	// it.
	//
	// Accounts are unioned with the ones actually on the codes, so an
	// enrollment whose account has since left the identity's claim still
	// appears rather than vanishing from a page that can still open it.
	// Deduplicated with includes rather than a Set: svelte/prefer-svelte-reactivity
	// rejects a built-in Set here, and SvelteSet would imply reactive state this
	// does not have — the collection is scratch, rebuilt on every evaluation of
	// the derived and discarded once the array is returned.
	const accounts = $derived.by(() => {
		const names: string[] = [];
		const add = (name: string) => {
			if (name && !names.includes(name)) {
				names.push(name);
			}
		};
		for (const account of heldAccounts) {
			add(account);
		}
		for (const enrollment of enrollments) {
			add(accountOf(enrollment));
		}

		return names.sort().map((account) => {
			const codes = enrollments.filter((e) => accountOf(e) === account);
			const live = codes.filter((e) => !isExpired(e.expires_at, now));
			const expiries = live.map((e) => e.expires_at).sort();
			const retrievals = codes
				.map((e) => e.last_retrieved_at)
				.filter((at): at is string => !!at)
				.sort();

			return {
				account,
				liveCount: live.length,
				expiredCount: codes.length - live.length,
				nextExpiry: expiries[0],
				lastRetrievedAt: retrievals[retrievals.length - 1]
			};
		});
	});

	// Which account is open, from the pushed state and falling back to the
	// search parameter a pasted link arrives with. It is the one level that
	// is still a view of this page rather than a route of its own: it is this
	// list, filtered, and the codes are already loaded behind it.
	const openAccount = $derived(
		'accountName' in page.state ? page.state.accountName : page.url.searchParams.get('account')
	);

	const accountCodes = $derived(enrollments.filter((e) => accountOf(e) === openAccount));

	// Expired codes stay under their own heading rather than dropping off the
	// page: a job that stopped working is explained by the code beneath it,
	// and hiding the row hides the explanation.
	const live = $derived(accountCodes.filter((e) => !isExpired(e.expires_at, now)));
	const dead = $derived(accountCodes.filter((e) => isExpired(e.expires_at, now)));

	// Shallow-route within this same page (a query parameter), not a
	// navigation to a different route id — resolve() is for the latter, so it
	// does not apply here.
	//
	// Closing records an explicit null rather than an absent key: an absent
	// one means "nothing has been opened or closed here yet", which falls
	// back to the search parameter — and on a page reached by a pasted link,
	// that would reopen what was just closed.
	function openAccountView(account: string | null) {
		const url = new URL(page.url);
		if (account) {
			url.searchParams.set('account', account);
		} else {
			url.searchParams.delete('account');
		}
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, { ...page.state, accountName: account });
	}

	const closeAccountView = () => openAccountView(null);

	$effect(() => {
		const controller = new AbortController();

		// The held accounts come from the session rather than from the codes,
		// which is the only way an account with no codes can be listed at all.
		// A failure to read them is not a failure of the page: the accounts
		// present on the codes still render, so it is left to the codes.
		Promise.all([
			listServiceEnrollments(controller.signal),
			getCurrentUser(controller.signal).catch(() => null)
		])
			.then(([result, user]) => {
				enrollments = result.enrollments;
				heldAccounts = user?.service_accounts ?? [];
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
</script>

<svelte:head><title>Service codes · ssoossh</title></svelte:head>

<PageShell width="wide">
	{#if openAccount}
		<PageHeading
			title={openAccount}
			testid="service-codes-heading"
			back={{
				onclick: closeAccountView,
				label: 'All service accounts',
				testid: 'service-codes-back'
			}}
		>
			{#snippet sub()}
				Every enrollment code approved for <code class="font-mono">{openAccount}</code>. Anyone with
				access to this account can see and manage them, whoever approved each one. Open a row for
				what it hands out and how long it stays redeemable.
			{/snippet}
		</PageHeading>
	{:else}
		<PageHeading title="Service enrollment codes" testid="service-codes-heading">
			{#snippet sub()}
				The service accounts you have access to, and the codes approved for each. A code belongs to
				its account rather than to whoever approved it, so you see every code for these accounts.
				The codes themselves are not shown: <code class="font-mono">ssoossh service enroll</code>
				prints each one once, and the server keeps it only to match a redemption against.
			{/snippet}
		</PageHeading>
	{/if}

	{#if loadError}
		<Alert variant="error" title="Could not load your service codes">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if openAccount}
		{#if accountCodes.length === 0}
			<p class="text-sm text-ink-muted" data-testid="account-empty">
				No enrollment codes for this account yet. One is created when a request from
				<code class="font-mono">ssoossh service enroll</code> is approved for it.
			</p>
		{:else}
			{#if live.length > 0}
				<div class="flex flex-col gap-2.5">
					{#each live as enrollment (enrollment.id)}
						<ServiceCodeRow
							{enrollment}
							{now}
							testid="service-code-row"
							showAccount={false}
							href={resolve(`/service-codes/${enrollment.id}`)}
						/>
					{/each}
				</div>
			{/if}

			{#if dead.length > 0}
				<div class="flex flex-col gap-2.5">
					<SectionLabel>Expired codes</SectionLabel>
					{#each dead as enrollment (enrollment.id)}
						<ServiceCodeRow
							{enrollment}
							{now}
							testid="service-code-row"
							showAccount={false}
							href={resolve(`/service-codes/${enrollment.id}`)}
						/>
					{/each}
				</div>
			{/if}
		{/if}
	{:else if accounts.length === 0}
		<p class="text-sm text-ink-muted">
			You do not have access to any service accounts. Service enrollment codes are approved for an
			account, and this page lists the accounts your identity carries.
		</p>
	{:else}
		<div class="flex flex-col gap-2.5">
			{#each accounts as entry (entry.account)}
				<ServiceAccountRow
					testid="service-account-row"
					account={entry.account}
					liveCount={entry.liveCount}
					expiredCount={entry.expiredCount}
					nextExpiry={entry.nextExpiry}
					lastRetrievedAt={entry.lastRetrievedAt}
					{now}
					onclick={() => openAccountView(entry.account)}
				/>
			{/each}
		</div>
	{/if}
</PageShell>
