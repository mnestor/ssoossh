<script lang="ts">
	import { onMount } from 'svelte';
	import AuditLogService, { type AuditLogEntry } from '$lib/services/auditlog.service';

	let entries: AuditLogEntry[] = [];
	let loading = true;
	let error = '';

	onMount(async () => {
		try {
			const svc = new AuditLogService();
			entries = await svc.list();
		} catch (e) {
			error = 'Failed to load audit log.';
		} finally {
			loading = false;
		}
	});

	function formatDate(iso: string): string {
		return new Date(iso).toLocaleString();
	}
</script>

<div class="p-6">
	<h1 class="mb-4 text-2xl font-bold">My Request Log</h1>

	{#if loading}
		<p class="text-gray-500">Loading…</p>
	{:else if error}
		<p class="text-red-500">{error}</p>
	{:else if entries.length === 0}
		<p class="text-gray-500">No requests yet.</p>
	{:else}
		<div class="overflow-x-auto rounded border border-gray-200">
			<table class="w-full text-left text-sm">
				<thead class="bg-gray-50 text-xs uppercase text-gray-600">
					<tr>
						<th class="px-4 py-3">Date</th>
						<th class="px-4 py-3">Type</th>
						<th class="px-4 py-3">Account</th>
						<th class="px-4 py-3">Decision</th>
					</tr>
				</thead>
				<tbody>
					{#each entries as entry}
						<tr class="border-t border-gray-200 hover:bg-gray-50">
							<td class="px-4 py-3 text-gray-700">{formatDate(entry.created_at)}</td>
							<td class="px-4 py-3 text-gray-700">{entry.cert_type}</td>
							<td class="px-4 py-3 text-gray-700">{entry.account || '—'}</td>
							<td class="px-4 py-3">
								{#if entry.decision === 'approved'}
									<span class="rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-800">
										approved
									</span>
								{:else}
									<span class="rounded-full bg-red-100 px-2 py-0.5 text-xs font-medium text-red-800">
										rejected
									</span>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>
