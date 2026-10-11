<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import Alert from '#lib/components/Alert.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SearchInput from '#lib/components/SearchInput.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import Pager from '#lib/components/Pager.svelte';
	import { getAdminUsers } from '#lib/api/endpoints.js';
	import type { AdminUsersListResponse } from '#lib/api/types.js';
	import { describeList } from '#lib/listStatus.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { formatDateTime } from '#lib/format.js';
	import Button from '#lib/components/Button.svelte';

	/** The account-state filter, matching the server's `status` parameter. */
	type StatusFilter = 'all' | 'active' | 'disabled';

	const statusFilters: { value: StatusFilter; label: string }[] = [
		{ value: 'all', label: 'All' },
		{ value: 'active', label: 'Active' },
		{ value: 'disabled', label: 'Disabled' }
	];

	let users: AdminUsersListResponse | null = $state(null);
	let error: string | null = $state(null);
	// True from the start: onMount starts the load after the first render,
	// and a first frame with nothing loading and nothing loaded used to show
	// "No users found".
	let busy = $state(true);
	let searchQuery = $state('');
	let status: StatusFilter = $state('all');
	let offset = $state(0);

	// Which load is allowed to write to the page. Nothing cancels a request
	// in flight and the search box stays live while one runs, so two can
	// overlap and answer in whatever order the network settles on; without
	// this, fast typing could land an older answer last. The certificate
	// list arbitrates its reads the same way. Not $state: nothing renders
	// from it.
	let latestLoad = 0;

	async function loadUsers() {
		const load = ++latestLoad;
		busy = true;
		error = null;
		try {
			// 'all' is sent as no parameter rather than as a value: the
			// server's filter is "active", "disabled", or absent, and an
			// unrecognized value is a 400 rather than a silent widening.
			const result = await getAdminUsers({
				q: searchQuery,
				limit: 25,
				offset,
				status: status === 'all' ? undefined : status
			});
			if (load === latestLoad) {
				users = result;
			}
		} catch (cause) {
			// An expired session is a sign-in, not a failed list.
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			if (load === latestLoad) {
				error = errorMessage(cause);
			}
		} finally {
			// Only the newest load owns the busy state.
			if (load === latestLoad) {
				busy = false;
			}
		}
	}

	function handleSearch(q: string) {
		searchQuery = q;
		offset = 0;
		loadUsers();
	}

	// Back to the first page, like a new search: the filtered set is a
	// different list, so page 3 of the old one means nothing in it.
	function selectStatus(next: StatusFilter) {
		if (next === status) {
			return;
		}
		status = next;
		offset = 0;
		loadUsers();
	}

	function handlePage(newOffset: number) {
		offset = newOffset;
		loadUsers();
	}

	// What the list just became, for a reader who cannot see the rows
	// change under a search or a state filter. See #lib/listStatus.
	// `$derived.by` rather than `$derived`: at this point in the module the
	// only assignment TypeScript has seen to `users` is the `null` it was
	// declared with, so it narrows the whole union away and `users?.meta`
	// types as `never`. Reading it inside a function body resets that — the
	// same reason the markup below can say `users.meta` after an `{#if}`.
	// Clearing remounts the search box: it owns what is typed in it, so
	// resetting the term alone would leave the old text over the full list.
	let searchKey = $state(0);
	function clearFilters() {
		searchQuery = '';
		status = 'all';
		offset = 0;
		searchKey += 1;
		loadUsers();
	}

	// Read inside functions: at this point in the module TypeScript has seen
	// only the 'all' that `status` was declared with, and narrows it away
	// (the same reason listStatus below is a $derived.by).
	const filtered = $derived.by(() => searchQuery.trim() !== '' || status !== 'all');

	// The filtered empty state, worded for what is narrowing the list.
	const emptyCopy = $derived.by((): { title: string; body: string } => {
		const state: StatusFilter = status;
		if (searchQuery.trim() === '' && state === 'disabled') {
			return { title: 'No disabled users', body: 'Everyone who has signed in is active.' };
		}
		if (searchQuery.trim() === '' && state === 'active') {
			return { title: 'No active users', body: 'Everyone who has signed in is disabled.' };
		}
		return { title: 'Nothing matches', body: 'No user matches this search and account state.' };
	});

	const listStatus = $derived.by(() => {
		const meta = users?.meta;
		return describeList({
			noun: 'user',
			total: meta?.total ?? 0,
			loading: busy,
			ready: meta !== undefined,
			page: meta?.page,
			pageCount: meta?.page_count,
			query: searchQuery
		});
	});

	onMount(loadUsers);
</script>

