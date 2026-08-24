<script lang="ts">
	import { pushState } from '$app/navigation';
	import { page } from '$app/state';
	import { listAdminEnrollments } from '$lib/api/endpoints';
	import type { AdminEnrollment } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
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

	function openDetail(id: string) {
		const url = new URL(page.url);
		url.searchParams.set('modal', id);
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		pushState(url, { modalEnrollmentId: id });
	}

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

<div class="flex w-full max-w-[680px] flex-col gap-5">
	<PageHeading eyebrow="Admin" title="Service enrollment codes" />

	<p class="text-sm text-ink-muted">
		All approved service enrollment codes across users. Codes themselves are never shown. Open a row
		to see what it hands out, how often it's been redeemed, and reassign it if needed.
	</p>

	<div class="flex gap-4">
		<SearchInput label="Search enrollments" onsearch={onSearch} testid="search-enrollments" />
	</div>

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
</div>
