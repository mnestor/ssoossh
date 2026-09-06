<script lang="ts">
	import { pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { listAdminEnrollments } from '$lib/api/endpoints';
	import type { AdminEnrollment } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import AdminServiceCodeDetailModal from '$lib/components/AdminServiceCodeDetailModal.svelte';
	import Alert from '$lib/components/Alert.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import SearchInput from '$lib/components/SearchInput.svelte';
	import ServiceCodeRow from '$lib/components/ServiceCodeRow.svelte';

	let enrollments = $state<AdminEnrollment[]>([]);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);
	let meta = $state<any>(null);
	let searchQuery = $state('');
	let currentOffset = $state(0);
	const pageSize = 25;

	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// Load enrollments when search or offset changes
	$effect(() => {
		const controller = new AbortController();
		hasLoaded = false;
		loadError = null;

		listAdminEnrollments(controller.signal, pageSize, currentOffset, searchQuery || undefined)
			.then((result) => {
				enrollments = result.enrollments;
				meta = result.meta;
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

	// Both the pushed state and the search parameter, like the holder-facing
	// page: the state is what a click records, and the parameter is what a
	// pasted link arrives with.
	const modalEnrollmentId = $derived(
		'modalEnrollmentId' in page.state
			? page.state.modalEnrollmentId
			: page.url.searchParams.get('modal')
	);

	const modalEnrollment = $derived(enrollments.find((e) => e.id === modalEnrollmentId));

	function navigate(id: string | null) {
		const url = new URL(page.url);
		if (id) {
			url.searchParams.set('modal', id);
		} else {
			url.searchParams.delete('modal');
		}
		// Shallow-route within this same page (a query parameter), not a
		// navigation to a different route id — resolve() is for the latter.
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, { ...page.state, modalEnrollmentId: id });
	}

	// Closing records an explicit null rather than an absent key: an absent
	// one falls back to the search parameter, which on a page reached by a
	// pasted link would reopen what was just closed.
	const openDetail = (id: string) => navigate(id);
	const closeDetail = () => navigate(null);

	function onSearch(query: string) {
		searchQuery = query;
		currentOffset = 0; // Reset to first page on new search
	}

	function onPageChange(offset: number) {
		currentOffset = offset;
	}
</script>

<svelte:head>
	<title>Service codes · Admin · ssoossh</title>
</svelte:head>

<PageShell width="wide">
	<PageHeading eyebrow="Admin" title="Service enrollment codes" />

	<p class="text-sm text-ink-muted">
		All approved service enrollment codes across users. Codes themselves are never shown. Open a row
		to see what it hands out, how often it's been redeemed, and reassign it if needed.
	</p>

	<!-- The placeholder names what the search actually matches. The
	     enrollment id is first because it is the identifier every other
	     record of a code carries -- the notification email, the
	     enrollment.* audit events, the server log lines -- and an operator
	     arriving with one had no way to know it would work. -->
	<SearchInput
		label="Search enrollments"
		placeholder="enrollment ID, account, key ID, request ID, or approver..."
		onsearch={onSearch}
		testid="search-enrollments"
	/>

	{#if loadError}
		<Alert variant="error" title="Could not load service codes">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if enrollments.length === 0}
		<p data-testid="enrollments-empty" class="text-sm text-ink-muted">
			No service enrollment codes found
			{#if searchQuery}
				matching your search.
			{:else}
				.
			{/if}
		</p>
	{:else}
		<div class="flex flex-col gap-2.5">
			{#each enrollments as enrollment (enrollment.id)}
				<ServiceCodeRow
					{enrollment}
					{now}
					onclick={() => openDetail(enrollment.id)}
					testid="enrollment-row"
				/>
			{/each}
		</div>

		{#if meta}
			<Pager {meta} onpage={onPageChange} testid="enrollments-pager" />
		{/if}
	{/if}

	<!-- The row click pushed the state and nothing rendered the panel, so
	     opening a code did nothing at all. -->
	{#if modalEnrollment}
		<AdminServiceCodeDetailModal enrollment={modalEnrollment} {now} onclosed={closeDetail} />
	{/if}
</PageShell>
