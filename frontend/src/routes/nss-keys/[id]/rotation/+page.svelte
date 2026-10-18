<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { ApiError } from '#lib/api/client.js';
	import { getNSSRotationReport, nssRotationReportCSVURL } from '#lib/api/endpoints.js';
	import {
		NSSRotationBucketGone,
		NSSRotationBucketNotSwitched,
		NSSRotationBucketSwitched,
		type NSSRotationBucket
	} from '#lib/api/generated/enums.js';
	import type { NSSRotationReport, NSSRotationReportRow } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { formatDateTime } from '#lib/format.js';
	import { groupRotationRows } from '#lib/nss.js';

	// The page an operator opens after rotating a key: which hosts have
	// switched to the new secret, and which would break if the old one ended
	// now. The headline is the second number, "12 of 140 hosts still on the
	// old secret", because it is the one that decides whether to wait.
	// Not switched comes first for the same reason; gone (not seen since
	// the rotation) last, since those hosts may simply be off.

	const id = $derived(page.params.id ?? '');

	let report = $state<NSSRotationReport | null>(null);
	let loadError = $state<string | null>(null);
	let notFound = $state(false);
	let hasLoaded = $state(false);
	let attempt = $state(0);

	// One address filter across the three groups, and one sort.
	let addressFilter = $state('');
	let sort = $state<'last_seen' | 'address' | 'lookups'>('last_seen');

	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
		const keyId = id;
		const controller = new AbortController();
		notFound = false;
		getNSSRotationReport(keyId, controller.signal)
			.then((result) => {
				report = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				notFound = cause instanceof ApiError && cause.isNotFound;
				loadError = notFound ? null : errorMessage(cause);
				hasLoaded = true;
			});
		return () => controller.abort();
	});

	const totalHosts = $derived(report ? report.switched + report.not_switched + report.gone : 0);

	/** visible narrows and orders one group's rows. */
	function visible(rows: NSSRotationReportRow[]): NSSRotationReportRow[] {
		const needle = addressFilter.trim();
		return rows
			.filter((row) => needle === '' || row.source_ip.includes(needle))
			.sort((a, b) => {
				const byAddress = a.source_ip.localeCompare(b.source_ip, undefined, { numeric: true });
				if (sort === 'address') return byAddress;
				const primary =
					sort === 'lookups'
						? b.lookups - a.lookups
						: new Date(b.last_seen).getTime() - new Date(a.last_seen).getTime();
				// Ties read in address order, so 10.0.0.2 sits before 10.0.0.10.
				return primary || byAddress;
			});
	}

	const groups = $derived(report ? groupRotationRows(report.rows) : null);

	const sections: { bucket: NSSRotationBucket; title: string; description: string }[] = [
		{
			bucket: NSSRotationBucketNotSwitched,
			title: 'Not switched',
			description:
				'Last presented an older secret. These stop resolving accounts when the previous secret ends.'
		},
		{
			bucket: NSSRotationBucketSwitched,
			title: 'Switched',
			description: 'Last presented the current secret.'
		},
		{
			bucket: NSSRotationBucketGone,
			title: 'Gone',
			description: 'Not seen since the rotation. They may be off, or no longer use this key.'
		}
	];

	const shownCount = $derived(
		groups
			? visible(groups[NSSRotationBucketNotSwitched]).length +
					visible(groups[NSSRotationBucketSwitched]).length +
					visible(groups[NSSRotationBucketGone]).length
			: 0
	);
	const listMessage = $derived(
		addressFilter.trim() && groups
			? `${shownCount} ${shownCount === 1 ? 'host' : 'hosts'} match.`
			: ''
	);
</script>

