<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import SearchInput from '$lib/components/SearchInput.svelte';
	import Pager from '$lib/components/Pager.svelte';
	import { getAdminUsers } from '$lib/api/endpoints';
	import type { AdminUsersListResponse } from '$lib/api/types';

	let users: AdminUsersListResponse | null = $state(null);
	let error: string | null = $state(null);
	let busy = $state(false);
	let searchQuery = $state('');
	let offset = $state(0);

	async function loadUsers() {
		busy = true;
		error = null;
		try {
			users = await getAdminUsers({ q: searchQuery, limit: 25, offset });
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

	<SearchInput
		label="Search users"
		placeholder="username, email, or subject..."
		onsearch={handleSearch}
	/>

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
						<th class="px-3 py-2 text-left font-semibold text-ink">Username</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Email</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Status</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Created</th>
						<th class="px-3 py-2 text-left font-semibold text-ink">Action</th>
					</tr>
				</thead>
				<tbody>
					{#each users.users as user (user.id)}
						<tr class="border-b border-border-subtle hover:bg-surface-muted">
							<td class="px-3 py-2">{user.username}</td>
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
							<td class="px-3 py-2">
								<a href={resolve(`/admin/users/${user.id}`)} class="text-accent hover:underline">
									View
								</a>
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
