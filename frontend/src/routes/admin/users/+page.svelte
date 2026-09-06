<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import SearchInput from '$lib/components/SearchInput.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import { getAdminUsers } from '$lib/api/endpoints';
	import type { AdminUsersListResponse } from '$lib/api/types';

	/** The account-state filter, matching the server's `status` parameter. */
	type StatusFilter = 'all' | 'active' | 'disabled';

	const statusFilters: { value: StatusFilter; label: string }[] = [
		{ value: 'all', label: 'All' },
		{ value: 'active', label: 'Active' },
		{ value: 'disabled', label: 'Disabled' }
	];

	let users: AdminUsersListResponse | null = $state(null);
	let error: string | null = $state(null);
	let busy = $state(false);
	let searchQuery = $state('');
	let status: StatusFilter = $state('all');
	let offset = $state(0);

	async function loadUsers() {
		busy = true;
		error = null;
		try {
			// 'all' is sent as no parameter rather than as a value: the
			// server's filter is "active", "disabled", or absent, and an
			// unrecognized value is a 400 rather than a silent widening.
			users = await getAdminUsers({
				q: searchQuery,
				limit: 25,
				offset,
				status: status === 'all' ? undefined : status
			});
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to load users';
		} finally {
			busy = false;
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

	onMount(loadUsers);
</script>

<div class="flex max-w-full flex-col gap-6">
	<div>
		<h1 class="text-2xl font-bold text-ink">Users</h1>
		<p class="text-sm text-ink-muted">Directory of all users with disable controls</p>
	</div>

	<div class="flex flex-wrap items-end justify-between gap-4">
		<div class="min-w-[240px] flex-1">
			<SearchInput
				label="Search users"
				placeholder="name, username, email, or subject..."
				onsearch={handleSearch}
				testid="search-users"
			/>
		</div>

		<!-- A segmented control rather than a select: three mutually
		     exclusive options, all worth reading at a glance, and "which
		     view am I looking at" has to be answerable without opening
		     anything. -->
		<div
			class="inline-flex overflow-hidden rounded-lg border border-border-subtle"
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
					class="px-3 py-2 text-sm transition focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
					class:bg-accent={status === filter.value}
					class:text-white={status === filter.value}
					class:text-ink-muted={status !== filter.value}
					class:hover:bg-surface-muted={status !== filter.value}
				>
					{filter.label}
				</button>
			{/each}
		</div>
	</div>

	{#if error}
		<div class="rounded-lg border border-danger-surface bg-danger-surface p-4 text-sm text-danger">
			{error}
		</div>
		<!--
		Length, not the array itself: an empty array is truthy, so testing
		`users?.users` renders a table of headers with no rows for a search
		that matched nothing, and the "No users found" branch below can never
		be reached on a successful response.
	-->
	{:else if users?.users?.length}
		<div class="overflow-x-auto">
			<table class="w-full text-sm">
				<thead>
					<tr class="border-b border-border-subtle">
						<th class="px-3 py-2 text-left font-semibold text-ink">Name</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Username</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Email</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Status</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Created</th>
					</tr>
				</thead>
				<tbody>
					{#each users.users as user (user.id)}
						<tr class="border-b border-border-subtle hover:bg-surface-muted">
							<!-- The name first, because an admin scanning this list is
							     usually looking for a person rather than for an account
							     name. Empty for anyone whose IdP sent no name claim.
							     Both identity cells are the link to the record, which
							     is what let the Action column go: a column holding one
							     word per row cost more width than it earned, and names
							     and emails are what actually need the space. -->
							<td class="px-3 py-2">
								<a
									href={resolve(`/admin/users/${user.id}`)}
									data-testid="user-link"
									class="text-accent hover:underline">{user.name || '—'}</a
								>
							</td>
							<td class="px-3 py-2 font-mono">
								<a href={resolve(`/admin/users/${user.id}`)} class="text-accent hover:underline"
									>{user.username}</a
								>
							</td>
							<td class="px-3 py-2 text-ink-muted">{user.email || '—'}</td>
							<td class="px-3 py-2">
								{#if user.disabled_at}
									<span class="rounded bg-danger-surface px-2 py-1 text-danger">Disabled</span>
								{:else}
									<span class="rounded bg-granted-surface px-2 py-1 text-granted">Active</span>
								{/if}
							</td>
							<td class="px-3 py-2 text-ink-muted">
								{new Date(user.created_at).toLocaleDateString()}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		<Pager meta={users.meta} onpage={handlePage} {busy} />
	{:else if busy}
		<div class="py-8 text-center text-ink-muted">Loading...</div>
	{:else}
		<div class="py-8 text-center text-ink-muted">No users found</div>
	{/if}
</div>