<svelte:head><title>Rotation report · {id} · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading
		title="Rotation report"
		testid="rotation-heading"
		back={{
			href: resolve('/nss-keys/[id]', { id: id }),
			label: `Key ${id}`,
			testid: 'rotation-back'
		}}
	>
		{#snippet sub()}
			Which hosts have switched to this key's current secret since it was last rotated.
		{/snippet}
		{#snippet action()}
			{#if report?.rotated_at}
				<!-- A plain link: the answer is a file download, not a page. -->
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a
					href={nssRotationReportCSVURL(id)}
					download
					data-testid="rotation-csv"
					class="inline-flex min-h-target items-center gap-2 rounded-md border border-border-control px-4 py-2 text-sm font-medium text-ink transition hover:bg-surface-muted"
				>
					<Icon name="download" size="sm" />
					Download CSV
				</a>
			{/if}
		{/snippet}
	</PageHeading>

	<ListStatus message={listMessage} testid="rotation-status" />

	{#if notFound}
		<Alert variant="error" title="No such NSS key" testid="rotation-missing">
			No key with this id is shared with you.
		</Alert>
	{:else if loadError}
		<Alert
			variant="error"
			title="Could not load the rotation report"
			onretry={retry}
			testid="rotation-error">{loadError}</Alert
		>
	{:else if !hasLoaded || !report}
		<LoadingBlock
			shape="lines"
			count={5}
			label="Loading the rotation report…"
			testid="rotation-loading"
		/>
	{:else if !report.rotated_at}
		<EmptyState icon="rotate" title="No rotation yet" testid="rotation-empty">
			This key has never been rotated. Rotate it from its page, then come back here to see which
			hosts have switched to the new secret.
			{#snippet action()}
				<a
					href={resolve('/nss-keys/[id]', { id: id })}
					class="inline-flex min-h-target items-center text-sm text-accent hover:underline"
					>Open the key</a
				>
			{/snippet}
		</EmptyState>
	{:else}
		<section class="flex flex-col gap-4 rounded-lg bg-surface p-5" data-testid="rotation-summary">
			<p class="text-lg font-semibold text-ink" data-testid="rotation-headline">
				{report.not_switched} of {totalHosts}
				{totalHosts === 1 ? 'host' : 'hosts'} still on the old secret
			</p>
			<p class="text-dense text-ink-muted" data-testid="rotation-grace">
				Rotated {formatDateTime(report.rotated_at)} to generation {report.current_generation}.
				{#if report.previous_expires_at && report.grace_days_left > 0}
					The previous secret is accepted for {report.grace_days_left}
					more {report.grace_days_left === 1 ? 'day' : 'days'}, until {formatDateTime(
						report.previous_expires_at
					)}.
				{:else}
					The previous secret is no longer accepted.
				{/if}
			</p>
			<dl class="grid grid-cols-3 gap-4 sm:max-w-md">
				<div>
					<dt class="text-xs font-semibold text-ink-muted">Not switched</dt>
					<dd
						class="text-lg font-semibold tabular-nums {report.not_switched > 0
							? 'text-trimmed'
							: 'text-ink'}"
						data-testid="rotation-count-not_switched"
					>
						{report.not_switched}
					</dd>
				</div>
				<div>
					<dt class="text-xs font-semibold text-ink-muted">Switched</dt>
					<dd
						class="text-lg font-semibold text-ink tabular-nums"
						data-testid="rotation-count-switched"
					>
						{report.switched}
					</dd>
				</div>
				<div>
					<dt class="text-xs font-semibold text-ink-muted">Gone</dt>
					<dd class="text-lg font-semibold text-ink tabular-nums" data-testid="rotation-count-gone">
						{report.gone}
					</dd>
				</div>
			</dl>
		</section>

		{#if totalHosts > 0}
			<div class="flex flex-wrap items-end gap-x-6 gap-y-3">
				<label class="flex flex-col gap-1">
					<span class="text-xs font-semibold text-ink-muted">Source address</span>
					<input
						bind:value={addressFilter}
						type="search"
						inputmode="decimal"
						autocomplete="off"
						data-testid="rotation-filter"
						class="min-h-target w-64 max-w-full rounded-md border border-border-control bg-surface px-3 font-mono text-dense text-ink"
					/>
				</label>
				<label class="flex flex-col gap-1">
					<span class="text-xs font-semibold text-ink-muted">Sort by</span>
					<select
						bind:value={sort}
						data-testid="rotation-sort"
						class="min-h-target rounded-md border border-border-control bg-surface px-3 text-dense text-ink hover:bg-surface-muted"
					>
						<option value="last_seen">Last seen, newest first</option>
						<option value="address">Source address</option>
						<option value="lookups">Lookups, most first</option>
					</select>
				</label>
			</div>
		{/if}

		{#if groups}
			{#each sections as section (section.bucket)}
				{@const rows = visible(groups[section.bucket])}
				<PageSection
					title="{section.title} ({groups[section.bucket].length})"
					description={section.description}
					testid="rotation-{section.bucket}"
				>
					{#if groups[section.bucket].length === 0}
						<p class="text-dense text-ink-muted" data-testid="rotation-{section.bucket}-none">
							None.
						</p>
					{:else if rows.length === 0}
						<p class="text-dense text-ink-muted">No address here matches the filter.</p>
					{:else}
						<div class="overflow-x-auto">
							<table class="data-table">
								<thead>
									<tr>
										<th scope="col">Source address</th>
										<th scope="col">First seen</th>
										<th scope="col">Last seen</th>
										<th scope="col" class="text-right">Lookups</th>
										<th scope="col">Last secret</th>
									</tr>
								</thead>
								<tbody>
									{#each rows as row (row.source_ip)}
										<tr data-testid="rotation-row">
											<td class="font-mono">{row.source_ip}</td>
											<td class="whitespace-nowrap text-ink-muted"
												>{formatDateTime(row.first_seen)}</td
											>
											<td class="whitespace-nowrap">{formatDateTime(row.last_seen)}</td>
											<td class="text-right">{row.lookups.toLocaleString()}</td>
											<td class="whitespace-nowrap">Generation {row.last_generation}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					{/if}
				</PageSection>
			{/each}
		{/if}
	{/if}
</PageShell>
