<script lang="ts">
	import { resolve } from '$app/paths';
	import { page, type ReadonlyURLSearchParams } from '$app/state';

	import { syncAddressSearch } from '#lib/addressSearch.svelte.js';
	import { ApiError } from '#lib/api/client.js';
	import { listAdminNSSKeys } from '#lib/api/endpoints.js';
	import {
		NSSKeyStatusActive,
		NSSKeyStatusExpired,
		NSSKeyStatusRevoked,
		NSSSecretGenerationCurrent,
		NSSSecretGenerationPrevious,
		type NSSKeyStatus,
		type NSSSecretGeneration
	} from '#lib/api/generated/enums.js';
	import type { NSSAdminKey } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { formatDateTime, relativeTime, remainingLabel } from '#lib/format.js';
	import { describeList, describeLoaded } from '#lib/listStatus.js';
	import { expiryTone, keyStatus } from '#lib/nss.js';
	import { readKeySearch, usageWindowPhrase } from '#lib/nssKeySearch.js';
	import { session } from '#lib/session.svelte.js';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import EmptyState from './EmptyState.svelte';
	import FilterChip from './FilterChip.svelte';
	import FilterGroup from './FilterGroup.svelte';
	import Icon from './Icon.svelte';
	import KeyStatusBadge from './KeyStatusBadge.svelte';
	import ListStatus from './ListStatus.svelte';
	import LoadingBlock from './LoadingBlock.svelte';
	import LoadMoreFooter from './LoadMoreFooter.svelte';

	// Every fleet key, whoever manages it, for the admin area's roles: the
	// list an investigation starts from. The clue is usually an address
	// from a log or an alert, else a key's id, name, group or creator, so one
	// search field leads and reads both: text searches the key, an address
	// or a CIDR range searches the usage the server kept. Read only: a row
	// opens the key's own page, which keeps every action.
	//
	// The worst case is concluding an address uses no key when the search
	// quietly missed, so a term shaped like an address that is not one is
	// refused in the field rather than searched as text, the line under the
	// field says what is being searched and over how many days, and an
	// empty address search says what the server matches against.

	let rows = $state<NSSAdminKey[]>([]);
	let total = $state(0);
	let nextCursor = $state<string | null>(null);
	let loadError = $state<string | null>(null);
	let moreError = $state<string | null>(null);
	let isLoading = $state(false);
	let hasLoaded = $state(false);
	// A 404: NSS went off after the session said it was on.
	let nssGone = $state(false);
	const nssOff = $derived(!(session.user?.nss_enabled ?? false) || nssGone);

	// The answer's reading of an address search and the window it covers.
	let addressSearch = $state('');
	let retentionSeconds = $state(0);

	// Expiry and last use are read against a clock that moves.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 60_000);
		return () => clearInterval(timer);
	});

	const statusOptions = [
		{ value: '', label: 'All', icon: 'filter-off' },
		{ value: NSSKeyStatusActive, label: 'Active', icon: 'server-2' },
		{ value: NSSKeyStatusExpired, label: 'Expired', icon: 'clock-cancel' },
		{ value: NSSKeyStatusRevoked, label: 'Revoked', icon: 'server-off' }
	];
	const windowOptions = [
		{ value: '7', label: '7 days' },
		{ value: '30', label: '30 days' },
		{ value: '90', label: '90 days' }
	];

	// The applied filters, as the address carries them. searchDraft is what
	// is in the box; searchQuery is the term the list was last asked for.
	let searchDraft = $state('');
	let searchQuery = $state('');
	let statusFilter = $state<NSSKeyStatus | ''>('');
	// Days, or 0 with the Expiring toggle off.
	let expiring = $state(0);
	let unused = $state(false);
	let alerts = $state(false);
	// The field's error: a term shaped like an address that is not one,
	// from the check here or the server's 400. Nothing is asked meanwhile.
	let fieldError = $state<string | null>(null);

	/** readFilters sets every filter from a query string; a missing or unknown value is the default. */
	function readFilters(params: ReadonlyURLSearchParams) {
		const q = params.get('q')?.trim() ?? '';
		searchDraft = q;
		const read = readKeySearch(q);
		searchQuery = read.kind === 'invalid' ? '' : q;
		fieldError = read.kind === 'invalid' ? read.message : null;
		const status = params.get('status') ?? '';
		statusFilter = statusOptions.some((option) => option.value === status)
			? (status as NSSKeyStatus | '')
			: '';
		const days = params.get('expiring') ?? '';
		expiring = windowOptions.some((option) => option.value === days) ? Number(days) : 0;
		unused = params.get('unused') === '1';
		alerts = params.get('alerts') === '1';
	}
	readFilters(page.url.searchParams);

	// The filters as a query string, defaults left out: what the address
	// carries and what a row's page returns to through its back chip.
	const filterSearch = $derived.by(() => {
		const parts: string[] = [];
		const add = (key: string, value: string) => parts.push(`${key}=${encodeURIComponent(value)}`);
		if (searchQuery) add('q', searchQuery);
		if (statusFilter) add('status', statusFilter);
		if (expiring) add('expiring', String(expiring));
		if (unused) add('unused', '1');
		if (alerts) add('alerts', '1');
		return parts.length ? `?${parts.join('&')}` : '';
	});

	// Which load may write to the page; see the certificate list.
	let latestLoad = 0;

	/** load reads the first page under the filters or, given a cursor, appends the next. */
	async function load(after?: string) {
		const current = ++latestLoad;
		const query = searchQuery;
		isLoading = true;
		moreError = null;
		if (!after) loadError = null;
		try {
			const result = await listAdminNSSKeys({
				q: query || undefined,
				status: statusFilter || undefined,
				expiringWithin: expiring || undefined,
				unused,
				alerts,
				after,
				limit: 50
			});
			if (current !== latestLoad) return;
			rows = after ? [...rows, ...result.keys] : result.keys;
			total = result.total;
			nextCursor = result.next_cursor ?? null;
			addressSearch = result.address_search ?? '';
			retentionSeconds = result.usage_retention_seconds;
			hasLoaded = true;
		} catch (cause) {
			if (current !== latestLoad || redirectIfUnauthenticated(cause)) return;
			if (cause instanceof ApiError && cause.isNotFound && !after) {
				nssGone = true;
			} else if (cause instanceof ApiError && cause.status === 400 && !after && query) {
				// The server refused the term the check here let through:
				// the field says why, as it would have, and the list is read
				// again without it rather than left showing another search.
				fieldError = cause.message;
				searchQuery = '';
			} else if (after) {
				moreError = errorMessage(cause);
			} else {
				loadError = errorMessage(cause);
			}
			hasLoaded = true;
		} finally {
			if (current === latestLoad) isLoading = false;
		}
	}

	// The only first-page loader: it reads every filter, so it re-runs on
	// each change. Nothing is asked of a server that does not serve NSS. A
	// term in the field that is not an address never becomes searchQuery,
	// so it is never sent; the list stays as the last valid search left it.
	$effect(() => {
		void [searchQuery, statusFilter, expiring, unused, alerts];
		if (nssOff) return;
		void load();
	});

	syncAddressSearch(() => filterSearch, readFilters);

	// Typing settles into a search, as every list's box does; a term shaped
	// like an address waits until it parses, and is called out only on
	// Enter or leaving the field, so a half-typed address is not an error.
	let settleTimer: ReturnType<typeof setTimeout> | undefined;
	$effect(() => () => clearTimeout(settleTimer));

	/** apply searches for the box's term when it reads; strict also calls out one that does not. */
	function apply(strict: boolean) {
		clearTimeout(settleTimer);
		const read = readKeySearch(searchDraft);
		if (read.kind === 'invalid') {
			if (strict) fieldError = read.message;
			return;
		}
		fieldError = null;
		searchQuery = searchDraft.trim();
	}

	function onInput() {
		clearTimeout(settleTimer);
		settleTimer = setTimeout(() => apply(false), 300);
	}

	function clearSearch() {
		searchDraft = '';
		apply(true);
		searchInput?.focus();
	}

	function clearFilters() {
		searchDraft = '';
		fieldError = null;
		searchQuery = '';
		statusFilter = '';
		expiring = 0;
		unused = false;
		alerts = false;
	}

	function retry() {
		void load();
	}

	async function loadMore() {
		if (nextCursor) await load(nextCursor);
	}

	const searchKind = $derived(readKeySearch(searchQuery).kind);
	const addressMode = $derived(searchKind === 'address');
	const otherFilters = $derived(statusFilter !== '' || expiring !== 0 || unused || alerts);
	const filtered = $derived(searchQuery !== '' || otherFilters);
	const windowPhrase = $derived(usageWindowPhrase(retentionSeconds));
	// The address line waits for the server's reading of the term it asked
	// for, so it never names a range the list does not yet show.
	const shownAddress = $derived(addressMode && !isLoading && hasLoaded ? addressSearch : '');

	/** rowHref opens one key on its own page, carrying the way back here. */
	function rowHref(id: string): string {
		const back = filterSearch ? `&back=${encodeURIComponent(filterSearch)}` : '';
		return `${resolve('/nss-keys/[id]', { id })}?from=admin${back}`;
	}

	/** secretWords names which secret a host presented. */
	function secretWords(secret: NSSSecretGeneration): string {
		if (secret === NSSSecretGenerationCurrent) return 'current secret';
		if (secret === NSSSecretGenerationPrevious) return 'previous secret';
		return 'an older secret';
	}

	/** expiryClass colours an expiry by how close it is, as the key list does. */
	function expiryClass(key: NSSAdminKey): string {
		if (key.status === NSSKeyStatusRevoked) return 'text-ink-muted';
		const tone = expiryTone(key.expires_at, now);
		return tone === 'past' ? 'text-danger' : tone === 'soon' ? 'text-trimmed' : 'text-ink';
	}

	const listStatus = $derived(
		describeList({ noun: 'key', total, loading: isLoading, ready: hasLoaded, query: searchQuery })
	);

	const ids = $props.id();
	const inputId = `${ids}-search`;
	const hintId = `${ids}-hint`;
	const errorId = `${ids}-error`;
	let searchInput = $state<HTMLInputElement>();
