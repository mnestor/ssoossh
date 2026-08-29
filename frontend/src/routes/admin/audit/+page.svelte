<script lang="ts">
	import { onMount } from 'svelte';
	import { getAuditFeed } from '$lib/api/endpoints';
	import AuditTimeline from '$lib/components/AuditTimeline.svelte';
	import Button from '$lib/components/Button.svelte';
	import type { AuditEvent } from '$lib/api/types';

	const pageSize = 50;

	let events: AuditEvent[] = $state([]);
	let total = $state(0);
	let nextOffset = $state(0);
	let error: string | null = $state(null);
	let busy = $state(false);

	async function load(offset = 0) {
		busy = true;
		error = null;
		try {
			const page = await getAuditFeed({ limit: pageSize, offset });
			// Append when paging forward, replace on the first load, so
			// "load more" grows one continuous list.
			events = offset === 0 ? page.events : [...events, ...page.events];
			total = page.total;
			nextOffset = page.next_offset ?? 0;
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to load the audit feed';
		} finally {
			busy = false;
		}
	}

	onMount(() => load(0));
</script>

<div class="flex max-w-full flex-col gap-6">
	<div>
		<h1 class="text-2xl font-bold text-ink">Audit Log</h1>
		<p class="text-sm text-ink-muted">
			Recent administrative activity, newest first. This is a bounded cache of recent events kept
			for this view; the shipped audit log is the archive, and searching happens there.
		</p>
	</div>

	{#if error}
		<p class="text-sm text-danger" data-testid="audit-error">{error}</p>
	{/if}

	{#if busy && events.length === 0}
		<p class="text-ink-muted">Loading...</p>
	{:else}
		<div class="rounded-lg border border-border-subtle bg-surface p-4">
			<AuditTimeline {events} />
		</div>

		<div class="flex items-center gap-4">
			<p class="text-xs text-ink-muted">
				Showing {events.length} of {total}
			</p>
			{#if nextOffset > 0}
				<Button variant="ghost" disabled={busy} onclick={() => load(nextOffset)}>
					{busy ? 'Loading...' : 'Load more'}
				</Button>
			{/if}
		</div>
	{/if}
</div>
