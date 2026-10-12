<script lang="ts">
	import { page, type ReadonlyURLSearchParams } from '$app/state';
	import { syncAddressSearch } from '#lib/addressSearch.svelte.js';
	import { getAuditFeed } from '#lib/api/endpoints.js';
	import * as taxonomy from '#lib/api/generated/auditactions.js';
	import type { AuditEvent } from '#lib/api/types.js';
	import {
		auditActionGroups,
		auditFilterSearch,
		isIPAddress,
		visibleAuditEvents
	} from '#lib/audit.js';
	import AuditTimeline from '#lib/components/AuditTimeline.svelte';
	import Alert from '#lib/components/Alert.svelte';
	import LoadMoreFooter from '#lib/components/LoadMoreFooter.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import Button from '#lib/components/Button.svelte';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { describeList, describeLoaded } from '#lib/listStatus.js';

	const pageSize = 50;

	// The action filter's options: every action the feed can return, grouped
	// by namespace, from the generated taxonomy. Twenty-odd actions are too
	// many for a row of chips, so this is a select.
	const actionGroups = auditActionGroups(
		Object.values(taxonomy).filter((value): value is string => typeof value === 'string')
	);
	const knownActions = actionGroups.flatMap((group) => group.actions);

	// How long the address field waits after the last keystroke before it
	// filters: the same settle a search box uses.
	const sourceSettleMs = 400;

	// Filter state, read from the address the page opened at and written back
	// to it, as /logs/me does, so a filtered view survives a reload and can be
	// sent to someone. An action the taxonomy does not know is dropped rather
	// than sent: the server would refuse it. An address that does not parse
	// stays in the field, marked, and filters nothing until it is fixed.
	let selectedAction = $state('');
	let selectedSource = $state('');
	let sourceDraft = $state('');
	let sourceInvalid = $state(false);
	let settleTimer: ReturnType<typeof setTimeout> | undefined;

	/**
	 * readFilters sets every filter from a query string, dropping any
	 * address still waiting to settle. A key it does not carry puts that
	 * filter back at its default, so an empty one clears.
	 */
	function readFilters(params: ReadonlyURLSearchParams) {
		clearTimeout(settleTimer);
		const action = params.get('action') ?? '';
		const source = (params.get('source_ip') ?? '').trim();
		selectedAction = knownActions.includes(action) ? action : '';
		selectedSource = isIPAddress(source) ? source : '';
		sourceDraft = source;
		sourceInvalid = source !== '' && !isIPAddress(source);
	}
	readFilters(page.url.searchParams);

	let events: AuditEvent[] = $state([]);
	let total = $state(0);
	// The cursor for the next page, or null when the last one has loaded.
	let nextCursor: string | null = $state(null);
	let error: string | null = $state(null);
	// A failed "Load more" is its own error: the events already shown are
	// still right, so it sits by the button and retries the page that
	// failed, rather than replacing the list with the first page again.
	let moreError: string | null = $state(null);
	let failedCursor: string | null = $state(null);
	let busy = $state(true);
	let hasLoaded = $state(false);

	// Which load may write to the page. A filter changed while a read is in
	// flight starts another, and only the newest one's answer lands, so a
	// slow unfiltered page can never arrive after, and over, a filtered one.
	let latestLoad = 0;

	/**
	 * load reads one page of the feed under the current filters: the first
	 * page when after is null, otherwise the page after that cursor.
	 */
	async function load(after: string | null = null) {
		const ticket = ++latestLoad;
		const action = selectedAction;
		const sourceIP = selectedSource;
		busy = true;
		moreError = null;
		if (after === null) {
			error = null;
		}
		try {
			const result = await getAuditFeed({
				limit: pageSize,
				after: after ?? undefined,
				action,
				source_ip: sourceIP
			});
			if (ticket !== latestLoad) return;
			// Append when paging forward, replace on the first load, so
			// "load more" grows one continuous list. The server pages by
			// cursor, so an event recorded between two pages lands above
			// the cursor rather than repeating a row below it.
			events = after === null ? result.events : [...events, ...result.events];
			total = result.total;
			nextCursor = result.next_cursor ?? null;
		} catch (cause) {
			if (ticket !== latestLoad) return;
			// An expired session is a sign-in, not a failed feed.
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			if (after === null) {
				// The rows on screen belonged to the previous filters; leaving
				// them under an error about these ones would misstate both.
				events = [];
				total = 0;
				nextCursor = null;
				error = errorMessage(cause);
			} else {
				moreError = errorMessage(cause);
				failedCursor = after;
			}
		} finally {
			if (ticket === latestLoad) {
				busy = false;
				hasLoaded = true;
			}
		}
	}

	const isFiltered = $derived(selectedAction !== '' || selectedSource !== '');

	/** applySource filters to a typed address once it parses, and clears the filter when empty. */
	function applySource(value: string) {
		const trimmed = value.trim();
		if (trimmed === '') {
			sourceInvalid = false;
			selectedSource = '';
			return;
		}
		if (isIPAddress(trimmed)) {
			sourceInvalid = false;
			selectedSource = trimmed;
		}
	}

	/**
	 * onSourceInput filters as typing settles, like a search box. A half-typed
	 * address is not an error yet; it simply changes nothing until it parses.
	 */
	function onSourceInput() {
		clearTimeout(settleTimer);
		settleTimer = setTimeout(() => applySource(sourceDraft), sourceSettleMs);
	}

	/**
	 * settleSource applies the field at once. Enter and leaving the field call
	 * it, and they are the moments an address that still does not parse is
	 * called out.
	 */
	function settleSource() {
		clearTimeout(settleTimer);
		applySource(sourceDraft);
		const trimmed = sourceDraft.trim();
		sourceInvalid = trimmed !== '' && !isIPAddress(trimmed);
	}

	/**
	 * filterToSource is a source address clicked in a row. The action filter
	 * stays: an address picked out of a run of refusals should show that run.
	 */
	function filterToSource(ip: string) {
		clearTimeout(settleTimer);
		sourceDraft = ip;
		sourceInvalid = false;
		selectedSource = ip;
	}

	/** clearFilters returns to the whole feed, as the bare address does. */
	function clearFilters() {
		readFilters(new URLSearchParams());
	}

	/**
	 * sourceHref is where a row's source link points: this page filtered to
	 * that address, with the current action kept. A plain click applies it
	 * in place (filterToSource); a new tab follows the address.
	 */
	function sourceHref(ip: string): string {
		return `${page.url.pathname}${auditFilterSearch(selectedAction, ip)}`;
	}

	// Keep the address bar and the filters in step, both ways: a nav click
	// to this page starts over (#182). See #lib/addressSearch for why a
	// hand-written effect here hung the page (#178).
	syncAddressSearch(() => auditFilterSearch(selectedAction, selectedSource), readFilters);

	// Reads the filters, and only the filters: a change reloads from the
	// first page. Everything load() writes is settled inside it.
	$effect(() => {
		void selectedAction;
		void selectedSource;
		load();
	});

	// A pending settle must not fire into a page that has gone.
	$effect(() => () => clearTimeout(settleTimer));

	const shownCount = $derived(visibleAuditEvents(events).length);
	// What is still on the server, counted the same way the server counts:
	// loaded events against its total. The footer used to set the shown
	// count against the raw total, which read "48 of 50" with everything
	// loaded because two hidden actions were never going to show.
	const notLoaded = $derived(Math.max(0, total - events.length));

	// What narrowed the list, for the announcement: "cert.requested from
	// 192.0.2.10".
	const filterWords = $derived(
		[selectedAction, selectedSource ? `from ${selectedSource}` : ''].filter(Boolean).join(' ')
	);

	// What the timeline just became, for a reader who cannot see rows
	// arrive under Load more or a filter. See #lib/listStatus.
	const listStatus = $derived(
		describeList({
			noun: 'audit event',
			total: shownCount,
			loading: busy,
			ready: hasLoaded,
			query: filterWords
		})
	);