</script>

{#if nssOff}
	<Alert variant="info" title="The NSS directory is off" testid="nss-admin-keys-off">
		<code>nss.enabled</code> is false, so there are no fleet keys and no host looks anything up here.
	</Alert>
{:else}
	<form
		role="search"
		aria-label="Search and filter the NSS keys"
		class="flex flex-col gap-4"
		onsubmit={(event) => {
			event.preventDefault();
			apply(true);
		}}
		data-testid="nss-admin-keys-filters"
	>
		<div class="flex max-w-2xl flex-col gap-1">
			<label for={inputId} class="text-xs font-semibold text-ink-muted">Search keys</label>
			<div class="relative flex w-full items-center">
				<span class="pointer-events-none absolute left-3 text-ink-muted">
					<Icon name="search" size="sm" />
				</span>
				<!-- Plain text with no inputmode: an IPv6 address needs colons
				     and letters. Mono, because a reader compares an address
				     character by character. -->
				<input
					bind:this={searchInput}
					id={inputId}
					type="text"
					autocomplete="off"
					spellcheck="false"
					bind:value={searchDraft}
					oninput={onInput}
					onblur={() => apply(true)}
					aria-invalid={fieldError !== null}
					aria-describedby={fieldError ? `${errorId} ${hintId}` : hintId}
					data-testid="nss-admin-keys-search"
					class="min-h-target w-full rounded-md border border-border-control bg-surface py-2 pr-target pl-9 font-mono text-sm text-ink placeholder:text-ink-muted focus:border-accent"
				/>
				{#if searchDraft !== ''}
					<button
						type="button"
						aria-label="Clear search"
						onclick={clearSearch}
						data-testid="nss-admin-keys-search-clear"
						class="absolute inset-y-0 right-0 flex w-target items-center justify-center rounded-md text-ink-muted transition hover:bg-surface-muted hover:text-ink"
					>
						<Icon name="x" size="sm" />
					</button>
				{/if}
			</div>
			{#if fieldError}
				<p id={errorId} class="text-xs text-danger" data-testid="nss-admin-keys-search-error">
					{fieldError} The list below is not searched by it.
				</p>
			{/if}
			<p id={hintId} class="text-xs text-ink-muted">
				A name, group, creator or key id, or an address (<code class="font-mono">10.0.4.7</code>) or
				range (<code class="font-mono">10.0.4.0/24</code>).
			</p>
			{#if shownAddress && !fieldError}
				<p class="text-xs text-ink" data-testid="nss-admin-keys-searching">
					Keys used from <code class="font-mono">{shownAddress}</code>
					{windowPhrase}.
				</p>
			{/if}
		</div>

		<div class="flex flex-wrap items-end gap-x-6 gap-y-3 sm:items-center">
			<FilterGroup
				label="Status"
				options={statusOptions}
				selected={statusFilter}
				onselect={(value) => (statusFilter = value as NSSKeyStatus | '')}
				testid="nss-admin-keys-status"
			/>
			<div class="flex flex-wrap items-center gap-2" role="group" aria-label="Needs attention">
				<span class="text-xs font-semibold text-ink-muted">Needs attention:</span>
				<FilterChip
					label="Expiring"
					icon="clock-exclamation"
					selected={expiring !== 0}
					onclick={() => (expiring = expiring ? 0 : 30)}
					testid="nss-admin-keys-expiring"
				/>
				<FilterChip
					label="Unused 30 days"
					icon="search-off"
					selected={unused}
					onclick={() => (unused = !unused)}
					testid="nss-admin-keys-unused"
				/>
				<FilterChip
					label="Alerts 7 days"
					icon="alert-triangle"
					selected={alerts}
					onclick={() => (alerts = !alerts)}
					testid="nss-admin-keys-alerts"
				/>
			</div>
			{#if expiring}
				<FilterGroup
					label="Expiring within"
					options={windowOptions}
					selected={String(expiring)}
					onselect={(value) => (expiring = Number(value))}
					testid="nss-admin-keys-window"
				/>
			{/if}
			{#if filtered || fieldError}
				<Button variant="ghost" onclick={clearFilters} testid="nss-admin-keys-clear">
					Clear filters
				</Button>
			{/if}
		</div>
	</form>

	<ListStatus message={listStatus} />

	{#if loadError}
		<Alert
			variant="error"
			title="Could not load the NSS keys"
			onretry={retry}
			testid="nss-admin-keys-error">{loadError}</Alert
		>
	{:else if !hasLoaded || (isLoading && rows.length === 0)}
		<LoadingBlock shape="table" count={6} testid="nss-admin-keys-loading" />
	{:else if rows.length === 0 && addressMode && addressSearch}
		<EmptyState
			icon="filter-off"
			title={otherFilters
				? `No key used from ${addressSearch} matches these filters`
				: `No key was used from ${addressSearch}`}
			testid="nss-admin-keys-empty-address"
		>
			{#snippet action()}
				<Button variant="ghost" onclick={clearFilters}>Clear filters</Button>
			{/snippet}
			No key was used from <code class="font-mono">{addressSearch}</code>
			{windowPhrase}{retentionSeconds > 0 ? ', the usage the server keeps' : ''}. The search matches
			the address the server observed for each lookup: for a host behind a reverse proxy listed in
			<code class="font-mono">http.trusted_proxies</code>, the forwarded address, otherwise the
			connection's.
		</EmptyState>
	{:else if rows.length === 0 && filtered}
		<EmptyState
			icon="filter-off"
			title={searchQuery ? `No keys match “${searchQuery}”` : 'No keys match these filters'}
			testid="nss-admin-keys-empty-filtered"
		>
			{#snippet action()}
				<Button variant="ghost" onclick={clearFilters}>Clear filters</Button>
			{/snippet}
			The search covers each key's name and group and its creator's username and email.
		</EmptyState>
	{:else if rows.length === 0}
		<EmptyState icon="server-2" title="No NSS keys yet" testid="nss-admin-keys-empty">
			A host needs a fleet key to look up accounts and groups here: each fleet of hosts shares one,
			stored in <code class="font-mono">/etc/ssoossh/nss.key</code>.
			{#if session.user?.is_nss_key_admin}
				Create one with New key.
			{:else}
				A fleet key admin creates keys.
			{/if}
		</EmptyState>
	{:else}
		<!-- relative: the sr-only text inside is absolutely placed. -->
		<div class="relative overflow-x-auto rounded-lg bg-surface">
			<table class="data-table" data-testid="nss-admin-keys-table">
				<thead>
					<tr>
						<th scope="col">Name</th>
						<th scope="col">Creator</th>
						<th scope="col">Status</th>
						{#if addressMode}
							<th scope="col">Used from</th>
						{:else}
							<th scope="col" class="text-right">Hosts, 7 days</th>
						{/if}
						<th scope="col">Last seen</th>
					</tr>
				</thead>
				<tbody>
					{#each rows as key (key.id)}
						{@const status = keyStatus(key, now)}
						<tr data-testid="nss-admin-keys-row">
							<td>
								<a
									href={rowHref(key.id)}
									class="inline-flex min-h-target items-center font-mono font-medium text-accent hover:underline"
									data-testid="nss-admin-keys-link">{key.name}</a
								>
								<div class="font-mono text-xs text-ink-muted">
									{key.group_name || 'no group'}
								</div>
							</td>
							<td data-testid="nss-admin-keys-creator">
								{#if key.creator.exists}
									<div>{key.creator.username}</div>
									<div class="text-xs text-ink-muted">{key.creator.email}</div>
								{:else}
									<div class="text-ink-muted">unknown user</div>
									<div class="font-mono text-xs text-ink-muted">{key.creator.user_id}</div>
								{/if}
							</td>
							<td class="whitespace-nowrap">
								<KeyStatusBadge {status} testid="nss-admin-keys-status-badge" />
								<div class="mt-1 text-xs {expiryClass(key)}" data-testid="nss-admin-keys-expiry">
									{#if key.status === NSSKeyStatusRevoked}
										revoked{key.revoked_at ? ` ${formatDateTime(key.revoked_at)}` : ''}
									{:else if key.expires_at}
										<span class="inline-flex items-center gap-1">
											{#if expiryTone(key.expires_at, now) === 'soon'}
												<Icon name="alert-triangle" size="xs" />
											{/if}
											{remainingLabel(key.expires_at, now)}
										</span>
										<div class="text-ink-muted">{formatDateTime(key.expires_at)}</div>
									{:else}
										never expires
									{/if}
								</div>
								{#if key.key_source_alert_at || key.miss_rate_alert_at}
									<div
										class="mt-1 inline-flex items-center gap-1 text-xs text-trimmed"
										data-testid="nss-admin-keys-alert"
									>
										<Icon name="alert-triangle" size="xs" />
										{[
											key.key_source_alert_at ? 'Key source alert' : '',
											key.miss_rate_alert_at ? 'Miss rate alert' : ''
										]
											.filter(Boolean)
											.join(', ')}
									</div>
								{/if}
							</td>
							{#if addressMode}
								<td data-testid="nss-admin-keys-used-from">
									{#each key.matched?.addresses ?? [] as use (use.source_ip)}
										<div class="py-0.5">
											<span class="font-mono">{use.source_ip}</span>
											<span class="text-xs text-ink-muted">
												{relativeTime(use.last_seen, now)}, {secretWords(use.secret)}
											</span>
										</div>
									{/each}
									{#if key.matched && key.matched.total > key.matched.addresses.length}
										<div class="text-xs text-ink-muted" data-testid="nss-admin-keys-more-addresses">
											and {key.matched.total - key.matched.addresses.length} more
										</div>
									{/if}
								</td>
							{:else}
								<td class="text-right tabular-nums" data-testid="nss-admin-keys-hosts">
									{key.hosts_7d}
								</td>
							{/if}
							<td class="whitespace-nowrap" data-testid="nss-admin-keys-last-seen">
								{#if key.last_seen_at}
									<span class="text-ink">{relativeTime(key.last_seen_at, now)}</span>
									<div class="text-xs text-ink-muted">{formatDateTime(key.last_seen_at)}</div>
								{:else}
									<span class="text-ink-muted">Never</span>
								{/if}
							</td>
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
			errorTitle="Could not load more NSS keys"
			onmore={loadMore}
			testid="nss-admin-keys"
		/>
	{/if}
{/if}
