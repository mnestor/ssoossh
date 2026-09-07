<script lang="ts">
	import { resolve } from '$app/paths';
	import { listAdminCertificates } from '$lib/api/endpoints';
	import type { CertificateResponse } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import CertRow from '$lib/components/CertRow.svelte';
	import FilterChip from '$lib/components/FilterChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import SearchInput from '$lib/components/SearchInput.svelte';

	let certificates = $state<CertificateResponse[]>([]);
	let pageInfo = $state({ total: 0, limit: 25, offset: 0, page: 1, page_count: 1 });
	let searchQuery = $state('');
	let typeFilter = $state('');
	let statusFilter = $state('');
	let offset = $state(0);
	let loadError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);

	// The same chips the certificate history uses, with the same icons: two
	// screens listing the same rows should not filter them through two
	// different-looking controls. "All" leads the types because clearing the
	// filter is a choice like any other; the two status chips have no "All"
	// of their own because pressing the selected one clears it.
	const typeFilters = [
		{ value: '', label: 'All', icon: 'layout-grid' },
		{ value: 'user', label: 'User', icon: 'user' },
		{ value: 'service', label: 'Service', icon: 'cog' },
		{ value: 'pam', label: 'PAM', icon: 'terminal' },
		{ value: 'console', label: 'Console', icon: 'monitor' }
	];

	// The same pair, and the same glyphs, as the validity indicator on the
	// rows below — a reader filtering to "expired" should see the icon they
	// filtered on.
	const statusFilters = [
		{ value: 'live', label: 'Live', icon: 'shield-check' },
		{ value: 'expired', label: 'Expired', icon: 'alert-triangle' }
	];

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

	function handleTypeFilter(type: string) {
		typeFilter = typeFilter === type ? '' : type;
		offset = 0;
	}

	function handleStatusFilter(status: string) {
		statusFilter = statusFilter === status ? '' : status;
		offset = 0;
	}

	function handlePage(next: number) {
		offset = next;
	}
</script>

<svelte:head><title>Certificates · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading eyebrow="Admin" title="Certificate history" />

	{#if loadError}
		<Alert variant="error" title="Could not load certificates">{loadError}</Alert>
	{/if}

	<div class="flex flex-col gap-3">
		<SearchInput
			label="Search certificates"
			placeholder="Key ID, principal, fingerprint, username, email…"
			value={searchQuery}
			onsearch={handleSearch}
			testid="search-input"
		/>

		<div class="flex flex-col gap-2">
			<div data-testid="type-filter" class="flex flex-wrap items-center gap-2">
				<span class="text-xs font-semibold text-ink-muted">Type:</span>
				{#each typeFilters as filter (filter.value)}
					<FilterChip
						label={filter.label}
						icon={filter.icon}
						selected={typeFilter === filter.value}
						disabled={isLoading}
						onclick={() => handleTypeFilter(filter.value)}
					/>
				{/each}
			</div>

			<div class="flex flex-wrap items-center gap-2">
				<span class="text-xs font-semibold text-ink-muted">Status:</span>
				{#each statusFilters as filter (filter.value)}
					<FilterChip
						label={filter.label}
						icon={filter.icon}
						selected={statusFilter === filter.value}
						disabled={isLoading}
						onclick={() => handleStatusFilter(filter.value)}
					/>
				{/each}
			</div>
		</div>
	</div>

	{#if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if certificates.length === 0}
		<p class="text-sm text-ink-muted">No certificates found matching your search.</p>
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
