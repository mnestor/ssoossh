<script lang="ts">
	import { resolve } from '$app/paths';

	import { ApiError } from '#lib/api/client.js';
	import { listNSSKeys } from '#lib/api/endpoints.js';
	import {
		NSSKeyStatusActive,
		NSSKeyStatusExpired,
		NSSKeyStatusRevoked
	} from '#lib/api/generated/enums.js';
	import type { NSSKey, NSSKeyListResponse } from '#lib/api/types.js';
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
	import { formatDateTime, remainingLabel } from '#lib/format.js';
	import { expiryTone, KeyRotating, keyStatus, type KeyDisplayStatus } from '#lib/nss.js';

	// Every fleet key the caller may see. The server decides which: all of
	// them for the fleet key admin role and the admin area's roles, and for
	// anyone else the keys whose group they are in. The list is not paged
	// (keys are fleet-sized), so the filters and the sort run here.
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
			.sort((a, b) =>
				sort === 'expiry'
					? new Date(a.expires_at).getTime() - new Date(b.expires_at).getTime()
					: a.name.localeCompare(b.name)
			)
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
			The keys your hosts present to look up accounts and groups. Each fleet of hosts shares one.
		{/snippet}
		{#snippet action()}
			{#if list?.can_create}
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
		{#if list.can_create}
			<EmptyState icon="server-2" title="No NSS keys yet" testid="nss-keys-empty">
				Create one key for each fleet of hosts. Every host in the fleet stores it in
				<code class="font-mono">/etc/ssoossh/nss.key</code> and presents it on each lookup.
				{#snippet action()}
					<a
						href={resolve('/nss-keys/new')}
						class="inline-flex min-h-target items-center rounded-md bg-accent px-4 py-2 text-sm font-medium text-accent-ink hover:bg-accent-hover"
						>New key</a
					>
				{/snippet}
			</EmptyState>
		{:else}
			<EmptyState icon="server-2" title="No NSS keys are shared with you" testid="nss-keys-empty">
				A key appears here once an administrator assigns it to a group you are in.
			</EmptyState>
		{/if}
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
								<th scope="col" class="max-sm:hidden">Status</th>
							</tr>
						</thead>
						<tbody>
							{#each filtered as key (key.id)}
								{@const status = keyStatus(key, now)}
								<tr data-testid="nss-key-row">
									<td>
										<a
											href={resolve('/nss-keys/[id]', { id: key.id })}
											class="font-medium text-accent hover:underline"
											data-testid="nss-key-link">{key.name}</a
										>
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
											{:else}
												{#if expiryTone(key.expires_at, now) === 'soon'}
													<Icon name="alert-triangle" size="xs" />
												{/if}
												{remainingLabel(key.expires_at, now)}
											{/if}
										</span>
										<div class="text-xs text-ink-muted">{formatDateTime(key.expires_at)}</div>
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
