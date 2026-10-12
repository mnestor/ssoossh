<script lang="ts">
	import { resolve } from '$app/paths';
	import { listAdminEnrollments } from '#lib/api/endpoints.js';
	import type { AdminEnrollment } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SearchInput from '#lib/components/SearchInput.svelte';
	import ServiceCodeRow from '#lib/components/ServiceCodeRow.svelte';
	import { describeList } from '#lib/listStatus.js';

	let enrollments = $state<AdminEnrollment[]>([]);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);
	// Bumped by Try again; the load below reads it, so a bump re-runs it.
	let attempt = $state(0);
	// How many codes match the search, across every page; null until the
	// first answer.
	let total = $state<number | null>(null);
	// The cursor for the page after the last code shown, or null once it is
	// in.
	let nextCursor = $state<string | null>(null);
	// A failed "Load more" is its own error: the codes already shown are
	// still right, so it sits where the button was and retries that page.
	let moreError = $state<string | null>(null);
	let loadingMore = $state(false);
	// Which first-page load a "Load more" belongs to. A search started while
	// one is out makes its answer stale, and it is dropped rather than
	// appended to the new list. Not $state: nothing renders from it.
	let generation = 0;
	let searchQuery = $state('');
	// Clearing remounts the search box: it owns what is typed in it, so
	// resetting the term alone would leave the old text over the full list.
	let searchKey = $state(0);
	function clearSearch() {
		searchQuery = '';
		searchKey += 1;
	}
	const pageSize = 25;

	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// Load the first page whenever the search changes, or on Try again.
	$effect(() => {
		void attempt;
		const controller = new AbortController();
		generation += 1;
		hasLoaded = false;
		loadError = null;
		moreError = null;
		loadingMore = false;

		listAdminEnrollments(controller.signal, pageSize, undefined, searchQuery || undefined)
			.then((result) => {
				enrollments = result.enrollments;
				total = result.total;
				nextCursor = result.next_cursor ?? null;
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

	// The way out of a failed load: the first page of the same search again.
	function retry() {
		attempt += 1;
	}

	/**
	 * loadMore appends the page after the last code shown. The server pages
	 * by cursor, so a code approved since the first page lands above it
	 * rather than repeating a row here.
	 */
	async function loadMore() {
		if (loadingMore || nextCursor === null) {
			return;
		}
		const mine = generation;
		loadingMore = true;
		moreError = null;
		try {
			const result = await listAdminEnrollments(
				undefined,
				pageSize,
				nextCursor,
				searchQuery || undefined
			);
			if (mine !== generation) return;
			enrollments = [...enrollments, ...result.enrollments];
			total = result.total;
			nextCursor = result.next_cursor ?? null;
		} catch (cause) {
			if (mine !== generation || redirectIfUnauthenticated(cause)) return;
			moreError = errorMessage(cause);
		} finally {
			if (mine === generation) loadingMore = false;
		}
	}

	function onSearch(query: string) {
		searchQuery = query;
	}

	// What the list just became, for a reader who cannot see the rows
	// change under a search (WCAG 4.1.3). See #lib/listStatus.
	const status = $derived(
		describeList({
			noun: 'service code',
			total: total ?? 0,
			loading: !hasLoaded || loadingMore,
			ready: total !== null,
			query: searchQuery
		})
	);
</script>

<svelte:head>
	<title>Service codes · ssoossh</title>
</svelte:head>

<PageShell width="wide">
	<PageHeading title="All service codes" />

	<p class="text-sm text-ink-muted">
		Every approved service enrollment code, by account. The codes themselves are never shown. Open a
		row to see what it hands out, where it has been redeemed, and to expire it.
	</p>

	<!-- The placeholder names what the search actually matches. The
	     enrollment id is first because it is the identifier every other
	     record of a code carries -- the notification email, the
	     enrollment.* audit events, the server log lines -- and an operator
	     arriving with one had no way to know it would work. -->
	{#key searchKey}
		<SearchInput
			label="Search enrollments"
			placeholder="enrollment ID, account, key ID, request ID, or approver..."
			value={searchQuery}
			onsearch={onSearch}
			testid="search-enrollments"
		/>
	{/key}

	<ListStatus message={status} />

	{#if loadError}
		<Alert
			variant="error"
			title="Could not load service codes"
			onretry={retry}
			testid="service-codes-error"
		>
			{loadError}
		</Alert>
	{:else if !hasLoaded}
		<LoadingBlock shape="rows" count={4} testid="enrollments-loading" />
	{:else if enrollments.length === 0 && searchQuery}
		<EmptyState icon="filter-off" title="No service codes match" testid="enrollments-empty">
			{#snippet action()}
				<Button variant="ghost" onclick={clearSearch} testid="enrollments-clear-search">
					Clear search
				</Button>
			{/snippet}
			No service enrollment code on this deployment matches the search above.
		</EmptyState>
	{:else if enrollments.length === 0}
		<EmptyState icon="key" title="No service codes yet" testid="enrollments-empty">
			A code is created when a request from <code class="font-mono">ssoossh service enroll</code> is approved.
			None has been yet.
		</EmptyState>
	{:else}
		<div class="flex flex-col gap-2">
			{#each enrollments as enrollment (enrollment.id)}
				<ServiceCodeRow
					{enrollment}
					{now}
					href={resolve(`admin/service-codes/${enrollment.id}`)}
					testid="enrollment-row"
				/>
			{/each}
		</div>

		{#if moreError}
			<Alert
				variant="error"
				title="Could not load more service codes"
				onretry={loadMore}
				testid="enrollments-more-error"
			>
				{moreError}
			</Alert>
		{:else if nextCursor}
			<div class="flex flex-col items-center gap-1">
				<Button
					variant="ghost"
					busy={loadingMore}
					onclick={loadMore}
					testid="enrollments-load-more"
				>
					{loadingMore ? 'Loading…' : 'Load more'}
				</Button>
				<span class="text-xs text-ink-muted tabular-nums" data-testid="enrollments-count"
					>{enrollments.length} of {total} shown</span
				>
			</div>
		{/if}
	{/if}
</PageShell>
