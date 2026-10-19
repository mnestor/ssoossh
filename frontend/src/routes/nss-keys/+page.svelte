<script lang="ts">
	import { resolve } from '$app/paths';
	import { page, type ReadonlyURLSearchParams } from '$app/state';

	import { ApiError } from '#lib/api/client.js';
	import { listNSSKeys } from '#lib/api/endpoints.js';
	import {
		NSSKeyStatusActive,
		NSSKeyStatusExpired,
		NSSKeyStatusRevoked
	} from '#lib/api/generated/enums.js';
	import type { NSSKey, NSSKeyListResponse } from '#lib/api/types.js';
	import { syncAddressSearch } from '#lib/addressSearch.svelte.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import FilterGroup from '#lib/components/FilterGroup.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import KeyStatusBadge from '#lib/components/KeyStatusBadge.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { formatDateTime, relativeTime, remainingLabel } from '#lib/format.js';
	import { expiryTone, KeyRotating, keyStatus, type KeyDisplayStatus } from '#lib/nss.js';
	import { session } from '#lib/session.svelte.js';

	// The fleet keys the caller manages: the server lists the keys whose
	// group is one of the caller's groups, for every caller, the admin
	// roles included. Every key, a key with no group too, is on the admin
	// list at /admin/nss/keys. The list is not paged (one person's keys
	// are few), so the filters and the sort run here.
	//
	// The one question a reader brings is "which key, and is it about to
	// stop working?", so the key's name leads each row and the expiry is the
	// only coloured cell: amber within 30 days, red once past.

	let list = $state<NSSKeyListResponse | null>(null);
	let loadError = $state<string | null>(null);
	let disabled = $state(false);
	let hasLoaded = $state(false);
	// Bumped by Try again; the load below reads it, so a bump re-runs it.
	let attempt = $state(0);

	// Expiry and rotation are read against a clock that moves.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 60_000);
		return () => clearInterval(timer);
	});

	// The status filter's values are the display statuses; '' is any.
	let statusFilter = $state('');
	// '' is any group; NO_GROUP is a key with none.
	const NO_GROUP = '\u0000none';
	let groupFilter = $state('');
	let sort = $state<'name' | 'expiry'>('name');

	const statusOptions = [
		{ value: '', label: 'All', icon: 'filter-off' },
		{ value: NSSKeyStatusActive, label: 'Active', icon: 'server-2' },
		{ value: KeyRotating, label: 'Rotating', icon: 'rotate' },
		{ value: NSSKeyStatusExpired, label: 'Expired', icon: 'clock-cancel' },
		{ value: NSSKeyStatusRevoked, label: 'Revoked', icon: 'server-off' }
	];

	// The filters and the sort live in the address too, so a reader who
	// opens a key and comes back by its back chip finds the same view. An
	// unknown value is dropped, not refused.
	/** readFilters sets the filters and the sort from a query string; a missing key is the default. */
	function readFilters(params: ReadonlyURLSearchParams) {
		const wanted = params.get('status') ?? '';
		statusFilter = statusOptions.some((option) => option.value === wanted) ? wanted : '';
		groupFilter = params.has('no_group') ? NO_GROUP : (params.get('group') ?? '');
		sort = params.get('sort') === 'expiry' ? 'expiry' : 'name';
	}
	readFilters(page.url.searchParams);

	// The query string the filters stand for, '' at the defaults.
	const filterSearch = $derived.by(() => {
		const parts: string[] = [];
		const add = (key: string, value: string) => parts.push(`${key}=${encodeURIComponent(value)}`);
		if (statusFilter) add('status', statusFilter);
		if (groupFilter === NO_GROUP) add('no_group', '1');
		else if (groupFilter) add('group', groupFilter);
		if (sort !== 'name') add('sort', sort);
		return parts.length ? `?${parts.join('&')}` : '';
	});

	syncAddressSearch(() => filterSearch, readFilters);

	/** keyHref opens one key, carrying the filters for its back chip. */
	function keyHref(id: string): string {
		const back = filterSearch ? `?back=${encodeURIComponent(filterSearch)}` : '';
		return `${resolve('/nss-keys/[id]', { id })}${back}`;
	}

	const keys = $derived(list?.keys ?? []);

	const groups = $derived(
		[...new Set(keys.map((key) => key.group_name).filter((name) => name !== ''))].sort()
	);
	const anyUngrouped = $derived(keys.some((key) => key.group_name === ''));

	const filtered = $derived(
		keys
			.filter((key) => statusFilter === '' || keyStatus(key, now) === statusFilter)
			.filter(
				(key) =>
					groupFilter === '' ||
					(groupFilter === NO_GROUP ? key.group_name === '' : key.group_name === groupFilter)
			)
			.sort((a, b) => (sort === 'expiry' ? compareExpiry(a, b) : a.name.localeCompare(b.name)))
	);

	const filtering = $derived(statusFilter !== '' || groupFilter !== '');

	// Announced once a filter has narrowed the list, not on first paint.
	const listMessage = $derived(
		hasLoaded && list && filtering
			? `${filtered.length} ${filtered.length === 1 ? 'key' : 'keys'} shown.`
			: ''
	);

	function clearFilters() {
		statusFilter = '';
		groupFilter = '';
	}

	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
		const controller = new AbortController();
		listNSSKeys(controller.signal)
			.then((result) => {
				list = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				// A 404 here is nss_disabled: the setting changed after the
				// session was read.
				disabled = cause instanceof ApiError && cause.isNotFound;
				loadError = disabled ? null : errorMessage(cause);
				hasLoaded = true;
			});
		return () => controller.abort();
	});

	/** expiryTime is when a key expires, Infinity for one that never does. */
	function expiryTime(key: NSSKey): number {
		return key.expires_at ? new Date(key.expires_at).getTime() : Number.POSITIVE_INFINITY;
	}

	/**
	 * compareExpiry orders keys soonest expiry first, a key that never
	 * expires after those that do. A revoked key's expiry no longer means
	 * anything, so revoked keys come after every live one rather than
	 * among the keys about to stop working, by name among themselves.
	 */
	function compareExpiry(a: NSSKey, b: NSSKey): number {
		const aRevoked = a.status === NSSKeyStatusRevoked;
		const bRevoked = b.status === NSSKeyStatusRevoked;
		if (aRevoked !== bRevoked) return aRevoked ? 1 : -1;
		if (aRevoked) return a.name.localeCompare(b.name);
		const at = expiryTime(a);
		const bt = expiryTime(b);
		if (at === bt) return a.name.localeCompare(b.name);
		return at < bt ? -1 : 1;
	}

	/** toneClass colours an expiry by how close it is; a revoked key's expiry no longer matters. */
	function toneClass(key: NSSKey, status: KeyDisplayStatus): string {
		if (status === NSSKeyStatusRevoked) return 'text-ink-muted';
		const tone = expiryTone(key.expires_at, now);
		return tone === 'past' ? 'text-danger' : tone === 'soon' ? 'text-trimmed' : 'text-ink';
	}
