<script lang="ts">
	import { onMount } from 'svelte';
	import { getAuditFeed } from '#lib/api/endpoints.js';
	import type { AuditEvent } from '#lib/api/types.js';
	import { dedupeAuditEvents, visibleAuditEvents } from '#lib/audit.js';
	import AuditTimeline from '#lib/components/AuditTimeline.svelte';
	import Alert from '#lib/components/Alert.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import Button from '#lib/components/Button.svelte';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { describeList } from '#lib/listStatus.js';

	const pageSize = 50;

	let events: AuditEvent[] = $state([]);
	let total = $state(0);
	let nextOffset = $state(0);
	let error: string | null = $state(null);
	// A failed "Load more" is its own error: the events already shown are
	// still right, so it sits by the button and retries the page that
	// failed, rather than replacing the list with the first page again.
	let moreError: string | null = $state(null);
	let failedOffset = $state(0);
	let busy = $state(true);

	async function load(offset = 0) {
		busy = true;
		if (offset === 0) {
			error = null;
		} else {
			moreError = null;
		}
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
			// An expired session is a sign-in, not a failed feed.
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			if (offset === 0) {
				error = errorMessage(cause);
			} else {
				moreError = errorMessage(cause);
				failedOffset = offset;
			}
		} finally {
			busy = false;
		}
	}

	const shownCount = $derived(visibleAuditEvents(events).length);
	// What is still on the server, counted the same way the server counts:
	// loaded events against its total. The footer used to set the shown
	// count against the raw total, which read "48 of 50" with everything
	// loaded because two hidden actions were never going to show.
	const notLoaded = $derived(Math.max(0, total - events.length));

	// What the timeline just became, for a reader who cannot see rows
	// arrive under Load more. See #lib/listStatus.
	const listStatus = $derived(
		describeList({
			noun: 'audit event',
			total: shownCount,
			loading: busy,
			ready: events.length > 0 || !busy
		})
	);

	onMount(() => load(0));
</script>

<svelte:head><title>Audit log · ssoossh</title></svelte:head>

<PageShell width="full">
	<PageHeading title="Audit log">
		{#snippet sub()}
			Recent administrative activity, newest first. This is a bounded cache of recent events kept
			for this view; the shipped audit log is the archive, and searching happens there.
		{/snippet}
	</PageHeading>

	{#if error}
		<Alert
			variant="error"
			title="Could not load the audit log"
			onretry={() => load(0)}
			testid="audit-error"
		>
			{error}
		</Alert>
	{/if}

	<ListStatus message={listStatus} />

	{#if busy && events.length === 0}
		<LoadingBlock shape="lines" count={6} testid="audit-loading" />
	{:else if !error || events.length > 0}
		<!-- Not under a failed first load: the timeline's own "No audit
		     events recorded" read as "nothing happened" right beneath the
		     error that said the log could not be read. -->
		<AuditTimeline {events} />

		<div class="flex flex-col gap-3">
			<p class="text-xs text-ink-muted tabular-nums" data-testid="audit-count">
				{shownCount} shown{#if notLoaded > 0}, {notLoaded} older not loaded{/if}
			</p>
			{#if moreError}
				<Alert
					variant="error"
					title="Could not load older events"
					onretry={() => load(failedOffset)}
					testid="audit-more-error"
				>
					{moreError}
				</Alert>
			{:else if nextOffset > 0}
				<div>
					<Button variant="ghost" {busy} onclick={() => load(nextOffset)} testid="audit-load-more">
						{busy ? 'Loading…' : 'Load more'}
					</Button>
				</div>
			{/if}
		</div>
	{/if}
</PageShell>
