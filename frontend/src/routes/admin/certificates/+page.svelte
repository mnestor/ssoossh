<script lang="ts">
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { listAdminCertificates } from '#lib/api/endpoints.js';
	import type { CertificateResponse } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import LoadMoreFooter from '#lib/components/LoadMoreFooter.svelte';
	import Button from '#lib/components/Button.svelte';
	import CertRow from '#lib/components/CertRow.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import FilterGroup from '#lib/components/FilterGroup.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import SearchInput from '#lib/components/SearchInput.svelte';
	import { statusFilters, typeFilters } from '#lib/filters.js';
	import { describeList, describeLoaded } from '#lib/listStatus.js';
	import { certificateEvent } from '#lib/rowEvents.js';

	let certificates = $state<CertificateResponse[]>([]);
	// How many certificates match the filters, across every page, and the
	// cursor for the page after the last one shown (null once it is in).
	let total = $state(0);
	let nextCursor = $state<string | null>(null);
	// A failed "Load more" is its own error: the certificates already shown
	// are still right, so it sits where the button was and retries that
	// page, rather than replacing the list with the first page again.
	let moreError = $state<string | null>(null);
	// Read from the address the page opened at, and written back to it, so
	// returning from a certificate (whose back chip carries this search)
	// lands on the same narrowed view instead of the defaults.
	const opened = page.url.searchParams;
	let searchQuery = $state(opened.get('q') ?? '');
	let typeFilter = $state(opened.get('type') ?? '');
	let statusFilter = $state(opened.get('status') ?? '');
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	// Which load is allowed to write to the page. Nothing cancels a request
	// in flight, and the search box stays live while one is running, so two
	// can easily overlap — and they answer in whatever order the server and
	// the network settle on, not the order they were sent. Without this, a
	// slower earlier request landing last replaced the list with results for
	// a term the user had already moved on from.
	//
	// Deliberately not $state: nothing renders from it, and the effect below
	// reads the filters, so making this reactive would feed the loop it is
	// there to arbitrate.
	let latestLoad = 0;

	// Takes what to fetch rather than reading it. The effect below is the one
	// place that touches the filter state, which is what keeps its dependency
	// list deliberate instead of a consequence of where the first await
	// happens to fall. Given after, the previous page's cursor, it appends
	// the page that follows, so "Load more" grows one continuous list.
	async function loadCertificates(
		query: {
			q: string;
			type: string;
			status: string;
		},
		after?: string
	) {
		const load = ++latestLoad;
		isLoading = true;
		// A new load starts clean: an error from an earlier one says nothing
		// about this one, and left in place it sat above a list that loaded.
		moreError = null;
		if (!after) {
			loadError = null;
		}
		try {
			const result = await listAdminCertificates(undefined, {
				after,
				limit: 25,
				q: query.q || undefined,
				type: query.type || undefined,
				status: query.status || undefined
			});
			if (load !== latestLoad) {
				return;
			}
			certificates = after ? [...certificates, ...result.certificates] : result.certificates;
			total = result.total;
			nextCursor = result.next_cursor ?? null;
			hasLoaded = true;
		} catch (cause) {
			if (load !== latestLoad) {
				return;
			}
			if (!redirectIfUnauthenticated(cause)) {
				if (after) {
					moreError = errorMessage(cause);
				} else {
					loadError = errorMessage(cause);
				}
				hasLoaded = true;
			}
		} finally {
			// Only the newest load owns the spinner; a superseded one
			// finishing must not clear it while its replacement is still out.
			if (load === latestLoad) {
				isLoading = false;
			}
		}
	}

	// The only first-page loader. Reading the three inputs here is what subscribes this
	// effect to them, so it covers the first load and every change alike.
	//
	// The handlers below therefore only move state. They used to also call
	// loadCertificates directly, which looked like the explicit version of
	// the same thing but was a second trigger: this effect already tracked
	// the filters, because loadCertificates read them synchronously before
	// its first await. Every search and every filter click issued two
	// identical requests, one from the handler and one from the effect
	// re-running.
	$effect(() => {
		loadCertificates({ q: searchQuery, type: typeFilter, status: statusFilter });
	});

	// Narrowing the results starts the new set from its first page: the
	// effect above reloads on every filter change.
	function handleSearch(query: string) {
		searchQuery = query;
	}

	// Plain setters, not toggles. Each group carries its own "any" chip now,
	// so clearing a filter is a chip you press rather than pressing the
	// selected one a second time — which looked identical to selecting it.
	function handleTypeFilter(type: string) {
		typeFilter = type;
	}

	function handleStatusFilter(status: string) {
		statusFilter = status;
	}

	/** loadMore appends the page after the last certificate shown. */
	async function loadMore() {
		if (nextCursor) {
			await loadCertificates(
				{ q: searchQuery, type: typeFilter, status: statusFilter },
				nextCursor
			);
		}
	}

	// The way out of a failed load. The effect above only re-runs when one of
	// the three inputs changes, so without this a reader whose first request
	// failed had to touch a filter to get a second attempt, and the page said
	// nothing about that being the trick.
	function retry() {
		loadCertificates({ q: searchQuery, type: typeFilter, status: statusFilter });
	}

	// Whether anything on screen is narrowing the list. An empty result means
	// two different things either side of this, and only one of them is worth
	// offering a way back from.
	const filtered = $derived(searchQuery !== '' || typeFilter !== '' || statusFilter !== '');

	// The filters as a query string: what the address carries and what a
	// certificate's back chip returns to. Defaults are left out. How far the
	// list was loaded is not: a cursor names a page, not the rows above it,
	// so a return opens on the first page of the same filters.
	const filterSearch = $derived.by(() => {
		const parts: string[] = [];
		const add = (key: string, value: string) => parts.push(`${key}=${encodeURIComponent(value)}`);
		if (searchQuery.trim()) add('q', searchQuery.trim());
		if (typeFilter) add('type', typeFilter);
		if (statusFilter) add('status', statusFilter);
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
			// Before the router is ready (or in a test) the address stays as
			// it was; the filters themselves still apply.
		}
	});

	// A row opens the certificate's own page, which returns here through its
	// back chip with the filters it was opened from.
	function certHref(certId: string): string {
		const back = filterSearch ? `&back=${encodeURIComponent(filterSearch)}` : '';
		return `${resolve(`certs/${certId}`)}?from=admin${back}`;
	}

	// Clearing the filters remounts the search box: it owns what is typed in
	// it and does not watch `value` afterwards, so resetting the term alone
	// would leave the old text on screen under an unfiltered list.
	let searchKey = $state(0);

	function clearFilters() {
		searchQuery = '';
		typeFilter = '';
		statusFilter = '';
		searchKey += 1;
	}

	// What the list just became, for a reader who cannot see the rows
	// change under a filter. See #lib/listStatus.
	const status = $derived(
		describeList({
			noun: 'certificate',
			total,
			loading: isLoading,
			ready: hasLoaded,
			query: searchQuery
		})
	);
