<script lang="ts">
	import { resolve } from '$app/paths';
	import { page, type ReadonlyURLSearchParams } from '$app/state';

	import { syncAddressSearch } from '#lib/addressSearch.svelte.js';
	import { ApiError } from '#lib/api/client.js';
	import { listNSSAccounts, listNSSGroups } from '#lib/api/endpoints.js';
	import type { NSSEntryState } from '#lib/api/generated/enums.js';
	import type { NSSAccountSummary, NSSGroupSummary } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { formatDateTime } from '#lib/format.js';
	import { describeList, describeLoaded } from '#lib/listStatus.js';
	import { entryStateLabel, entryStates, parseEntryState, type EntryKind } from '#lib/nss.js';
	import { session } from '#lib/session.svelte.js';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import ConfigProse from './ConfigProse.svelte';
	import EmptyState from './EmptyState.svelte';
	import ListStatus from './ListStatus.svelte';
	import LoadingBlock from './LoadingBlock.svelte';
	import LoadMoreFooter from './LoadMoreFooter.svelte';
	import NSSDirectoryRef from './NSSDirectoryRef.svelte';
	import NSSStateBadge from './NSSStateBadge.svelte';
	import SearchInput from './SearchInput.svelte';

	// One of the NSS directory's two lists, the stored accounts or the stored
	// groups: search, the state filter, the table, then Load more. One
	// component for both because they differ only in columns and words, and a
	// reader who learns one should already know the other.
	//
	// Search and the state filter are the server's (q, state), kept in the
	// address like every other list's, so a pasted link opens the same view.
	// The rows are what the server stored; whether a host is served one is
	// said on its own page, never here.

	interface Props {
		kind: EntryKind;
	}

	let { kind }: Props = $props();

	// The words that differ between the two lists.
	const words = $derived(
		kind === 'account'
			? {
					noun: 'stored account',
					plural: 'stored accounts',
					filterKey: 'nss.user_filter',
					placeholder: 'name, uid or DN'
				}
			: {
					noun: 'stored group',
					plural: 'stored groups',
					filterKey: 'nss.group_filter',
					placeholder: 'name, gid or DN'
				}
	);

	type Row = NSSAccountSummary | NSSGroupSummary;

	let rows = $state<Row[]>([]);
	let total = $state(0);
	let nextCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	let moreError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);
	// A 404 naming nss.enabled: NSS went off after the session said it was on.
	let nssGone = $state(false);
	const nssOff = $derived(!(session.user?.nss_enabled ?? false) || nssGone);

	let searchQuery = $state('');
	let stateFilter = $state<NSSEntryState | ''>('');

	/** readFilters sets both filters from a query string; a missing key is the default. */
	function readFilters(params: ReadonlyURLSearchParams) {
		searchQuery = params.get('q')?.trim() ?? '';
		stateFilter = parseEntryState(params.get('state'), kind);
	}
	readFilters(page.url.searchParams);

	// Which load may write to the page; see the certificate list. Not
	// $state: nothing renders from it.
	let latestLoad = 0;

	/**
	 * load reads the first page under the given filters or, given the
	 * previous page's cursor, appends the page after it.
	 */
	async function load(query: { q: string; state: NSSEntryState | '' }, after?: string) {
		const current = ++latestLoad;
		isLoading = true;
		moreError = null;
		if (!after) {
			loadError = null;
		}
		const options = { q: query.q || undefined, state: query.state || undefined, after, limit: 50 };
		try {
			const result =
				kind === 'account'
					? await listNSSAccounts(options).then((page) => ({
							rows: page.accounts as Row[],
							total: page.total,
							next: page.next_cursor
						}))
					: await listNSSGroups(options).then((page) => ({
							rows: page.groups as Row[],
							total: page.total,
							next: page.next_cursor
						}));
			if (current !== latestLoad) return;
			rows = after ? [...rows, ...result.rows] : result.rows;
			total = result.total;
			nextCursor = result.next ?? null;
			hasLoaded = true;
		} catch (cause) {
			if (current !== latestLoad || redirectIfUnauthenticated(cause)) return;
			if (cause instanceof ApiError && cause.isNotFound && !after) {
				nssGone = true;
			} else if (after) {
				moreError = errorMessage(cause);
			} else {
				loadError = errorMessage(cause);
			}
			hasLoaded = true;
		} finally {
			if (current === latestLoad) {
				isLoading = false;
			}
		}
	}

	// The only first-page loader: it reads both filters, so it re-runs on
	// every change. Nothing is asked of a server that does not serve NSS.
	$effect(() => {
		if (nssOff) return;
		void load({ q: searchQuery, state: stateFilter });
	});

	function retry() {
		void load({ q: searchQuery, state: stateFilter });
	}

	async function loadMore() {
		if (nextCursor) {
			await load({ q: searchQuery, state: stateFilter }, nextCursor);
		}
	}

	const filtered = $derived(searchQuery !== '' || stateFilter !== '');

	// The filters as a query string, defaults left out: what the address
	// carries and what a row's page returns to through its back chip.
	const filterSearch = $derived.by(() => {
		const parts: string[] = [];
		if (searchQuery) parts.push(`q=${encodeURIComponent(searchQuery)}`);
		if (stateFilter) parts.push(`state=${stateFilter}`);
		return parts.length ? `?${parts.join('&')}` : '';
	});

	syncAddressSearch(() => filterSearch, followAddress);

	// Clearing remounts the search box, which does not watch its value.
	let searchKey = $state(0);

	/** followAddress sets the filters from an address this page was sent to while on screen. */
	function followAddress(params: ReadonlyURLSearchParams) {
		readFilters(params);
		searchKey += 1;
	}

	function clearFilters() {
		followAddress(new URLSearchParams());
	}

	/** rowHref opens one entry by its number, carrying the filters for the way back. */
	function rowHref(row: Row): string {
		const back = filterSearch ? `?back=${encodeURIComponent(filterSearch)}` : '';
		return 'uid' in row
			? `${resolve('/admin/nss/accounts/[uid]', { uid: String(row.uid) })}${back}`
			: `${resolve('/admin/nss/groups/[gid]', { gid: String(row.gid) })}${back}`;
	}

	const stateOptions = $derived(entryStates(kind));
	const stateSelectId = $props.id();

	const listStatus = $derived(
		describeList({
			noun: words.noun,
			total,
			loading: isLoading,
			ready: hasLoaded,
			query: searchQuery
		})
	);
