<script lang="ts">
	import { resolve } from '$app/paths';
	import { listAdminEnrollments } from '$lib/api/endpoints';
	import type { AdminEnrollment } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import EmptyState from '$lib/components/EmptyState.svelte';
	import LoadingBlock from '$lib/components/LoadingBlock.svelte';
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
	<PageHeading title="All service codes" />

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
		<LoadingBlock shape="rows" count={4} testid="enrollments-loading" />
	{:else if enrollments.length === 0 && searchQuery}
		<EmptyState icon="filter-off" title="No service codes match" testid="enrollments-empty">
			No service enrollment code on this deployment matches the search above.
		</EmptyState>
	{:else if enrollments.length === 0}
		<EmptyState icon="key" title="No service codes yet" testid="enrollments-empty">
			A code is created when a request from <code class="font-mono">ssoossh service enroll</code> is approved.
			None has been yet.
		</EmptyState>
	{:else}
		<div class="flex flex-col gap-2.5">
			{#each enrollments as enrollment (enrollment.id)}
				<ServiceCodeRow
					{enrollment}
					{now}
					href={resolve(`/admin/service-codes/${enrollment.id}`)}
					testid="enrollment-row"
				/>
			{/each}
		</div>

		{#if meta}
			<Pager {meta} onpage={onPageChange} testid="enrollments-pager" />
		{/if}
	{/if}
</PageShell>
