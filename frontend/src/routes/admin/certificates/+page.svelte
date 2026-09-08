<script lang="ts">
	import { resolve } from '$app/paths';
	import { listAdminCertificates } from '$lib/api/endpoints';
	import type { CertificateResponse } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import FilterGroup from '$lib/components/FilterGroup.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import ListStatus from '$lib/components/ListStatus.svelte';
	import LoadingBlock from '$lib/components/LoadingBlock.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import SearchInput from '$lib/components/SearchInput.svelte';
	import { statusFilters, typeFilters } from '$lib/filters';
	import { describeList } from '$lib/listStatus';

	let certificates = $state<CertificateResponse[]>([]);
	let pageInfo = $state({ total: 0, limit: 25, offset: 0, page: 1, page_count: 1 });
	let searchQuery = $state('');
	let typeFilter = $state('');
	let statusFilter = $state('');
	let offset = $state(0);
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
	// happens to fall.
	async function loadCertificates(query: {
		offset: number;
		q: string;
		type: string;
		status: string;
	}) {
		const load = ++latestLoad;
		isLoading = true;
		try {
			const result = await listAdminCertificates(undefined, {
				offset: query.offset,
				limit: 25,
				q: query.q || undefined,
				type: query.type || undefined,
				status: query.status || undefined
			});
			if (load !== latestLoad) {
				return;
			}
			certificates = result.certificates;
			pageInfo = result.page_meta;
			hasLoaded = true;
		} catch (cause) {
			if (load !== latestLoad) {
				return;
			}
			if (!redirectIfUnauthenticated(cause)) {
				loadError = errorMessage(cause);
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

	// The only loader. Reading the four inputs here is what subscribes this
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
		loadCertificates({ offset, q: searchQuery, type: typeFilter, status: statusFilter });
	});

	// Narrowing the results while looking at page 4 should show the first
	// page of the new set, not whatever lands at that offset.
	function handleSearch(query: string) {
		searchQuery = query;
		offset = 0;
	}

	// Plain setters, not toggles. Each group carries its own "any" chip now,
	// so clearing a filter is a chip you press rather than pressing the
	// selected one a second time — which looked identical to selecting it.
	function handleTypeFilter(type: string) {
		typeFilter = type;
		offset = 0;
	}

	function handleStatusFilter(status: string) {
		statusFilter = status;
		offset = 0;
	}

	function handlePage(next: number) {
		offset = next;
	}

	// The way out of a failed load. The effect below only re-runs when one of
	// the four inputs changes, so without this a reader whose first request
	// failed had to touch a filter to get a second attempt, and the page said
	// nothing about that being the trick.
	function retry() {
		loadError = null;
		loadCertificates({ offset, q: searchQuery, type: typeFilter, status: statusFilter });
	}

	// Whether anything on screen is narrowing the list. An empty result means
	// two different things either side of this, and only one of them is worth
	// offering a way back from.
	const filtered = $derived(searchQuery !== '' || typeFilter !== '' || statusFilter !== '');

	// Clearing the filters remounts the search box: it owns what is typed in
	// it and does not watch `value` afterwards, so resetting the term alone
	// would leave the old text on screen under an unfiltered list.
	let searchKey = $state(0);

	function clearFilters() {
		searchQuery = '';
		typeFilter = '';
		statusFilter = '';
		offset = 0;
		searchKey += 1;
	}

	// What the list just became, for a reader who cannot see the rows
	// change under a filter. See $lib/listStatus.
	const status = $derived(
		describeList({
			noun: 'certificate',
			total: pageInfo.total,
			loading: isLoading,
			ready: hasLoaded,
			page: pageInfo.page,
			pageCount: pageInfo.page_count,
			query: searchQuery
		})
	);
</script>

<svelte:head><title>Certificates · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="All certificates" />

	{#if loadError}
		<Alert variant="error" title="Could not load certificates">
			{loadError}
			<div class="mt-3">
				<Button variant="ghost" onclick={retry} testid="certificates-retry">Try again</Button>
			</div>
		</Alert>
	{/if}

	<!-- Search, then the filter groups on one line. Both lists that show
	     certificate rows open the same way; see $lib/filters. -->
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
				disabled={isLoading}
				onselect={handleTypeFilter}
				testid="type-filter"
			/>
			<FilterGroup
				label="Status"
				options={statusFilters}
				selected={statusFilter}
				disabled={isLoading}
				onselect={handleStatusFilter}
				testid="status-filter"
			/>
		</div>
	</div>

	<ListStatus message={status} />

	{#if !hasLoaded}
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
		<div data-testid="cert-list" class="flex flex-col gap-2.5">
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
					event="certificate issued"
					testid="cert-row"
					href={`${resolve(`/certs/${cert.id}`)}?from=admin`}
				/>
			{/each}
		</div>
	{/if}

	<Pager meta={pageInfo} onpage={handlePage} busy={isLoading} testid="pager" />
</PageShell>