</script>

{#if nssOff}
	<Alert variant="info" title="The NSS directory is off" testid="nss-list-off">
		<code>nss.enabled</code> is false, so the server stores no accounts or groups for hosts and serves
		none.
	</Alert>
{:else}
	<!-- Search first, then the one filter beside it. -->
	<div class="flex flex-wrap items-end gap-4">
		<div class="min-w-60 flex-1">
			{#key searchKey}
				<SearchInput
					label="Search {words.plural}"
					placeholder={words.placeholder}
					value={searchQuery}
					onsearch={(query) => (searchQuery = query)}
					testid="nss-search"
				/>
			{/key}
		</div>
		<!-- A select rather than chips: six states, each a word with no glyph
		     of its own, as the audit log's action filter is. -->
		<div class="flex flex-col gap-1">
			<label for={stateSelectId} class="text-xs font-semibold text-ink-muted">State</label>
			<select
				id={stateSelectId}
				value={stateFilter}
				onchange={(event) => (stateFilter = parseEntryState(event.currentTarget.value, kind))}
				data-testid="nss-state-filter"
				class="min-h-target rounded-md border border-border-control bg-surface px-3 text-base text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent sm:text-sm"
			>
				<option value="">Any state</option>
				{#each stateOptions as option (option)}
					<option value={option}>{entryStateLabel(option)}</option>
				{/each}
			</select>
		</div>
	</div>

	<ListStatus message={listStatus} />

	{#if loadError}
		<Alert
			variant="error"
			title="Could not load the {words.plural}"
			onretry={retry}
			testid="nss-list-error">{loadError}</Alert
		>
	{:else if !hasLoaded || (isLoading && rows.length === 0)}
		<LoadingBlock shape="table" count={6} testid="nss-list-loading" />
	{:else if rows.length === 0 && filtered}
		<EmptyState icon="filter-off" title="No {words.noun} matches" testid="nss-list-empty">
			{#snippet action()}
				<Button variant="ghost" onclick={clearFilters} testid="nss-clear-filters"
					>Clear filters</Button
				>
			{/snippet}
			Nothing stored matches this search and state.
			{#if searchQuery}
				<span data-testid="nss-list-empty-lookup">
					A host that looks it up still asks the directory, which this page never does.
					<NSSDirectoryRef section="probe" testid="nss-list-empty-probe" />
				</span>
			{/if}
		</EmptyState>
	{:else if rows.length === 0}
		<EmptyState icon="database-search" title="No {words.plural} yet" testid="nss-list-empty">
			<ConfigProse text="The sync stores the {kind}s {words.filterKey} selects on its next pass." />
			<NSSDirectoryRef section="sync" testid="nss-list-empty-sync" />
		</EmptyState>
	{:else}
		<!-- relative: the sr-only text inside is absolutely placed. -->
		<div class="relative overflow-x-auto rounded-lg bg-surface">
			<table class="data-table" data-testid="nss-list-table">
				<thead>
					<tr>
						<th scope="col">Name</th>
						{#if kind === 'account'}
							<th scope="col" class="text-right">UID</th>
							<th scope="col" class="text-right">GID</th>
							<th scope="col" class="hidden sm:table-cell">Gecos</th>
						{:else}
							<th scope="col" class="text-right">GID</th>
							<th scope="col" class="text-right">Members</th>
						{/if}
						<th scope="col">State</th>
						<th scope="col">Last synced</th>
					</tr>
				</thead>
				<tbody>
					{#each rows as row (row.id)}
						<tr data-testid="nss-list-row">
							<td>
								<a
									href={rowHref(row)}
									class="font-mono text-accent hover:underline"
									data-testid="nss-list-link">{row.name}</a
								>
							</td>
							{#if 'uid' in row}
								<td class="text-right font-mono">{row.uid}</td>
								<td class="text-right font-mono">{row.gid ?? 'none'}</td>
								<td class="hidden text-ink-muted sm:table-cell">{row.gecos || '—'}</td>
							{:else}
								<td class="text-right font-mono">{row.gid}</td>
								<td class="text-right">{row.member_count}</td>
							{/if}
							<td><NSSStateBadge state={row.state} /></td>
							<td class="whitespace-nowrap text-ink-muted">{formatDateTime(row.last_synced_at)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>

		<LoadMoreFooter
			summary={describeLoaded(rows.length, total - rows.length, 'more')}
			hasMore={!!nextCursor}
			busy={isLoading}
			error={moreError}
			errorTitle="Could not load more {words.plural}"
			onmore={loadMore}
			testid="nss-list"
		/>
	{/if}
{/if}
