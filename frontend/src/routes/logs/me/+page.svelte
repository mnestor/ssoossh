<script lang="ts">
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { listCertificates, listDeniedRequests } from '#lib/api/endpoints.js';
	import type { CertificateRecord, DeniedRequest } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import Button from '#lib/components/Button.svelte';
	import CertRow from '#lib/components/CertRow.svelte';
	import DeniedRow from '#lib/components/DeniedRow.svelte';
	import FilterGroup from '#lib/components/FilterGroup.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import SearchInput from '#lib/components/SearchInput.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import { outcomeFilters, statusFilters, typeFilters } from '#lib/filters.js';
	import { describeList } from '#lib/listStatus.js';
	import { certificateEvent } from '#lib/rowEvents.js';
	import { watchNarrow } from '#lib/viewport.js';

	// Cursor-paginated decision history: what was issued, and what was
	// refused. The server takes the search and the type and status filters,
	// and the outcome decides which of the two endpoints is read at all.
	//
	// Two sources because the server has two. /api/certs reads the
	// certificates table, and a denial never writes a row there, so the half
	// of somebody's decisions that says "no" is served by
	// /api/decisions/denied and interleaved here by time. That half is the
	// one an incident review asks about first.
	let allCertificates = $state<CertificateRecord[]>([]);
	let allDenials = $state<DeniedRequest[]>([]);
	let nextCursor = $state<string | null>(null);
	let nextDenialCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	// A failed "Load more" is its own error: the rows already shown are
	// still right, so it sits by the button rather than replacing them.
	let moreError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	// Filter state, read from the address the page opened at and written
	// back to it, so returning from a certificate (whose back chip carries
	// this search) lands on the same narrowed view instead of the defaults.
	// The vocabulary is shared with /admin/certificates; see #lib/filters.
	//
	// Approved by default, not both. This page has always been the list of
	// certificates somebody holds, and that is what most visits are for. A
	// refusal is one click away instead.
	const opened = page.url.searchParams;
	let selectedOutcome = $state(opened.get('outcome') ?? 'approved');
	let selectedType = $state(opened.get('type') ?? '');
	let selectedStatus = $state(opened.get('status') ?? '');
	let searchQuery = $state(opened.get('q') ?? '');

	// Below `sm` a certificate row has no room for its outcome mark. The
	// list still mixes the two kinds when asked to: a refusal is a DeniedRow,
	// which carries its own mark and says "denied" in its detail line, so
	// the outcome filter stays on offer at every width. It used to be pinned
	// to approvals on a phone, which left a refusal unreachable there.
	let isNarrow = $state(false);
	$effect(() => watchNarrow((narrow) => (isNarrow = narrow)));

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

	/** oldestAt is the earliest time in a loaded page, or -Infinity for none. */
	function oldestAt(times: number[]): number {
		return times.length ? Math.min(...times) : -Infinity;
	}

	// How far back the merged list is known to be complete. Each source with
	// more to fetch is complete only back to its oldest loaded row; anything
	// older may still have rows from that source to come. Showing past it
	// gave a timeline with holes, and Load more then inserted rows above
	// ones the reader had already passed. A single-source view is unchanged,
	// because the other source has no cursor.
	const horizon = $derived(
		Math.max(
			nextCursor
				? oldestAt(allCertificates.map((c) => new Date(c.issued_at).getTime()))
				: -Infinity,
			nextDenialCursor
				? oldestAt(allDenials.map((d) => new Date(d.decided_at).getTime()))
				: -Infinity
		)
	);
	const shown = $derived(sorted.filter((entry) => entry.at >= horizon));

	// Whether anything has been narrowed, which is what separates "you have
	// no history" from "nothing matches". Approved is the state the page
	// opens in, so it does not count as a narrowing.
	const isFiltered = $derived(
		searchQuery.trim() !== '' ||
			selectedType !== '' ||
			selectedStatus !== '' ||
			selectedOutcome !== 'approved'
	);

	// Clearing remounts the search box: it owns what is typed in it and does
	// not watch `value` afterwards, so resetting the term alone would leave
	// the old text on screen over an unfiltered list.
	let searchKey = $state(0);

	function clearFilters() {
		searchQuery = '';
		selectedType = '';
		selectedStatus = '';
		selectedOutcome = 'approved';
		searchKey += 1;
	}

	// The filters as a query string: what the address bar carries and what
	// a certificate's back chip returns to. Defaults are left out, so an
	// unfiltered history has a clean address.
	const filterSearch = $derived.by(() => {
		const parts: string[] = [];
		const add = (key: string, value: string) => parts.push(`${key}=${encodeURIComponent(value)}`);
		if (searchQuery.trim()) add('q', searchQuery.trim());
		if (selectedType) add('type', selectedType);
		if (selectedStatus) add('status', selectedStatus);
		if (selectedOutcome !== 'approved') add('outcome', selectedOutcome);
		return parts.length ? `?${parts.join('&')}` : '';
	});

	$effect(() => {
		const search = filterSearch;
		if (search === page.url.search) {
			return;
		}
		try {
			replaceState(`${page.url.pathname}${search}`, page.state);
		} catch {
			// Before the router is ready (or in a test) the address simply
			// stays as it was; the filters themselves still apply.
		}
	});

	// What the list holds, for the announcement and the empty states: a
	// certificate, a refusal, or either.
	const noun = $derived(
		selectedOutcome === 'denied'
			? 'denied request'
			: selectedOutcome === 'approved'
				? 'certificate'
				: 'entry'
	);

	// What the list just became, for a reader who cannot see the rows
	// change under a filter chip. Counts what has been loaded rather than
	// what exists: "loaded" is the honest number, and the Load more button
	// below says the rest. See #lib/listStatus.
	const listStatus = $derived(
		describeList({
			noun,
			total: shown.length,
			loading: isLoading,
			ready: hasLoaded,
			query: searchQuery
		})
	);

	// A row opens the certificate's own page rather than a dialog over the
	// list. `from` is what the back chip there reads, and `back` the
	// filters it returns to.
	function certHref(certId: string): string {
		const back = filterSearch ? `&back=${encodeURIComponent(filterSearch)}` : '';
		return `${resolve(`certs/${certId}`)}?from=history${back}`;
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
	// sieve over what arrived; it decides what is fetched.
	const wantsCertificates = $derived(selectedOutcome !== 'denied');
	const wantsDenials = $derived(selectedOutcome !== 'approved');

	// Which load is allowed to write to the page. Nothing cancels a request
	// in flight and the filters stay live while one runs, so two can
	// overlap and answer in whatever order the network settles on; this
	// keeps a slower earlier read from landing last.
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
		// A new read starts clean: an earlier failure says nothing about it.
		loadError = null;
		moreError = null;
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
		moreError = null;
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
			moreError = errorMessage(cause);
		} finally {
			if (load === latestLoad) {
				isLoading = false;
			}
		}
	}

	// The way out of a failed read: the same filters, asked again.
	function retry() {
		load({ filter, certificates: wantsCertificates, denials: wantsDenials });
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

	{#if !hasLoaded}
		<LoadingBlock shape="rows" count={4} testid="history-loading" />
	{:else}
		<!-- Search, then the filter groups on one line. The admin certificate
		     list opens exactly the same way; see #lib/filters.

		     Always rendered once the page has loaded, never inside the
		     empty-state branch: the page opens on approvals, so somebody
		     whose only history is a refusal would land on "nothing here"
		     with no control on screen to go and find it. -->
		<ListStatus message={listStatus} />

		<div class="flex flex-col gap-3">
			{#key searchKey}
				<SearchInput
					label="Search your history"
					placeholder="Key ID, principal, serial, fingerprint, host"
					value={searchQuery}
					onsearch={(term) => (searchQuery = term)}
					testid="search-input"
				/>
			{/key}

			<!-- Type, then Status, then Outcome. Ordered by how often a
			     reader reaches for one: the type of certificate is what most
			     visits narrow by, and the outcome is the last thing they
			     would change. It also puts the two groups the admin list
			     shares first and in the same order.

			     Not disabled while a load runs: the latestLoad guard already
			     discards a stale answer, and disabling the chip just pressed
			     took keyboard focus away from it. -->
			<div class="flex flex-wrap items-center gap-x-5 gap-y-2">
				<FilterGroup
					label="Type"
					options={typeFilters}
					selected={selectedType}
					onselect={(value) => (selectedType = value)}
					testid="type-filter"
				/>
				<FilterGroup
					label="Status"
					options={statusFilters}
					selected={selectedStatus}
					onselect={(value) => (selectedStatus = value)}
					testid="status-filter"
				/>
				<FilterGroup
					label="Outcome"
					options={outcomeFilters}
					selected={selectedOutcome}
					onselect={(value) => (selectedOutcome = value)}
					testid="outcome-filter"
				/>
			</div>
		</div>

		<!-- The error takes the list's place: an empty-history message under a
		     failed read told the reader they had decided nothing. A retry from
		     an empty list shows the loading shape until it answers. -->
		{#if loadError}
			<Alert
				variant="error"
				title="Could not load your history"
				onretry={retry}
				testid="history-error"
			>
				{loadError}
			</Alert>
		{:else if isLoading && shown.length === 0}
			<LoadingBlock shape="rows" count={4} testid="history-loading" />
		{:else if shown.length === 0 && isFiltered}
			<EmptyState icon="filter-off" title="Nothing matches" testid="history-empty">
				{#snippet action()}
					<Button variant="ghost" onclick={clearFilters} testid="history-clear-filters">
						Clear filters
					</Button>
				{/snippet}
				Nothing in your history matches this search or these filters.
			</EmptyState>
		{:else if shown.length === 0}
			<!-- The page opens on approvals, so "no decisions" would be false
			     for someone whose only decision was a refusal. -->
			<EmptyState icon="certificate-off" title="No certificates yet" testid="history-empty">
				Run <code class="font-mono">ssoossh login</code> to request one. Refused requests are under Outcome:
				Denied.
			</EmptyState>
		{:else}
			<div class="flex flex-col gap-2">
				<!-- Keyed on the kind as well as the id: a certificate and the
				     decision that refused a different request are separate
				     tables with separate id spaces, and nothing stops one from
				     colliding with the other. -->
				{#each shown as entry (entry.kind + ':' + (entry.kind === 'certificate' ? entry.cert.id : entry.denial.id))}
					{#if entry.kind === 'certificate'}
						<CertRow
							cert={entry.cert}
							{now}
							event={certificateEvent(entry.cert.type)}
							showOutcome={!isNarrow}
							testid="cert-row"
							href={certHref(entry.cert.id)}
						/>
					{:else}
						<DeniedRow denial={entry.denial} {now} testid="denied-row" />
					{/if}
				{/each}
			</div>

			<!-- One pager. Loaded rows all show, and Load more fetches older
			     ones: a page-of-ten pager over what had loaded used to sit on
			     top of this, reading "Page 3 of 3" while more existed. -->
			{#if moreError}
				<Alert
					variant="error"
					title="Could not load more of your history"
					onretry={loadMore}
					testid="history-more-error"
				>
					{moreError}
				</Alert>
			{:else if hasMore}
				<div class="flex flex-col items-center gap-1">
					<Button variant="ghost" busy={isLoading} onclick={loadMore} testid="history-load-more">
						{isLoading ? 'Loading…' : 'Load more'}
					</Button>
					<span class="text-xs text-ink-muted tabular-nums">{shown.length} shown</span>
				</div>
			{/if}
		{/if}
	{/if}
</PageShell>
