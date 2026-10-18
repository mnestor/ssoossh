<script lang="ts">
	import { getNSSAccountAudit, getNSSGroupAudit } from '#lib/api/endpoints.js';
	import type { AuditEvent } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { visibleAuditEvents } from '#lib/audit.js';
	import { describeLoaded } from '#lib/listStatus.js';

	import Alert from './Alert.svelte';
	import AuditTimeline from './AuditTimeline.svelte';
	import LoadingBlock from './LoadingBlock.svelte';
	import LoadMoreFooter from './LoadMoreFooter.svelte';
	import PageSection from './PageSection.svelte';

	// The History section at the foot of a stored NSS account's or group's
	// page: every audit event about the row, newest first, rendered by the
	// same AuditTimeline and LoadMoreFooter as a user's audit timeline. Its
	// own read, so a failure here leaves the facts above standing. The
	// history belongs to the stored row, so it follows a rename or an
	// accepted uid or gid change.

	interface Props {
		/** Which page this is: an account (by uid) or a group (by gid). */
		kind: 'account' | 'group';
		/** The stored uid or gid. */
		number: number;
		/** Prefix for the stable selectors: `<testid>-loading`, `-error`, `-empty`. */
		testid: string;
	}

	const { kind, number, testid }: Props = $props();

	const pageSize = 25;

	let events = $state<AuditEvent[]>([]);
	let total = $state(0);
	let cursor = $state<string | null>(null);
	let loaded = $state(false);
	let busy = $state(false);
	let error = $state<string | null>(null);
	let moreError = $state<string | null>(null);
	// Guards against an older answer landing last when the page moves to
	// another row while a read is out.
	let latest = 0;

	/** read is the endpoint for this kind of row. */
	function read(after?: string) {
		const params = { limit: pageSize, after };
		return kind === 'account'
			? getNSSAccountAudit(number, params)
			: getNSSGroupAudit(number, params);
	}

	/** load reads the first page or, given a cursor, appends the next. */
	async function load(after?: string) {
		const current = ++latest;
		busy = true;
		moreError = null;
		if (!after) error = null;
		try {
			const result = await read(after);
			if (current !== latest) return;
			events = after ? [...events, ...result.events] : result.events;
			total = result.total;
			cursor = result.next_cursor ?? null;
			loaded = true;
		} catch (cause) {
			if (current !== latest || redirectIfUnauthenticated(cause)) return;
			if (after) {
				moreError = errorMessage(cause);
			} else {
				error = errorMessage(cause);
				loaded = true;
			}
		} finally {
			if (current === latest) busy = false;
		}
	}

	// The first page, and again for each row opened from this page.
	$effect(() => {
		void kind;
		void number;
		events = [];
		cursor = null;
		loaded = false;
		void load();
	});

	/** retry is the failed first read's way out: the loading shape, then the read again. */
	function retry() {
		loaded = false;
		void load();
	}

	/** more appends the next page; the footer waits on it to place focus. */
	async function more() {
		if (cursor) await load(cursor);
	}
</script>

<PageSection
	title="History"
	description={kind === 'account'
		? 'Every recorded change to this stored account, newest first: its lifecycle, the group changes that added or removed it, collisions, id changes and shell changes. It follows the row through a rename or an accepted uid change.'
		: 'Every recorded change to this stored group, newest first: its lifecycle, every change to its members, collisions and id changes. It follows the row through a rename or an accepted gid change.'}
	{testid}
>
	{#if error}
		<Alert
			variant="error"
			title="Could not load the history"
			onretry={retry}
			testid="{testid}-error">{error}</Alert
		>
	{:else if !loaded}
		<LoadingBlock shape="lines" count={3} label="Loading the history…" testid="{testid}-loading" />
	{:else if events.length === 0}
		<p class="text-dense text-ink-muted" data-testid="{testid}-empty">No recorded changes.</p>
	{:else}
		<div class="flex flex-col gap-3">
			<AuditTimeline {events} />
			<!-- Counted as a user's timeline is: rows the timeline rendered,
			     against what the server holds. -->
			<LoadMoreFooter
				summary={describeLoaded(visibleAuditEvents(events).length, total - events.length, 'older')}
				hasMore={cursor !== null}
				{busy}
				error={moreError}
				errorTitle="Could not load older events"
				onmore={more}
				{testid}
			/>
		</div>
	{/if}
</PageSection>
