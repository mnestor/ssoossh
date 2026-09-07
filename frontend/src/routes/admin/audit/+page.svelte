<script lang="ts">
	import { onMount } from 'svelte';
	import { getAuditFeed } from '$lib/api/endpoints';
	import type { AuditEvent } from '$lib/api/types';
	import { dedupeAuditEvents, visibleAuditEvents } from '$lib/audit';
	import AuditTimeline from '$lib/components/AuditTimeline.svelte';
	import Button from '$lib/components/Button.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';

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
			//
			// Deduplicated by id: offset paging over a live table cannot
			// promise the window holds still, and a repeat is not cosmetic
			// here — the timeline keys its {#each} by id, so it throws
			// each_key_duplicate and takes the page down. See
			// dedupeAuditEvents.
			events = offset === 0 ? page.events : dedupeAuditEvents([...events, ...page.events]);
			total = page.total;
			nextOffset = page.next_offset ?? 0;
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to load the audit feed';
		} finally {
			busy = false;
		}
	}

	const shownCount = $derived(visibleAuditEvents(events).length);

	onMount(() => load(0));
</script>

<PageShell width="full">
	<PageHeading title="Audit log">
		{#snippet sub()}
			Recent administrative activity, newest first. This is a bounded cache of recent events kept
			for this view; the shipped audit log is the archive, and searching happens there.
		{/snippet}
	</PageHeading>

	{#if error}
		<p class="text-sm text-danger" data-testid="audit-error">{error}</p>
	{/if}

	{#if busy && events.length === 0}
		<p class="text-ink-muted">Loading...</p>
	{:else}
		<AuditTimeline {events} />

		<div class="flex items-center gap-4">
			<!-- The rendered count, not the loaded one: the timeline drops
			     the privileged-view actions, and a footer that counted rows
			     it did not show would keep saying "showing 50" beneath a
			     shorter list. -->
			<p class="text-xs text-ink-muted">
				Showing {shownCount} of {total}
			</p>
			{#if nextOffset > 0}
				<Button variant="ghost" disabled={busy} onclick={() => load(nextOffset)}>
					{busy ? 'Loading...' : 'Load more'}
				</Button>
			{/if}
		</div>
	{/if}
</PageShell>