</script>

<svelte:head><title>Audit log · ssoossh</title></svelte:head>

<PageShell width="full">
	<PageHeading title="Audit log">
		{#snippet sub()}
			Recent administrative activity, newest first, narrowed by action or by the address a request
			came from. This is a bounded cache kept for this view; the shipped audit log is the archive,
			and any other search happens there.
		{/snippet}
	</PageHeading>

	<!-- The filters stay on screen in every state, the error and the empty
	     list included: a filter that matched nothing, or a read that failed
	     under one, needs its way back to the full feed in reach. -->
	<form
		class="flex flex-wrap items-end gap-x-5 gap-y-3"
		role="search"
		aria-label="Filter the audit log"
		onsubmit={(e) => {
			e.preventDefault();
			settleSource();
		}}
		data-testid="audit-filters"
	>
		<div class="flex flex-col gap-1">
			<label for="audit-action-filter" class="text-xs font-semibold text-ink-muted">Action</label>
			<select
				id="audit-action-filter"
				bind:value={selectedAction}
				data-testid="audit-action-filter"
				class="min-h-target rounded-md border border-border-control bg-surface px-3 py-2 font-mono text-dense text-ink hover:bg-surface-muted"
			>
				<option value="">Any action</option>
				{#each actionGroups as group (group.label)}
					<optgroup label={group.label}>
						{#each group.actions as action (action)}
							<option value={action}>{action}</option>
						{/each}
					</optgroup>
				{/each}
			</select>
		</div>

		<div class="flex flex-col gap-1">
			<label for="audit-source-filter" class="text-xs font-semibold text-ink-muted">
				Source address
			</label>
			<!-- Mono: an address is a value a reader compares character by
			     character (DESIGN.md, "Where the mono goes"). -->
			<input
				id="audit-source-filter"
				type="text"
				autocomplete="off"
				spellcheck="false"
				placeholder="192.0.2.10"
				bind:value={sourceDraft}
				oninput={onSourceInput}
				onkeydown={(e) => {
					if (e.key === 'Enter') {
						e.preventDefault();
						settleSource();
					}
				}}
				onblur={settleSource}
				aria-invalid={sourceInvalid}
				aria-describedby={sourceInvalid ? 'audit-source-error' : undefined}
				data-testid="audit-source-filter"
				class="min-h-target w-60 max-w-full rounded-md border border-border-control bg-surface px-3 py-2 font-mono text-dense text-ink placeholder:text-ink-muted"
			/>
		</div>

		{#if isFiltered || sourceInvalid}
			<Button variant="ghost" onclick={clearFilters} testid="audit-clear-filters">
				Clear filters
			</Button>
		{/if}

		{#if sourceInvalid}
			<p
				id="audit-source-error"
				class="basis-full text-xs text-danger"
				data-testid="audit-source-error"
			>
				Enter one IP address, like 192.0.2.10 or 2001:db8::1. The list is not filtered by it yet.
			</p>
		{/if}
	</form>

	{#if error}
		<Alert
			variant="error"
			title="Could not load the audit log"
			onretry={() => load()}
			testid="audit-error"
		>
			{error}
		</Alert>
	{/if}

	<ListStatus message={listStatus} />

	{#if busy && events.length === 0 && !error}
		<LoadingBlock shape="lines" count={6} testid="audit-loading" />
	{:else if !error && shownCount === 0}
		<!-- Two different empties, told apart (DESIGN.md, EmptyState): the
		     filters excluding everything, and a table with nothing in it. Not
		     under a failed load, where "nothing here" would read as "nothing
		     happened". -->
		{#if isFiltered}
			<EmptyState icon="filter-off" title="No events match these filters" testid="audit-no-match">
				Nothing in the recent table matches. Older events, and anything the table no longer holds,
				are in the shipped audit log.
				{#snippet action()}
					<Button variant="ghost" onclick={clearFilters} testid="audit-no-match-clear">
						Clear filters
					</Button>
				{/snippet}
			</EmptyState>
		{:else}
			<EmptyState icon="logs" title="No audit events yet" testid="audit-empty">
				Sign-ins, certificate decisions and admin actions appear here as they happen.
			</EmptyState>
		{/if}
	{:else if !error}
		<!-- In a surface panel (DESIGN.md, the admin area): the ruled rows
		     read as one list there, rather than as loose text on the ground. -->
		<div class="rounded-lg bg-surface p-5">
			<AuditTimeline {events} {sourceHref} onsource={filterToSource} />
		</div>

		<LoadMoreFooter
			summary={describeLoaded(shownCount, notLoaded, 'older')}
			hasMore={nextCursor !== null}
			{busy}
			error={moreError}
			errorTitle="Could not load older events"
			onmore={() => load(nextCursor)}
			onretry={() => load(failedCursor)}
			testid="audit"
		/>
	{/if}
</PageShell>
