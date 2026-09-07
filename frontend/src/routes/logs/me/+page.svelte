<script lang="ts">
	import { resolve } from '$app/paths';
	import { listCertificates, listDeniedRequests } from '$lib/api/endpoints';
	import type { CertificateRecord, DeniedRequest } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import DeniedRow from '$lib/components/DeniedRow.svelte';
	import FilterGroup from '$lib/components/FilterGroup.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import ListStatus from '$lib/components/ListStatus.svelte';
	import SearchInput from '$lib/components/SearchInput.svelte';
	import { outcomeFilters, statusFilters, typeFilters } from '$lib/filters';
	import { describeList } from '$lib/listStatus';

	// Cursor-paginated decision history: what was issued, and what was
	// refused. The type filter and client-side pagination apply only to
	// loaded results — if the user filters to "console" and everything
	// loaded so far is "user", they see nothing until they load more pages.
	// This is the accepted tradeoff of load-more pagination over offset
	// pagination.
	//
	// Two sources because the server has two. /api/certs reads the
	// certificates table, and a denial never writes a row there, so the half
	// of somebody's decisions that says "no" is served by
	// /api/decisions/denied and interleaved here by time. That half is the
	// one an incident review asks about first, and until now this page could
	// not show it at all.
	let allCertificates = $state<CertificateRecord[]>([]);
	let allDenials = $state<DeniedRequest[]>([]);
	let nextCursor = $state<string | null>(null);
	let nextDenialCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	// Filter state. The vocabulary is shared with /admin/certificates —
	// same labels, same icons, same order; see $lib/filters. Only the
	// outcome group is this page's alone, because the admin list reads the
	// certificates table and a denial never writes a row there.
	//
	// Approved by default, not both. This page has always been the list of
	// certificates somebody holds, and that is what most visits are for;
	// opening it on a mixture would change what an existing reader gets
	// without their asking. A refusal is one click away instead, which is
	// the right place for a thing you go looking for.
	let selectedOutcome = $state('approved');
	let selectedType = $state('');
	let selectedStatus = $state('');
	let searchQuery = $state('');

	let currentPage = $state(1);
	const pageSize = 10;

	// What each certificate type's row is a record of.
	const rowEvents: Record<string, string> = {
		user: 'certificate requested',
		pam: 'certificate requested',
		console: 'console login',
		service: 'service key requested'
	};

	// Rows say how long ago something was, so they need a clock that moves.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// One list of two kinds of thing, newest first. A discriminated union
	// rather than a shared shape: the two have almost no fields in common,
	// and the row components take the record each was written for.
	type Entry =
		| { kind: 'certificate'; at: number; cert: CertificateRecord }
		| { kind: 'denial'; at: number; denial: DeniedRequest };

	const sorted = $derived<Entry[]>(
		[
			...allCertificates.map((cert): Entry => ({
				kind: 'certificate',
				at: new Date(cert.issued_at).getTime(),
				cert
			})),
			...allDenials.map((denial): Entry => ({
				kind: 'denial',
				at: new Date(denial.decided_at).getTime(),
				denial
			}))
		].sort((a, b) => b.at - a.at)
	);

	// No client-side sieve any more: the server takes q/type/status and
	// the outcome decides which of the two endpoints is read at all, so
	// what is loaded is already what was asked for. All that is left here
	// is the page-of-ten a reader steps through.
	const filtered = $derived(sorted);

	// Whether anything has been narrowed, which is what separates "you have
	// no history" from "nothing matches". Approved is the state the page
	// opens in, so it does not count as a narrowing.
	const isFiltered = $derived(
		searchQuery.trim() !== '' ||
			selectedType !== '' ||
			selectedStatus !== '' ||
			selectedOutcome !== 'approved'
	);

	// Narrowing the results while looking at page 4 should show the first
	// page of the new set, not whatever lands at that offset.
	$effect(() => {
		void selectedType;
		void selectedOutcome;
		void selectedStatus;
		void searchQuery;
		currentPage = 1;
	});

	const paginated = $derived(filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize));

	const totalPages = $derived(Math.ceil(filtered.length / pageSize));

	// What the list just became, for a reader who cannot see the rows
	// change under a filter chip. Counts what has been loaded rather than
	// what exists: this list pages client-side over a growing window, so
	// "loaded" is the honest number and the Load more button below says the
	// rest. See $lib/listStatus.
	const listStatus = $derived(
		describeList({
			noun: 'certificate',
			total: filtered.length,
			loading: isLoading,
			ready: hasLoaded,
			page: currentPage,
			pageCount: totalPages,
			query: searchQuery
		})
	);

	// A row opens the certificate's own page rather than a dialog over the
	// list. `from` is what the back chip there reads: this list, not the
	// reader's own history, is where they came from.
	function certHref(certId: string): string {
		return `${resolve(`/certs/${certId}`)}?from=history`;
	}

	// True while either source has more to give. One button loads both, so a
	// reader is never told there is no more history while half of it is
	// still unfetched.
	const hasMore = $derived(nextCursor !== null || nextDenialCursor !== null);

	// What to send with every read. Named once so the first page and every
	// "load more" after it cannot drift apart.
	const filter = $derived({
		q: searchQuery.trim(),
		type: selectedType,
		status: selectedStatus
	});

	// Which endpoints the outcome asks for. Approved reads certificates
	// alone and Denied reads decisions alone, so the filter is not just a
	// sieve over what arrived — it decides what is fetched. (A validity
	// filter excludes denials too, but the server already answers that with
	// an empty list, so there is no second rule here.)
	const wantsCertificates = $derived(selectedOutcome !== 'denied');
	const wantsDenials = $derived(selectedOutcome !== 'approved');

	// Which load is allowed to write to the page. Nothing cancels a request
	// in flight and the filters stay live while one runs, so two can
	// overlap and answer in whatever order the network settles on. Without
	// this, a slower earlier read landing last would replace the list with
	// results for a filter the reader had already moved off. The admin
	// certificate list arbitrates its own reads the same way.
	//
	// Deliberately not $state: nothing renders from it, and the effect below
	// reads the filters, so making it reactive would feed the loop it is
	// there to settle.
	let latestLoad = 0;

	/**
	 * load fetches the first page for one set of filters, replacing whatever
	 * is on screen. Takes what to fetch rather than reading it, which is
	 * what keeps the effect's dependency list deliberate rather than a
	 * consequence of where the first await falls.
	 */
	async function load(params: {
		filter: { q: string; type: string; status: string };
		certificates: boolean;
		denials: boolean;
	}) {
		const load = ++latestLoad;
		isLoading = true;
		const controller = new AbortController();
		try {
			const [certs, denials] = await Promise.all([
				params.certificates ? listCertificates(controller.signal, null, 25, params.filter) : null,
				params.denials ? listDeniedRequests(controller.signal, null, 25, params.filter) : null
			]);
			if (load !== latestLoad) {
				return;
			}
			// Both arrays are guarded: an unexpected response shape must
			// render as "nothing of that kind" rather than take down a page
			// whose other half loaded fine.
			allCertificates = Array.isArray(certs?.certificates) ? certs.certificates : [];
			nextCursor = certs?.next_cursor ?? null;
			allDenials = Array.isArray(denials?.denials) ? denials.denials : [];
			nextDenialCursor = denials?.next_cursor ?? null;
			loadError = null;
		} catch (cause) {
			if (load !== latestLoad || controller.signal.aborted || redirectIfUnauthenticated(cause)) {
				return;
			}
			loadError = errorMessage(cause);
		} finally {
			if (load === latestLoad) {
				isLoading = false;
				hasLoaded = true;
			}
		}
	}

	async function loadMore() {
		if (isLoading || !hasMore) {
			return;
		}
		const load = ++latestLoad;
		isLoading = true;
		const controller = new AbortController();
		try {
			// Both pages in flight together: they are independent reads, and
			// awaiting them in turn would double the wait for one button.
			const [certs, denials] = await Promise.all([
				nextCursor ? listCertificates(controller.signal, nextCursor, 25, filter) : null,
				nextDenialCursor
					? listDeniedRequests(controller.signal, nextDenialCursor, 25, filter)
					: null
			]);
			if (load !== latestLoad) {
				return;
			}
			if (certs) {
				allCertificates = [...allCertificates, ...certs.certificates];
				nextCursor = certs.next_cursor ?? null;
			}
			if (denials) {
				allDenials = [...allDenials, ...(denials.denials ?? [])];
				nextDenialCursor = denials.next_cursor ?? null;
			}
		} catch (cause) {
			if (load !== latestLoad || controller.signal.aborted || redirectIfUnauthenticated(cause)) {
				return;
			}
			loadError = errorMessage(cause);
		} finally {
			if (load === latestLoad) {
				isLoading = false;
			}
		}
	}

	// Reads the filters, and only the filters. Everything it writes is
	// settled inside load(), so a page that has just filled itself does not
	// re-run this.
	$effect(() => {
		load({ filter, certificates: wantsCertificates, denials: wantsDenials });
	});
