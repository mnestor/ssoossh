<script lang="ts">
	import { resolve } from '$app/paths';

	import { ApiError } from '#lib/api/client.js';
	import {
		acceptNSSGroupIDChange,
		acceptNSSUserIDChange,
		getNSSCollisions,
		getNSSPendingIDChanges,
		purgeNSSGroupTombstone,
		purgeNSSUserTombstone
	} from '#lib/api/endpoints.js';
	import type {
		NSSCollision,
		NSSCollisionEntry,
		NSSPendingIDChangesResponse
	} from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { formatDateTime, formatDuration, relativeTime } from '#lib/format.js';
	import { collisionNoun, isAccountCollision } from '#lib/nss.js';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import ConfirmModal from './ConfirmModal.svelte';
	import LoadingBlock from './LoadingBlock.svelte';
	import PageSection from './PageSection.svelte';

	// The NSS directory's two admin panels on the Directory page: uid and gid
	// changes waiting for an admin, and entries that contend for one number
	// or name. Both are things the sync refuses to settle on its own, because
	// either answer can hand a host's files to the wrong person.
	//
	// Accepting a change and purging a tombstone each take a typed name: the
	// rows are near-identical lists of names and numbers, and the wrong row
	// re-owns someone else's files. A refused accept (another entry already
	// holds the new number) is shown on the row it came from, since the fix
	// is in the directory, not in this dialog.
	//
	// The parent renders this only while nss.enabled is on; a 404 from either
	// read means it went off meanwhile, and the panel then says nothing.

	interface Props {
		/** Moved into the page's own live region by the caller. */
		onannounce?: (message: string) => void;
	}

	let { onannounce }: Props = $props();

	// ---- Pending id changes ---------------------------------------------

	let pending = $state<NSSPendingIDChangesResponse | null>(null);
	let pendingError = $state<string | null>(null);
	let pendingGone = $state(false);
	let pendingAttempt = $state(0);

	$effect(() => {
		void pendingAttempt;
		const controller = new AbortController();
		getNSSPendingIDChanges(controller.signal)
			.then((result) => {
				pending = result;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				pendingGone = cause instanceof ApiError && cause.isNotFound;
				pendingError = pendingGone ? null : errorMessage(cause);
			});
		return () => controller.abort();
	});

	function retryPending() {
		pendingError = null;
		pending = null;
		pendingAttempt += 1;
	}

	/** PendingRow is an account or a group change, read the same way. */
	interface PendingRow {
		key: string;
		kind: 'account' | 'group';
		name: string;
		dn: string;
		/** The stored number the accept route is keyed by. */
		number: number;
		/** from is absent for a gid the account was stored without. */
		changes: { label: string; from: number | undefined; to: number }[];
		since: string;
	}

	const pendingRows = $derived.by((): PendingRow[] => {
		if (!pending) return [];
		const accounts = pending.users.map((user) => {
			const changes = [];
			if (user.pending_uid !== undefined) {
				changes.push({ label: 'uid', from: user.uid, to: user.pending_uid });
			}
			if (user.pending_gid !== undefined) {
				changes.push({ label: 'gid', from: user.gid, to: user.pending_gid });
			}
			return {
				key: `account:${user.uid}`,
				kind: 'account' as const,
				name: user.name,
				dn: user.dn,
				number: user.uid,
				changes,
				since: user.pending_since
			};
		});
		const groups = pending.groups.map((group) => ({
			key: `group:${group.gid}`,
			kind: 'group' as const,
			name: group.name,
			dn: group.dn,
			number: group.gid,
			changes: [{ label: 'gid', from: group.gid, to: group.pending_gid }],
			since: group.pending_since
		}));
		return [...accounts, ...groups];
	});

	/** pendingFor is how long a change has waited: "3d", "5h". */
	function pendingFor(since: string): string {
		const at = new Date(since).getTime();
		if (Number.isNaN(at)) return '—';
		const seconds = Math.floor((Date.now() - at) / 1000);
		return seconds < 60 ? 'under a minute' : formatDuration(seconds);
	}

	// Refusals per row, keyed like the rows, shown under the row's name.
	let rowErrors = $state<Record<string, string>>({});

	let accepting = $state<PendingRow | null>(null);
	let acceptBusy = $state(false);
	let acceptError = $state<string | null>(null);

	function openAccept(row: PendingRow) {
		accepting = row;
		acceptError = null;
	}

	async function accept() {
		const row = accepting;
		if (!row) return;
		acceptBusy = true;
		acceptError = null;
		try {
			if (row.kind === 'account') {
				await acceptNSSUserIDChange(row.number);
			} else {
				await acceptNSSGroupIDChange(row.number);
			}
			const rest = { ...rowErrors };
			delete rest[row.key];
			rowErrors = rest;
			accepting = null;
			onannounce?.(`Accepted the new number for ${row.name}.`);
			await refreshPending();
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			if (cause instanceof ApiError && cause.status === 409) {
				// Another entry holds the new number: nothing in this dialog
				// can fix that, so the refusal moves to the row.
				rowErrors = { ...rowErrors, [row.key]: cause.message };
				accepting = null;
			} else {
				acceptError = errorMessage(cause);
			}
		} finally {
			acceptBusy = false;
		}
	}

	/** refreshPending re-reads the list in place, keeping what is on screen if it fails. */
	async function refreshPending() {
		try {
			pending = await getNSSPendingIDChanges();
		} catch (cause) {
			redirectIfUnauthenticated(cause);
		}
	}

	// ---- Collisions -------------------------------------------------------

	let collisions = $state<NSSCollision[] | null>(null);
	let collisionsError = $state<string | null>(null);
	let collisionsGone = $state(false);
	let collisionsAttempt = $state(0);

	$effect(() => {
		void collisionsAttempt;
		const controller = new AbortController();
		getNSSCollisions(controller.signal)
			.then((result) => {
				collisions = result.collisions;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				collisionsGone = cause instanceof ApiError && cause.isNotFound;
				collisionsError = collisionsGone ? null : errorMessage(cause);
			});
		return () => controller.abort();
	});

	function retryCollisions() {
		collisionsError = null;
		collisions = null;
		collisionsAttempt += 1;
	}

	/** refreshCollisions re-reads the list in place, keeping what is on screen if it fails. */
	async function refreshCollisions() {
		try {
			collisions = (await getNSSCollisions()).collisions;
		} catch (cause) {
			redirectIfUnauthenticated(cause);
		}
	}

	/**
	 * entryHref is the viewer page of a stored account or group, by the
	 * number its route is keyed by: /admin/nss/accounts/<uid> or
	 * /admin/nss/groups/<gid>.
	 */
	function entryHref(account: boolean, number: number): string {
		return account
			? resolve('/admin/nss/accounts/[uid]', { uid: String(number) })
			: resolve('/admin/nss/groups/[gid]', { gid: String(number) });
	}

	/** A tombstone that can be purged: a stored row the directory no longer has. */
	interface Purge {
		account: boolean;
		entry: NSSCollisionEntry;
	}

	/** purgeable lists the sides of a collision that are stored tombstones. */
	function purgeable(collision: NSSCollision): NSSCollisionEntry[] {
		return [collision.holder, collision.other].filter((entry) => entry.stored && entry.tombstone);
	}

	let purging = $state<Purge | null>(null);
	let purgeBusy = $state(false);
	let purgeError = $state<string | null>(null);

	function openPurge(collision: NSSCollision, entry: NSSCollisionEntry) {
		purging = { account: isAccountCollision(collision.kind), entry };
		purgeError = null;
	}

	async function purge() {
		const target = purging;
		if (!target) return;
		purgeBusy = true;
		purgeError = null;
		try {
			if (target.account) {
				await purgeNSSUserTombstone(target.entry.number);
			} else {
				await purgeNSSGroupTombstone(target.entry.number);
			}
			purging = null;
			onannounce?.(`Purged the tombstone of ${target.entry.name}.`);
			await refreshCollisions();
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			if (cause instanceof ApiError && cause.isNotFound) {
				// Purged elsewhere, or the entry came back: either way the row
				// on screen is stale, so refresh rather than retry.
				purging = null;
				onannounce?.('That tombstone was already gone.');
				await refreshCollisions();
			} else {
				purgeError = errorMessage(cause);
			}
		} finally {
			purgeBusy = false;
		}
	}