<svelte:head><title>Users · ssoossh</title></svelte:head>

<PageShell width="full">
	<PageHeading title="Users">
		{#snippet sub()}Everyone who has signed in. Open a user to disable or re-enable them.{/snippet}
	</PageHeading>

	<ListStatus message={listStatus} />

	<div class="flex flex-wrap items-end justify-between gap-4">
		<div class="min-w-60 flex-1">
			{#key searchKey}
				<SearchInput
					label="Search users"
					placeholder="name, username, email, or subject..."
					value={searchQuery}
					onsearch={handleSearch}
					testid="search-users"
				/>
			{/key}
		</div>

		<!-- A segmented control rather than a select: three mutually
		     exclusive options, all worth reading at a glance, and "which
		     view am I looking at" has to be answerable without opening
		     anything. -->
		<div
			class="inline-flex overflow-hidden rounded-md border border-border-control"
			role="group"
			aria-label="Filter by account state"
			data-testid="status-filter"
		>
			{#each statusFilters as filter (filter.value)}
				<button
					type="button"
					onclick={() => selectStatus(filter.value)}
					aria-pressed={status === filter.value}
					data-testid="status-filter-{filter.value}"
					class="inline-flex min-h-target items-center gap-1.5 px-3 text-sm transition focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
					class:bg-ink={status === filter.value}
					class:text-surface={status === filter.value}
					class:text-ink-muted={status !== filter.value}
					class:hover:bg-surface-muted={status !== filter.value}
				>
					{#if status === filter.value}<Icon name="check" size="xs" />{/if}
					{filter.label}
				</button>
			{/each}
		</div>
	</div>

	{#if error}
		<Alert
			variant="error"
			title="Could not load the user list"
			onretry={loadUsers}
			testid="users-error"
		>
			{error}
		</Alert>
		<!--
		Length, not the array itself: an empty array is truthy, so testing
		`users?.users` renders a table of headers with no rows for a search
		that matched nothing, and the "No users found" branch below can never
		be reached on a successful response.
	-->
	{:else if users?.users?.length}
		<div class="overflow-x-auto rounded-lg bg-surface">
			<table class="data-table">
				<thead>
					<tr>
						<th scope="col">Name</th>
						<th scope="col">Username</th>
						<th scope="col">Email</th>
						<th scope="col">Status</th>
						<th scope="col">First sign-in</th>
					</tr>
				</thead>
				<tbody>
					{#each users.users as user (user.id)}
						<tr>
							<!-- The name first, because an admin scanning this list is
							     usually looking for a person rather than for an account
							     name. Empty for anyone whose IdP sent no name claim.
							     Both identity cells are the link to the record, which
							     is what let the Action column go: a column holding one
							     word per row cost more width than it earned, and names
							     and emails are what actually need the space. -->
							<td>
								{#if user.name}
									<a
										href={resolve(`admin/users/${user.id}`)}
										data-testid="user-link"
										class="text-accent hover:underline">{user.name}</a
									>
								{:else}
									<!-- No link whose whole text is a dash; the username beside it is
									     the link to the record. -->
									<span class="text-ink-muted">—</span>
								{/if}
							</td>
							<td class="font-mono">
								<a
									href={resolve(`admin/users/${user.id}`)}
									data-testid="user-username-link"
									class="text-accent hover:underline">{user.username}</a
								>
							</td>
							<td class="text-ink-muted">{user.email || '—'}</td>
							<td>
								{#if user.disabled_at}
									<span class="rounded bg-danger-surface px-2 py-1 text-danger">Disabled</span>
								{:else}
									<span class="rounded bg-granted-surface px-2 py-1 text-granted">Active</span>
								{/if}
							</td>
							<td class="text-ink-muted">
								{formatDateTime(user.created_at)}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		<Pager meta={users.meta} onpage={handlePage} {busy} />
	{:else if busy}
		<LoadingBlock shape="table" count={6} testid="users-loading" />
	{:else}
		{#if filtered}
			<!-- What excluded everyone, with the way back: one message used to
			     blame a search that was not there and say nobody had signed in. -->
			<EmptyState icon="filter-off" title={emptyCopy.title} testid="users-empty">
				{#snippet action()}
					<Button variant="ghost" onclick={clearFilters} testid="users-clear-filters"
						>Clear filters</Button
					>
				{/snippet}
				{emptyCopy.body}
			</EmptyState>
		{:else}
			<EmptyState icon="users" title="No users yet" testid="users-empty">
				A record appears the first time an identity completes a sign-in.
			</EmptyState>
		{/if}
	{/if}
</PageShell>