</script>

<svelte:head><title>NSS keys · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="NSS keys" testid="nss-keys-heading">
		{#snippet sub()}
			The keys your groups manage. Each fleet of hosts shares one and presents it to look up
			accounts and groups.
		{/snippet}
		{#snippet action()}
			<!-- While the list is empty its empty state carries New key, so
			     the heading does not offer a second one. -->
			{#if list?.can_create && keys.length > 0}
				<a
					href={resolve('/nss-keys/new')}
					data-testid="nss-keys-new"
					class="inline-flex min-h-target items-center justify-center gap-2 rounded-md bg-accent px-4 py-2 text-sm font-medium text-accent-ink transition duration-100 hover:bg-accent-hover active:scale-98"
				>
					New key
				</a>
			{/if}
		{/snippet}
	</PageHeading>

	<ListStatus message={listMessage} testid="nss-keys-status" />

	{#if disabled}
		<Alert variant="info" title="The NSS directory is off" testid="nss-keys-disabled">
			<code class="font-mono">nss.enabled</code> is false on this server, so there are no fleet keys to
			manage.
		</Alert>
	{:else if loadError}
		<Alert
			variant="error"
			title="Could not load the NSS keys"
			onretry={retry}
			testid="nss-keys-error">{loadError}</Alert
		>
	{:else if !hasLoaded}
		<LoadingBlock shape="table" count={4} label="Loading the NSS keys…" testid="nss-keys-loading" />
	{:else if list && keys.length === 0}
		<!-- One empty state for every caller: the list is the keys their
		     groups manage, whatever their roles, so a role changes only
		     where they go next. -->
		<EmptyState icon="server-2" title="No NSS keys in your groups" testid="nss-keys-empty">
			A key is listed here once its group is one you are in.
			<!-- Auditors and above, and the fleet key admin role, read every
			     key on All NSS keys. -->
			{#if session.user?.is_auditor || session.user?.is_nss_key_admin}
				<span data-testid="nss-keys-empty-admin">
					Every key in the fleet, a key with no group included, is on
					<a href={resolve('/admin/nss/keys')} class="text-accent hover:underline">All NSS keys</a>.
				</span>
			{:else if list.can_create}
				Create one key for each fleet of hosts. Every host in the fleet stores it in
				<code class="font-mono">/etc/ssoossh/nss.key</code> and presents it on each lookup.
			{:else}
				An administrator assigns a key to a group.
			{/if}
			{#snippet action()}
				{#if list?.can_create}
					<a
						href={resolve('/nss-keys/new')}
						class="inline-flex min-h-target items-center rounded-md bg-accent px-4 py-2 text-sm font-medium text-accent-ink hover:bg-accent-hover"
						>New key</a
					>
				{/if}
			{/snippet}
		</EmptyState>
	{:else if list}
		<div class="flex flex-col gap-3">
			<div class="flex flex-wrap items-end gap-x-6 gap-y-3 sm:items-center">
				<FilterGroup
					label="Status"
					options={statusOptions}
					selected={statusFilter}
					onselect={(value) => (statusFilter = value)}
					testid="nss-keys-status-filter"
				/>
				{#if groups.length > 0}
					<!-- Laid out like the status group beside it: the name inline from
					     `sm` up, above the field below it. -->
					<label class="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-2">
						<span class="text-xs font-semibold text-ink-muted"
							>Group<span class="max-sm:hidden">:</span></span
						>
						<select
							bind:value={groupFilter}
							data-testid="nss-keys-group-filter"
							class="min-h-target rounded-md border border-border-control bg-surface px-3 text-base text-ink hover:bg-surface-muted sm:text-dense"
						>
							<option value="">Any group</option>
							{#each groups as group (group)}
								<option value={group}>{group}</option>
							{/each}
							{#if anyUngrouped}
								<option value={NO_GROUP}>No group</option>
							{/if}
						</select>
					</label>
				{/if}
				{#if filtering}
					<Button variant="ghost" onclick={clearFilters} testid="nss-keys-clear">
						Clear filters
					</Button>
				{/if}
			</div>

			{#if filtered.length === 0}
				<EmptyState
					icon="filter-off"
					title="No keys match these filters"
					testid="nss-keys-filtered"
				>
					{#snippet action()}
						<Button variant="ghost" onclick={clearFilters}>Clear filters</Button>
					{/snippet}
				</EmptyState>
			{:else}
				<div class="overflow-x-auto rounded-lg bg-surface">
					<table class="data-table" data-testid="nss-keys-table">
						<thead>
							<tr>
								<th scope="col" aria-sort={sort === 'name' ? 'ascending' : 'none'}>
									<button
										type="button"
										onclick={() => (sort = 'name')}
										class="inline-flex min-h-target items-center gap-1 tracking-widest uppercase hover:text-ink"
										data-testid="nss-keys-sort-name"
									>
										Key
										{#if sort === 'name'}<Icon name="chevron-down" size="xs" />{/if}
									</button>
								</th>
								<th scope="col" class="max-sm:hidden">Description</th>
								<th scope="col" class="max-sm:hidden">Group</th>
								<th scope="col" aria-sort={sort === 'expiry' ? 'ascending' : 'none'}>
									<button
										type="button"
										onclick={() => (sort = 'expiry')}
										class="inline-flex min-h-target items-center gap-1 tracking-widest uppercase hover:text-ink"
										data-testid="nss-keys-sort-expiry"
									>
										Expires
										{#if sort === 'expiry'}<Icon name="chevron-down" size="xs" />{/if}
									</button>
								</th>
								<th scope="col" class="max-sm:hidden">Last used</th>
								<th scope="col" class="max-sm:hidden">Status</th>
							</tr>
						</thead>
						<tbody>
							{#each filtered as key (key.id)}
								{@const status = keyStatus(key, now)}
								<tr data-testid="nss-key-row">
									<td>
										<!-- keyHref is a resolve()'d route plus the back query; the rule cannot see through the call.
										     Block form because the report lands on the href line. -->
										<!-- eslint-disable svelte/no-navigation-without-resolve -->
										<a
											href={keyHref(key.id)}
											class="inline-flex min-h-target items-center font-medium text-accent hover:underline"
											data-testid="nss-key-link">{key.name}</a
										>
										<!-- eslint-enable svelte/no-navigation-without-resolve -->
										<div class="font-mono text-xs text-ink-muted">{key.id}</div>
										<!-- Below `sm` the status column folds in here, so the
										     table fits a phone without scrolling. -->
										<div class="mt-1 sm:hidden">
											<KeyStatusBadge {status} testid="nss-key-status-narrow" />
										</div>
									</td>
									<td
										class="max-w-xs truncate text-ink-muted max-sm:hidden"
										title={key.description}
									>
										{key.description || '—'}
									</td>
									<td class="max-sm:hidden">
										{#if key.group_name}
											<span class="font-mono">{key.group_name}</span>
										{:else}
											<span class="text-ink-muted">—</span>
										{/if}
									</td>
									<td class="whitespace-nowrap">
										<span
											class="inline-flex items-center gap-1 {toneClass(key, status)}"
											data-testid="nss-key-expiry"
										>
											{#if status === NSSKeyStatusRevoked}
												<!-- A revoked key's expiry no longer means anything. -->
												no longer used
											{:else if key.expires_at}
												{#if expiryTone(key.expires_at, now) === 'soon'}
													<Icon name="alert-triangle" size="xs" />
												{/if}
												{remainingLabel(key.expires_at, now)}
											{:else}
												never
											{/if}
										</span>
										{#if key.expires_at}
											<div class="text-xs text-ink-muted">{formatDateTime(key.expires_at)}</div>
										{/if}
									</td>
									<td class="whitespace-nowrap max-sm:hidden" data-testid="nss-key-last-used">
										{#if key.last_used_at}
											<span class="text-ink">{relativeTime(key.last_used_at, now)}</span>
											<div class="text-xs text-ink-muted">{formatDateTime(key.last_used_at)}</div>
										{:else}
											<span class="text-ink-muted">Never</span>
										{/if}
									</td>
									<td class="max-sm:hidden"><KeyStatusBadge {status} testid="nss-key-status" /></td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>
	{/if}
</PageShell>
