<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { ApiError } from '#lib/api/client.js';
	import { getNSSKey, getNSSRotationReport, nssRotationReportCSVURL } from '#lib/api/endpoints.js';
	import {
		NSSKeyStatusRevoked,
		NSSRotationBucketGone,
		NSSRotationBucketNotSwitched,
		NSSRotationBucketSwitched,
		type NSSRotationBucket
	} from '#lib/api/generated/enums.js';
	import type { NSSKey, NSSRotationReport, NSSRotationReportRow } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
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
	//
	// The report names a key only by id, and an operator rotating several
	// fleets keeps several of these open, so the key itself is read beside
	// it for its name and whether it is revoked. That read is best effort:
	// without it the page falls back to the id and the report still stands.
	//
	// Usage is written once a minute and the page exists to watch a count
	// fall, so it refreshes in place and says when it was read.

	const id = $derived(page.params.id ?? '');

	let report = $state<NSSRotationReport | null>(null);
	let key = $state<NSSKey | null>(null);
	let loadError = $state<string | null>(null);
	let notFound = $state(false);
	let hasLoaded = $state(false);
	let attempt = $state(0);
	// When the report on screen was read.
	let readAt = $state<Date | null>(null);
	let refreshing = $state(false);
	let refreshError = $state<string | null>(null);
	// What a screen reader hears when a refresh lands.
	let refreshMessage = $state('');
	// Which read may write to the page, so a slow first load cannot land
	// over a newer refresh. Not $state: nothing renders from it.
	let latestRead = 0;

	// One address filter across the three groups, and one sort.
	let addressFilter = $state('');
	let sort = $state<'last_seen' | 'address' | 'lookups'>('last_seen');

	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	/**
	 * read fetches the report and, beside it, the key. The key read never
	 * fails the page: a missing name falls back to the id.
	 */
	async function read(keyId: string, signal?: AbortSignal) {
		const [result, named] = await Promise.all([
			getNSSRotationReport(keyId, signal),
			getNSSKey(keyId, signal).catch(() => null)
		]);
		return { result, named };
	}

	$effect(() => {
		void attempt;
		const keyId = id;
		const controller = new AbortController();
		const current = ++latestRead;
		notFound = false;
		read(keyId, controller.signal)
			.then(({ result, named }) => {
				if (current !== latestRead) return;
				report = result;
				key = named;
				readAt = new Date();
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				if (current !== latestRead) return;
				notFound = cause instanceof ApiError && cause.isNotFound;
				loadError = notFound ? null : errorMessage(cause);
				hasLoaded = true;
			});
		return () => controller.abort();
	});

	/**
	 * refresh reads the report again in place. The counts and the filter
	 * stay on screen while it runs, and a failure keeps them, saying the
	 * figures are from the last good read.
	 */
	async function refresh() {
		const current = ++latestRead;
		refreshing = true;
		refreshError = null;
		refreshMessage = '';
		try {
			const { result, named } = await read(id);
			if (current !== latestRead) return;
			report = result;
			key = named ?? key;
			readAt = new Date();
			refreshMessage = `Report refreshed. ${result.not_switched} of ${hostCount(result)} hosts still on the old secret.`;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			if (current !== latestRead) return;
			refreshError = errorMessage(cause);
		} finally {
			if (current === latestRead) refreshing = false;
		}
	}

	// The CSV is a plain download, which the page cannot redirect: with an
	// expired session the browser would save the server's 401 body under a
	// .csv name. So the link's first press reads the report first; a 401
	// there goes to sign in, and only a read that worked lets the download
	// through. Not $state: nothing renders from it.
	let csvLink = $state<HTMLAnchorElement | undefined>(undefined);
	let csvChecked = false;
	let csvChecking = $state(false);
	let csvError = $state<string | null>(null);

	async function downloadCSV(event: MouseEvent) {
		if (csvChecked) {
			csvChecked = false;
			return;
		}
		event.preventDefault();
		if (csvChecking) return;
		csvChecking = true;
		csvError = null;
		try {
			await getNSSRotationReport(id);
			csvChecked = true;
			csvLink?.click();
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			csvError = errorMessage(cause);
		} finally {
			csvChecking = false;
		}
	}

	/** hostCount is every host the report has seen. */
	function hostCount(value: NSSRotationReport): number {
		return value.switched + value.not_switched + value.gone;
	}

	// The key's name for the chip, the title and the sub line, or its id
	// when the key read failed.
	const keyName = $derived(key?.name ?? '');
	const revoked = $derived(key?.status === NSSKeyStatusRevoked);
	// Once the previous secret has ended, hosts still on it are failing,
	// not waiting.
	const ended = $derived(!!report && (!report.previous_expires_at || report.grace_days_left === 0));

	const totalHosts = $derived(report ? hostCount(report) : 0);

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

	const sections = $derived<{ bucket: NSSRotationBucket; title: string; description: string }[]>([
		{
			bucket: NSSRotationBucketNotSwitched,
			title: 'Not switched',
			description: ended
				? 'Last presented an older secret, which is no longer accepted. These serve only what they had cached and resolve no new accounts until they have the current one.'
				: 'Last presented an older secret. When the previous secret ends, these serve only what they had cached and resolve no new accounts.'
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
	]);

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

<svelte:head><title>Rotation report · {keyName || id} · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading
		title="Rotation report"
		testid="rotation-heading"
		back={{
			href: resolve('/nss-keys/[id]', { id: id }),
			label: keyName || `Key ${id}`,
			testid: 'rotation-back'
		}}
	>
		{#snippet sub()}
			{#if keyName}
				Which hosts have switched to the current secret of
				<span class="font-mono text-ink" data-testid="rotation-key-name">{keyName}</span> since it was
				last rotated.
			{:else}
				Which hosts have switched to this key's current secret since it was last rotated.
			{/if}
		{/snippet}
		{#snippet action()}
			{#if report?.rotated_at}
				<div class="flex flex-wrap items-center gap-2">
					<Button variant="ghost" busy={refreshing} onclick={refresh} testid="rotation-refresh">
						{refreshing ? 'Refreshing…' : 'Refresh'}
					</Button>
					<!-- A plain link: the answer is a file download, not a page.
					     downloadCSV checks the session before letting it through. -->
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
					<a
						bind:this={csvLink}
						href={nssRotationReportCSVURL(id)}
						download
						onclick={downloadCSV}
						aria-busy={csvChecking}
						data-testid="rotation-csv"
						class="inline-flex min-h-target items-center gap-2 rounded-md border border-border-control px-4 py-2 text-sm font-medium text-ink transition hover:bg-surface-muted"
					>
						<Icon name="download" size="sm" />
						Download CSV
					</a>
				</div>
			{/if}
		{/snippet}
	</PageHeading>

	<ListStatus message={listMessage} testid="rotation-status" />
	<div aria-live="polite" aria-atomic="true" class="sr-only" data-testid="rotation-refresh-live">
		{refreshMessage}
	</div>

	{#if csvError}
		<Alert variant="error" title="Could not download the CSV" testid="rotation-csv-error">
			{csvError} Press Download CSV to try again.
		</Alert>
	{/if}

	{#if refreshError}
		<Alert variant="error" title="Could not refresh the report" testid="rotation-refresh-error">
			{refreshError} The figures below are from the last read. Press Refresh to try again.
		</Alert>
	{/if}

	{#if revoked}
		<Alert variant="info" title="This key is revoked" testid="rotation-revoked">
			Neither secret is accepted any more{#if key?.revoked_at}, since {formatDateTime(
					key.revoked_at
				)}{/if}. Hosts still presenting it resolve no directory accounts, whichever generation they
			are on.
		</Alert>
	{/if}

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
			{#if ended}
				<p
					class="text-lg font-semibold {report.not_switched > 0 ? 'text-danger' : 'text-ink'}"
					data-testid="rotation-headline"
				>
					{report.not_switched} of {totalHosts}
					{totalHosts === 1 ? 'host' : 'hosts'}
					{report.not_switched === 1 ? 'presents' : 'present'} a secret that is no longer accepted
				</p>
			{:else}
				<p class="text-lg font-semibold text-ink" data-testid="rotation-headline">
					{report.not_switched} of {totalHosts}
					{totalHosts === 1 ? 'host' : 'hosts'} still on the old secret
				</p>
			{/if}
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
				{#if readAt}
					<span data-testid="rotation-read-at">As of {formatDateTime(readAt.toISOString())}.</span>
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