</script>

<svelte:head><title>Certificate history · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="Certificate history" />

	{#if loadError}
		<Alert variant="error" title="Could not load your history">{loadError}</Alert>
	{/if}

	{#if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else}
		<!-- Search, then the filter groups on one line. The admin certificate
		     list opens exactly the same way; see $lib/filters.

		     Always rendered once the page has loaded, never inside the
		     empty-state branch: the page opens on approvals, so somebody
		     whose only history is a refusal would land on "nothing here"
		     with no control on screen to go and find it. -->
		<ListStatus message={listStatus} />

		<div class="flex flex-col gap-3">
			<SearchInput
				label="Search your history"
				placeholder="Key ID, principal, serial, fingerprint, host"
				value={searchQuery}
				onsearch={(term) => (searchQuery = term)}
				testid="search-input"
			/>

			<div class="flex flex-wrap items-center gap-x-5 gap-y-2">
				<FilterGroup
					label="Outcome"
					options={outcomeFilters}
					selected={selectedOutcome}
					disabled={isLoading}
					onselect={(value) => (selectedOutcome = value)}
					testid="outcome-filter"
				/>
				<FilterGroup
					label="Type"
					options={typeFilters}
					selected={selectedType}
					disabled={isLoading}
					onselect={(value) => (selectedType = value)}
					testid="type-filter"
				/>
				<FilterGroup
					label="Status"
					options={statusFilters}
					selected={selectedStatus}
					disabled={isLoading}
					onselect={(value) => (selectedStatus = value)}
					testid="status-filter"
				/>
			</div>
		</div>

		{#if filtered.length === 0}
			<p class="text-sm text-ink-muted">
				{isFiltered
					? 'Nothing in your history matches the selected filter.'
					: 'You have not decided any certificate requests yet.'}
			</p>
		{:else}
			<div class="flex flex-col gap-2.5">
				<!-- Keyed on the kind as well as the id: a certificate and the
				     decision that refused a different request are separate
				     tables with separate id spaces, and nothing stops one from
				     colliding with the other. -->
				{#each paginated as entry (entry.kind + ':' + (entry.kind === 'certificate' ? entry.cert.id : entry.denial.id))}
					{#if entry.kind === 'certificate'}
						<CertRow
							cert={entry.cert}
							{now}
							event={rowEvents[entry.cert.type] ?? 'certificate requested'}
							testid="cert-row"
							href={certHref(entry.cert.id)}
						/>
					{:else}
						<DeniedRow denial={entry.denial} {now} testid="denied-row" />
					{/if}
				{/each}
			</div>

			{#if totalPages > 1 || hasMore}
				<div class="flex items-center justify-between gap-3 border-t border-border-subtle pt-2">
					<Button variant="ghost" disabled={currentPage === 1} onclick={() => currentPage--}>
						Previous
					</Button>
					<span class="text-xs text-ink-muted">
						Page {currentPage} of {totalPages} ({filtered.length} loaded)
					</span>
					<Button
						variant="ghost"
						disabled={currentPage === totalPages}
						onclick={() => currentPage++}
					>
						Next
					</Button>
				</div>

				{#if hasMore}
					<div class="flex justify-center">
						<Button variant="ghost" busy={isLoading} onclick={loadMore}>
							{isLoading ? 'Loading…' : 'Load more results'}
						</Button>
					</div>
				{/if}
			{/if}
		{/if}
	{/if}
</PageShell>
