<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { ApiError } from '#lib/api/client.js';
	import { getNSSGroup, listNSSGroupMembers } from '#lib/api/endpoints.js';
	import type { NSSGroupDetail, NSSGroupMember } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import CopyableId from '#lib/components/CopyableId.svelte';
	import DetailRow from '#lib/components/DetailRow.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import LoadMoreFooter from '#lib/components/LoadMoreFooter.svelte';
	import NSSDirectoryRef from '#lib/components/NSSDirectoryRef.svelte';
	import NSSEntryMarks from '#lib/components/NSSEntryMarks.svelte';
	import NSSServedVerdict from '#lib/components/NSSServedVerdict.svelte';
	import NSSStateBadge from '#lib/components/NSSStateBadge.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { formatDateTime } from '#lib/format.js';
	import { describeLoaded } from '#lib/listStatus.js';
	import { parseEntryNumber } from '#lib/nss.js';
	import { session } from '#lib/session.svelte.js';

	// One stored NSS group: whether hosts are served it and how many member
	// names they receive, then the row as stored, its state, and its
	// membership rows, paged from their own endpoint. Read only. The served
	// verdict is the server's, as on an account's page.

	const gid = $derived(parseEntryNumber(page.params.gid));

	let group = $state<NSSGroupDetail | null>(null);
	let loadError = $state<string | null>(null);
	let notFound = $state(false);
	let nssGone = $state(false);
	let hasLoaded = $state(false);
	let attempt = $state(0);

	const nssOff = $derived(!(session.user?.nss_enabled ?? false) || nssGone);

	const backHref = $derived.by(() => {
		const carried = page.url.searchParams.get('back') ?? '';
		const search = carried.startsWith('?') && !carried.includes('#') ? carried : '';
		return `${resolve('/admin/nss/groups')}${search}`;
	});

	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
		const number = gid;
		group = null;
		notFound = false;
		if (nssOff) return;
		if (number === null) {
			notFound = true;
			hasLoaded = true;
			return;
		}
		const controller = new AbortController();
		getNSSGroup(number, controller.signal)
			.then((result) => {
				group = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				if (cause instanceof ApiError && cause.isNotFound) {
					nssGone = cause.message.includes('nss.enabled');
					notFound = !nssGone;
				} else {
					loadError = errorMessage(cause);
				}
				hasLoaded = true;
			});
		return () => controller.abort();
	});

	// ---- Members ----------------------------------------------------------
	//
	// Their own load, failure and Load more: a group of thousands pages, and
	// a members read that fails leaves the facts above standing.

	let members = $state<NSSGroupMember[]>([]);
	let membersTotal = $state(0);
	let membersCursor = $state<string | null>(null);
	let membersLoaded = $state(false);
	let membersBusy = $state(false);
	let membersError = $state<string | null>(null);
	let membersMoreError = $state<string | null>(null);
	let latestMembers = 0;

	/** loadMembers reads the first page of members or, given a cursor, appends the next. */
	async function loadMembers(number: number, after?: string) {
		const current = ++latestMembers;
		membersBusy = true;
		membersMoreError = null;
		if (!after) membersError = null;
		try {
			const result = await listNSSGroupMembers(number, { after, limit: 100 });
			if (current !== latestMembers) return;
			members = after ? [...members, ...result.members] : result.members;
			membersTotal = result.total;
			membersCursor = result.next_cursor ?? null;
			membersLoaded = true;
		} catch (cause) {
			if (current !== latestMembers || redirectIfUnauthenticated(cause)) return;
			if (after) {
				membersMoreError = errorMessage(cause);
			} else {
				membersError = errorMessage(cause);
				membersLoaded = true;
			}
		} finally {
			if (current === latestMembers) membersBusy = false;
		}
	}

	// The first page once the group itself has loaded, and again for each
	// group opened from this page.
	$effect(() => {
		const number = group?.gid;
		if (number === undefined) return;
		members = [];
		membersLoaded = false;
		void loadMembers(number);
	});

	function retryMembers() {
		if (group) void loadMembers(group.gid);
	}

	async function moreMembers() {
		if (group && membersCursor) await loadMembers(group.gid, membersCursor);
	}
</script>