</script>

{#snippet entry(side: NSSCollisionEntry, role: string, account: boolean)}
	<div class="flex min-w-0 flex-col gap-0.5">
		<span class="inline-flex flex-wrap items-center gap-1.5">
			<!-- A stored side opens its viewer page; a refused one has no row
			     to open, so it stays plain text. -->
			{#if side.stored}
				<a
					href={entryHref(account, side.number)}
					class="font-mono text-accent hover:underline"
					data-testid="collision-{role}-link">{side.name}</a
				>
			{:else}
				<span class="font-mono text-ink">{side.name}</span>
			{/if}
			<span class="text-xs text-ink-muted tabular-nums">({side.number})</span>
			{#if side.tombstone}
				<span
					class="rounded bg-surface-muted px-1.5 py-0.5 text-xs font-semibold text-ink-muted"
					data-testid="collision-{role}-tombstone">Tombstone</span
				>
			{:else if !side.stored}
				<span
					class="rounded bg-surface-muted px-1.5 py-0.5 text-xs font-semibold text-ink-muted"
					data-testid="collision-{role}-refused">Not stored</span
				>
			{/if}
		</span>
		<span class="font-mono text-xs break-all text-ink-muted">{side.dn}</span>
	</div>
{/snippet}

<PageSection
	title="Pending id changes"
	description="Accounts and groups whose directory entry now carries a different uid or gid. Hosts keep the stored number until you accept the change."
	testid="nss-pending"
	id="pending-id-changes"
>
	{#if pendingGone}
		<p class="text-dense text-ink-muted">The NSS directory is off on this server.</p>
	{:else if pendingError}
		<Alert
			variant="error"
			title="Could not load the pending id changes"
			onretry={retryPending}
			testid="nss-pending-error">{pendingError}</Alert
		>
	{:else if !pending}
		<LoadingBlock shape="table" count={2} testid="nss-pending-loading" />
	{:else if pendingRows.length === 0}
		<p class="text-dense text-ink-muted" data-testid="nss-pending-empty">
			No pending changes. A uid or gid that moves in the directory waits here for you.
		</p>
	{:else}
		<!-- relative: the sr-only names inside are absolutely placed, and
		     would otherwise widen the page past a phone. -->
		<div class="relative overflow-x-auto">
			<table class="data-table" data-testid="nss-pending-table">
				<thead>
					<tr>
						<th scope="col">Name</th>
						<th scope="col">Change</th>
						<th scope="col">Pending for</th>
						<th scope="col"><span class="sr-only">Action</span></th>
					</tr>
				</thead>
				<tbody>
					{#each pendingRows as row (row.key)}
						<tr data-testid="nss-pending-row">
							<td>
								<div class="flex min-w-0 flex-col gap-0.5">
									<span class="inline-flex items-center gap-1.5">
										<!-- Every pending row is stored, so its name opens its
										     viewer page by the stored number. -->
										<a
											href={entryHref(row.kind === 'account', row.number)}
											class="font-mono text-accent hover:underline"
											data-testid="nss-pending-link">{row.name}</a
										>
										<span class="text-xs text-ink-muted">{row.kind}</span>
									</span>
									<span class="font-mono text-xs break-all text-ink-muted">{row.dn}</span>
									{#if rowErrors[row.key]}
										<span
											role="alert"
											class="text-xs text-danger"
											data-testid="nss-pending-row-error"
										>
											Not accepted: {rowErrors[row.key]}
										</span>
									{/if}
								</div>
							</td>
							<td class="whitespace-nowrap">
								{#each row.changes as change (change.label)}
									<div>
										{change.label}
										<span class="tabular-nums">{change.from ?? 'none'}</span>
										to
										<span class="font-semibold tabular-nums">{change.to}</span>
									</div>
								{/each}
							</td>
							<td class="whitespace-nowrap" title={formatDateTime(row.since)}>
								{pendingFor(row.since)}
							</td>
							<td class="text-right">
								<Button variant="ghost" onclick={() => openAccept(row)} testid="nss-pending-accept">
									Accept<span class="sr-only"> the change for {row.name}</span>
								</Button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</PageSection>

<PageSection
	title="Collisions"
	description="Two directory entries contending for one uid, gid or name. Neither is served until one of them changes."
	testid="nss-collisions"
	id="collisions"
>
	{#if collisionsGone}
		<p class="text-dense text-ink-muted">The NSS directory is off on this server.</p>
	{:else if collisionsError}
		<Alert
			variant="error"
			title="Could not load the collisions"
			onretry={retryCollisions}
			testid="nss-collisions-error">{collisionsError}</Alert
		>
	{:else if !collisions}
		<LoadingBlock shape="table" count={2} testid="nss-collisions-loading" />
	{:else if collisions.length === 0}
		<p class="text-dense text-ink-muted" data-testid="nss-collisions-empty">
			No collisions. Every stored uid, gid and name belongs to one entry.
		</p>
	{:else}
		<!-- relative: the sr-only names inside are absolutely placed, and
		     would otherwise widen the page past a phone. -->
		<div class="relative overflow-x-auto">
			<table class="data-table" data-testid="nss-collisions-table">
				<thead>
					<tr>
						<th scope="col">Contested</th>
						<th scope="col">Held by</th>
						<th scope="col">Wanted by</th>
						<th scope="col">Since</th>
						<th scope="col"><span class="sr-only">Action</span></th>
					</tr>
				</thead>
				<tbody>
					{#each collisions as collision (`${collision.kind}|${collision.value}|${collision.other.dn}`)}
						{@const tombstones = purgeable(collision)}
						<tr data-testid="nss-collision-row">
							<td class="whitespace-nowrap">
								{collisionNoun(collision.kind)}
								<span class="font-mono text-ink">{collision.value}</span>
							</td>
							<td
								>{@render entry(collision.holder, 'holder', isAccountCollision(collision.kind))}</td
							>
							<td>{@render entry(collision.other, 'other', isAccountCollision(collision.kind))}</td>
							<td class="whitespace-nowrap" title={formatDateTime(collision.first_seen_at)}>
								{relativeTime(collision.first_seen_at)}
							</td>
							<td class="text-right">
								{#if tombstones.length > 0}
									<div class="flex flex-col items-end gap-2">
										{#each tombstones as side (side.dn)}
											<Button
												variant="ghost"
												onclick={() => openPurge(collision, side)}
												testid="nss-collision-purge"
											>
												Purge tombstone<span class="sr-only"> of {side.name}</span>
											</Button>
										{/each}
									</div>
								{:else}
									<span class="text-xs text-ink-muted" data-testid="nss-collision-fix"
										>Fix one entry in the directory</span
									>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</PageSection>

{#if accepting}
	{@const row = accepting}
	<ConfirmModal
		title="Accept the new {row.changes.map((c) => c.label).join(' and ')} for {row.name}?"
		reasonless
		variant="primary"
		confirmText={row.name}
		confirmTextNoun="the name"
		confirmTextTestid="nss-accept-typed"
		confirmLabel="Accept change"
		busyLabel="Accepting…"
		busy={acceptBusy}
		error={acceptError}
		errorTitle="The change was not accepted"
		confirmTestid="nss-accept-confirm"
		onconfirm={accept}
		oncancel={() => (accepting = null)}
	>
		<p>
			Hosts see
			{#each row.changes as change, index (change.label)}{index > 0 ? ' and ' : ''}{change.label}
				{change.to}{/each}
			for <span class="font-mono">{row.name}</span> on their next refresh.
		</p>
		<p class="mt-2">
			Files owned by the old number are not moved. Re-own them on each host before you accept, or
			they stay with whoever is given that number next.
		</p>
	</ConfirmModal>
{/if}

{#if purging}
	{@const target = purging}
	<ConfirmModal
		title="Purge the tombstone of {target.entry.name}?"
		reasonless
		confirmText={target.entry.name}
		confirmTextNoun="the name"
		confirmTextTestid="nss-purge-typed"
		confirmLabel="Purge tombstone"
		busyLabel="Purging…"
		busy={purgeBusy}
		error={purgeError}
		errorTitle="The tombstone was not purged"
		confirmTestid="nss-purge-confirm"
		onconfirm={purge}
		oncancel={() => (purging = null)}
	>
		The stored {target.account ? 'account' : 'group'}
		<span class="font-mono">{target.entry.name}</span>
		({target.account ? 'uid' : 'gid'}
		{target.entry.number}) is deleted now instead of at the end of its retention. Its number and
		name are then free, and the entry contending for them is stored at the next sync.
	</ConfirmModal>
{/if}