</script>

<svelte:head><title>Certificates · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="All certificates" />

	<!-- Search, then the filter groups on one line. Both lists that show
	     certificate rows open the same way; see #lib/filters. -->
	<div class="flex flex-col gap-3">
		{#key searchKey}
			<SearchInput
				label="Search certificates"
				placeholder="Key ID, principal, serial, fingerprint, owner"
				value={searchQuery}
				onsearch={handleSearch}
				testid="search-input"
			/>
		{/key}

		<div class="flex flex-wrap items-center gap-x-5 gap-y-2">
			<FilterGroup
				label="Type"
				options={typeFilters}
				selected={typeFilter}
				onselect={handleTypeFilter}
				testid="type-filter"
			/>
			<FilterGroup
				label="Status"
				options={statusFilters}
				selected={statusFilter}
				onselect={handleStatusFilter}
				testid="status-filter"
			/>
		</div>
	</div>

	<ListStatus message={status} />

	<!-- The error takes the list's place rather than sitting above it: an
	     empty state under a failed load told the reader nothing had been
	     issued. A retry from an empty list shows the loading shape, not the
	     empty state, until it answers. -->
	{#if loadError}
		<Alert
			variant="error"
			title="Could not load certificates"
			onretry={retry}
			testid="certificates-error"
		>
			{loadError}
		</Alert>
	{:else if !hasLoaded || (isLoading && certificates.length === 0)}
		<LoadingBlock shape="rows" count={5} testid="certificates-loading" />
	{:else if certificates.length === 0 && filtered}
		<EmptyState icon="filter-off" title="No certificates match" testid="certificates-empty">
			{#snippet action()}
				<Button variant="ghost" onclick={clearFilters} testid="certificates-clear-filters">
					Clear filters
				</Button>
			{/snippet}
			Nothing on this deployment matches the search and filters above.
		</EmptyState>
	{:else if certificates.length === 0}
		<EmptyState icon="certificate-off" title="No certificates yet" testid="certificates-empty">
			Nothing has been issued on this deployment. Certificates appear here as soon as the first
			request is approved.
		</EmptyState>
	{:else}
		<div data-testid="cert-list" class="flex flex-col gap-2">
			{#each certificates as cert (cert.id)}
				<CertRow
					cert={{
						...cert,
						decided_by_outcome: cert.decided_by_outcome || undefined,
						decided_by_subject: cert.decided_by_subject || undefined,
						decided_by_username: cert.decided_by_username || undefined,
						decided_by_email: cert.decided_by_email || undefined,
						decided_by_groups: cert.decided_by_groups || [],
						decided_by_other_accounts: cert.decided_by_other_accounts || [],
						decided_by_service_accounts: cert.decided_by_service_accounts || [],
						decided_source_ip: cert.decided_source_ip || undefined,
						decided_user_agent: cert.decided_user_agent || undefined,
						decided_accept_language: cert.decided_accept_language || undefined,
						decided_forwarded_for: cert.decided_forwarded_for || undefined,
						decided_at: cert.decided_at || undefined,
						retrieved_source_ip: cert.retrieved_source_ip || undefined,
						retrieved_at: cert.retrieved_at || undefined,
						enrollment_id: cert.enrollment_id || undefined
					}}
					event={certificateEvent(cert.type, cert.reported_pam_service)}
					approvedBy={cert.decided_by_username || cert.decided_by_email || undefined}
					testid="cert-row"
					href={certHref(cert.id)}
				/>
			{/each}
		</div>
	{/if}

	{#if !loadError && certificates.length > 0}
		<!-- The audit feed's footer: what is on screen, what is left, then
		     Load more. -->
		<LoadMoreFooter
			summary={describeLoaded(certificates.length, total - certificates.length, 'older')}
			hasMore={!!nextCursor}
			busy={isLoading}
			error={moreError}
			errorTitle="Could not load more certificates"
			onmore={loadMore}
			testid="certificates"
		/>
	{/if}
</PageShell>