<svelte:head><title>{group ? `${group.name} · ` : ''}Stored group · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading
		title="Stored group"
		testid="nss-group-heading"
		back={{ href: backHref, label: 'Stored groups', testid: 'nss-group-back' }}
	/>

	{#if nssOff}
		<Alert variant="info" title="The NSS directory is off" testid="nss-group-off">
			<code>nss.enabled</code> is false, so the server stores no groups for hosts and serves none.
		</Alert>
	{:else if notFound}
		<Alert
			variant="info"
			title="No stored group has gid {gid === null ? page.params.gid : gid}"
			testid="nss-group-missing"
		>
			A host that looks it up still asks the directory on demand and stores what it finds; this page
			never asks. <NSSDirectoryRef section="probe" testid="nss-group-missing-probe" />
			{#if gid !== null}
				Private groups are never stored: if this is an account's private group, it is on the
				<a
					href={resolve('/admin/nss/accounts/[uid]', { uid: String(gid) })}
					class="text-accent hover:underline"
					data-testid="nss-group-missing-account">account with uid {gid}</a
				>.
			{/if}
		</Alert>
	{:else if loadError}
		<Alert
			variant="error"
			title="Could not load this stored group"
			onretry={retry}
			testid="nss-group-error">{loadError}</Alert
		>
	{:else if !hasLoaded || !group}
		<LoadingBlock shape="lines" count={6} label="Loading this group…" testid="nss-group-loading" />
	{:else}
		<div class="flex flex-col gap-3 rounded-lg bg-surface p-5" data-testid="nss-group-identity">
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div class="flex min-w-0 flex-wrap items-center gap-3">
					<NSSStateBadge state={group.state} testid="nss-group-state" />
					<span
						class="font-mono text-lg font-semibold break-all text-ink"
						data-testid="nss-group-name">{group.name}</span
					>
					<span class="text-sm text-ink-muted" data-testid="nss-group-numbers">
						gid <span class="font-mono text-ink">{group.gid}</span>
					</span>
				</div>
				<CopyableId
					value={String(group.gid)}
					label="GID"
					length={String(group.gid).length}
					testid="nss-group-gid"
				/>
			</div>
			<NSSServedVerdict
				kind="group"
				served={!!group.served}
				reason={group.not_served}
				withheldReason={group.withheld_reason}
				testid="nss-group-verdict"
			/>
		</div>

		<PageSection
			title="What hosts get"
			description="The record a host's lookup by this gid receives now, built by the lookup itself. Its member names are listed under Members."
			testid="nss-group-served"
		>
			{#if group.served}
				{@const served = group.served}
				<dl class="flex flex-col divide-y divide-border-subtle">
					<DetailRow label="Name" mono>{served.name}</DetailRow>
					<DetailRow label="GID" mono>{served.gid}</DetailRow>
					<DetailRow label="Member names">
						<span class="tabular-nums" data-testid="nss-group-served-count"
							>{served.member_count}</span
						>
					</DetailRow>
				</dl>
				{#if served.members_truncated}
					<p class="mt-2 max-w-[70ch] text-dense text-trimmed" data-testid="nss-group-truncated">
						<code>nss.max_group_members</code> cut the list, so hosts receive only these
						{served.member_count} member names.
					</p>
				{/if}
			{:else}
				<p class="max-w-[70ch] text-sm text-ink" data-testid="nss-group-not-served">
					Nothing: a host's lookup by gid <span class="font-mono">{group.gid}</span> finds no entry.
				</p>
			{/if}
		</PageSection>

		<PageSection
			title="Stored entry"
			description="The row as the sync stored it from the directory."
			testid="nss-group-stored"
		>
			<dl class="flex flex-col divide-y divide-border-subtle">
				<DetailRow label="DN">
					{#if group.dn}
						<span class="font-mono">{group.dn}</span>
					{:else}
						<span class="text-ink-muted" data-testid="nss-group-no-dn"
							>None: another entry took this DN, so the row is matched by its directory id alone</span
						>
					{/if}
				</DetailRow>
				<DetailRow label="Directory id">
					{#if group.directory_id}
						<span class="font-mono">{group.directory_id}</span>
					{:else}
						<span class="text-ink-muted"><code>ldap.id_attribute</code> is unset</span>
					{/if}
				</DetailRow>
				<DetailRow label="Membership rows">
					<span class="tabular-nums">{group.member_count}</span>
				</DetailRow>
				<DetailRow label="Created">{formatDateTime(group.created_at)}</DetailRow>
				<DetailRow label="Updated">{formatDateTime(group.updated_at)}</DetailRow>
				<DetailRow label="Last synced">{formatDateTime(group.last_synced_at)}</DetailRow>
			</dl>
		</PageSection>

		<NSSEntryMarks
			kind="group"
			state={group.state}
			missingSince={group.missing_since}
			deletedAt={group.deleted_at}
			withheldAt={group.withheld_at}
			withheldReason={group.withheld_reason}
			gid={group.gid}
			pendingGid={group.pending_gid}
			pendingSince={group.pending_since}
		/>

		<PageSection
			title="Members"
			description="Every membership row stored for this gid, by uid, served or not. A member with no stored account is never served."
			testid="nss-group-members"
		>
			{#if membersError}
				<Alert
					variant="error"
					title="Could not load the members"
					onretry={retryMembers}
					testid="nss-group-members-error">{membersError}</Alert
				>
			{:else if !membersLoaded}
				<LoadingBlock shape="table" count={4} testid="nss-group-members-loading" />
			{:else if members.length === 0}
				<p class="text-dense text-ink-muted" data-testid="nss-group-members-empty">
					No membership rows are stored for this gid.
				</p>
			{:else}
				<div class="flex flex-col gap-3">
					<div class="relative overflow-x-auto">
						<table class="data-table" data-testid="nss-group-members-table">
							<thead>
								<tr>
									<th scope="col" class="text-right">UID</th>
									<th scope="col">Account</th>
									<th scope="col">State</th>
								</tr>
							</thead>
							<tbody>
								{#each members as member (member.uid)}
									<tr data-testid="nss-group-member">
										<td class="text-right font-mono">{member.uid}</td>
										<td>
											{#if member.name}
												<a
													href={resolve('/admin/nss/accounts/[uid]', { uid: String(member.uid) })}
													class="font-mono text-accent hover:underline"
													data-testid="nss-group-member-link">{member.name}</a
												>
											{:else}
												<span class="text-ink-muted" data-testid="nss-group-member-unstored"
													>No stored account, so never served</span
												>
											{/if}
										</td>
										<td>
											{#if member.state}<NSSStateBadge state={member.state} />{:else}<span
													class="text-ink-muted">—</span
												>{/if}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
					<LoadMoreFooter
						summary={describeLoaded(members.length, membersTotal - members.length, 'more')}
						hasMore={!!membersCursor}
						busy={membersBusy}
						error={membersMoreError}
						errorTitle="Could not load more members"
						onmore={moreMembers}
						testid="nss-group-members"
					/>
				</div>
			{/if}
		</PageSection>
	{/if}
</PageShell>
