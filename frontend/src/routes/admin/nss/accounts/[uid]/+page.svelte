<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { ApiError } from '#lib/api/client.js';
	import { getNSSAccount } from '#lib/api/endpoints.js';
	import type { NSSAccountDetail } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import ConfigProse from '#lib/components/ConfigProse.svelte';
	import CopyableId from '#lib/components/CopyableId.svelte';
	import DetailRow from '#lib/components/DetailRow.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import NSSDirectoryRef from '#lib/components/NSSDirectoryRef.svelte';
	import NSSEntryMarks from '#lib/components/NSSEntryMarks.svelte';
	import NSSServedVerdict from '#lib/components/NSSServedVerdict.svelte';
	import NSSStateBadge from '#lib/components/NSSStateBadge.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { formatDateTime } from '#lib/format.js';
	import { parseEntryNumber } from '#lib/nss.js';
	import { session } from '#lib/session.svelte.js';

	// One stored NSS account: whether hosts are served it and what they get,
	// then the row as stored, its state, and its stored group memberships.
	// Read only. The served verdict comes from the server's served and
	// not_served alone and sits in the identity strip ahead of the raw
	// fields, because concluding the wrong way sends an admin to fix the
	// wrong layer.

	const uid = $derived(parseEntryNumber(page.params.uid));

	let account = $state<NSSAccountDetail | null>(null);
	let loadError = $state<string | null>(null);
	let notFound = $state(false);
	let nssGone = $state(false);
	let hasLoaded = $state(false);
	let attempt = $state(0);

	const nssOff = $derived(!(session.user?.nss_enabled ?? false) || nssGone);

	// The list's filters, carried by its rows so the chip returns to the same
	// view. Only a query string is taken, appended to the fixed list route.
	const backHref = $derived.by(() => {
		const carried = page.url.searchParams.get('back') ?? '';
		const search = carried.startsWith('?') && !carried.includes('#') ? carried : '';
		return `${resolve('/admin/nss/accounts')}${search}`;
	});

	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
		const number = uid;
		account = null;
		notFound = false;
		if (nssOff) return;
		if (number === null) {
			// Not a uid at all: no stored account can have it.
			notFound = true;
			hasLoaded = true;
			return;
		}
		const controller = new AbortController();
		getNSSAccount(number, controller.signal)
			.then((result) => {
				account = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				if (cause instanceof ApiError && cause.isNotFound) {
					// The route answers 404 for both "no such account" and
					// "NSS is off"; only the second names nss.enabled.
					nssGone = cause.message.includes('nss.enabled');
					notFound = !nssGone;
				} else {
					loadError = errorMessage(cause);
				}
				hasLoaded = true;
			});
		return () => controller.abort();
	});

	/** optional renders an absent value as the em dash every table uses. */
	function optional(value: string | undefined | null): string {
		return value ? value : '—';
	}
</script>

<svelte:head
	><title>{account ? `${account.name} · ` : ''}Stored account · ssoossh</title></svelte:head
>

<PageShell width="wide">
	<PageHeading
		title="Stored account"
		testid="nss-account-heading"
		back={{ href: backHref, label: 'Stored accounts', testid: 'nss-account-back' }}
	/>

	{#if nssOff}
		<Alert variant="info" title="The NSS directory is off" testid="nss-account-off">
			<code>nss.enabled</code> is false, so the server stores no accounts for hosts and serves none.
		</Alert>
	{:else if notFound}
		<Alert
			variant="info"
			title={uid === null
				? `No stored account has uid ${page.params.uid}`
				: `No stored account has uid ${uid}`}
			testid="nss-account-missing"
		>
			A host that looks it up still asks the directory on demand and stores what it finds; this page
			never asks. <NSSDirectoryRef section="probe" testid="nss-account-missing-probe" />
		</Alert>
	{:else if loadError}
		<Alert
			variant="error"
			title="Could not load this stored account"
			onretry={retry}
			testid="nss-account-error">{loadError}</Alert
		>
	{:else if !hasLoaded || !account}
		<LoadingBlock
			shape="lines"
			count={8}
			label="Loading this account…"
			testid="nss-account-loading"
		/>
	{:else}
		<!-- Identity strip: the account, its state, its numbers, and the
		     served verdict, ahead of every raw field. -->
		<div class="flex flex-col gap-3 rounded-lg bg-surface p-5" data-testid="nss-account-identity">
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div class="flex min-w-0 flex-wrap items-center gap-3">
					<NSSStateBadge state={account.state} testid="nss-account-state" />
					<span
						class="font-mono text-lg font-semibold break-all text-ink"
						data-testid="nss-account-name">{account.name}</span
					>
					<span class="text-sm text-ink-muted" data-testid="nss-account-numbers">
						uid <span class="font-mono text-ink">{account.uid}</span>,
						{#if account.gid === undefined}no gid{:else}gid
							<span class="font-mono text-ink">{account.gid}</span>{/if}
					</span>
				</div>
				<CopyableId
					value={String(account.uid)}
					label="UID"
					length={String(account.uid).length}
					testid="nss-account-uid"
				/>
			</div>
			<NSSServedVerdict
				kind="account"
				served={!!account.served}
				status={account.served?.status}
				reason={account.not_served}
				withheldReason={account.withheld_reason}
				testid="nss-account-verdict"
			/>
		</div>

		<PageSection
			title="What hosts get"
			description="The record a host's lookup by this uid receives now, built by the lookup itself."
			testid="nss-account-served"
		>
			{#if account.served}
				{@const served = account.served}
				<dl class="flex flex-col divide-y divide-border-subtle">
					<DetailRow label="Name" mono>{served.name}</DetailRow>
					<DetailRow label="UID" mono>{served.uid}</DetailRow>
					<DetailRow label="GID">
						<span class="font-mono">{served.gid}</span>
						<span class="text-ink-muted"> (after <code>nss.auto_private_groups</code>)</span>
					</DetailRow>
					<DetailRow label="Groups">
						{#if served.groups.length === 0}
							<span class="text-ink-muted">None</span>
						{:else}
							<span class="flex flex-wrap gap-x-3 gap-y-1" data-testid="nss-account-served-groups">
								{#each served.groups as group, index (group.gid)}
									<span>
										<a
											href={resolve('/admin/nss/groups/[gid]', { gid: String(group.gid) })}
											class="font-mono text-accent hover:underline"
											data-testid="nss-account-served-group">{group.name}</a
										>
										<span class="text-ink-muted">({group.gid}{index === 0 ? ', primary' : ''})</span
										>
									</span>
								{/each}
							</span>
						{/if}
					</DetailRow>
					<DetailRow label="Home" mono>{optional(served.home)}</DetailRow>
					<DetailRow label="Shell" mono>{optional(served.shell)}</DetailRow>
					<DetailRow label="Shell preference" mono>{optional(served.shell_pref)}</DetailRow>
					<DetailRow label="Gecos">{optional(served.gecos)}</DetailRow>
					<DetailRow label="Status">
						<span class="capitalize" data-testid="nss-account-served-status">{served.status}</span>
					</DetailRow>
				</dl>
			{:else}
				<p class="max-w-[70ch] text-sm text-ink" data-testid="nss-account-not-served">
					Nothing: a host's lookup by uid <span class="font-mono">{account.uid}</span> finds no entry.
				</p>
			{/if}
		</PageSection>

		<PageSection
			title="Stored entry"
			description="The row as the sync stored it from the directory, before the server's fallbacks."
			testid="nss-account-stored"
		>
			<dl class="flex flex-col divide-y divide-border-subtle">
				<DetailRow label="DN">
					{#if account.dn}
						<span class="font-mono">{account.dn}</span>
					{:else}
						<span class="text-ink-muted" data-testid="nss-account-no-dn"
							>None: another entry took this DN, so the row is matched by its directory id alone</span
						>
					{/if}
				</DetailRow>
				<DetailRow label="Directory id">
					{#if account.directory_id}
						<span class="font-mono">{account.directory_id}</span>
					{:else}
						<span class="text-ink-muted"><code>ldap.id_attribute</code> is unset</span>
					{/if}
				</DetailRow>
				<DetailRow label="Stored GID">
					{#if account.gid === undefined}
						<span class="text-ink-muted" data-testid="nss-account-no-gid"
							>None: the entry has no gid attribute, which only <code>nss.auto_private_groups</code> true
							accepts</span
						>
					{:else}
						<span class="font-mono">{account.gid}</span>
						<span class="text-ink-muted">(before <code>nss.auto_private_groups</code>)</span>
					{/if}
				</DetailRow>
				<DetailRow label="Home">
					{#if account.home}<span class="font-mono">{account.home}</span>{:else}<span
							class="text-ink-muted"
							><ConfigProse
								text="Not set in the directory; the nss.default_home fallback applies"
							/></span
						>{/if}
				</DetailRow>
				<DetailRow label="Shell">
					{#if account.shell}<span class="font-mono">{account.shell}</span>{:else}<span
							class="text-ink-muted"
							><ConfigProse
								text="Not set in the directory; the nss.default_shell fallback applies"
							/></span
						>{/if}
				</DetailRow>
				<DetailRow label="Gecos">{optional(account.gecos)}</DetailRow>
				<DetailRow label="Directory disabled">
					<span data-testid="nss-account-dir-disabled">{account.dir_disabled ? 'Yes' : 'No'}</span>
				</DetailRow>
				<DetailRow label="Linked person">
					{#if account.person}
						{@const person = account.person}
						<span class="flex flex-wrap items-center gap-2" data-testid="nss-account-person">
							<a
								href={resolve('/admin/users/[id]', { id: person.user_id })}
								class="font-mono text-accent hover:underline"
								data-testid="nss-account-person-link">{person.username}</a
							>
							{#if person.display_name}<span class="text-ink-muted">{person.display_name}</span
								>{/if}
							{#if person.disabled_at}
								<span
									class="rounded bg-danger-surface px-2 py-0.5 text-xs font-semibold text-danger"
									data-testid="nss-account-person-disabled"
									>Disabled {formatDateTime(person.disabled_at)}</span
								>
							{:else}
								<span
									class="rounded bg-granted-surface px-2 py-0.5 text-xs font-semibold text-granted"
									data-testid="nss-account-person-active">Active</span
								>
							{/if}
						</span>
					{:else}
						<span class="text-ink-muted" data-testid="nss-account-no-person"
							>None. Shared and service accounts have none, nor does anyone who has not logged in.</span
						>
					{/if}
				</DetailRow>
				<DetailRow label="Created">{formatDateTime(account.created_at)}</DetailRow>
				<DetailRow label="Updated">{formatDateTime(account.updated_at)}</DetailRow>
				<DetailRow label="Last synced">{formatDateTime(account.last_synced_at)}</DetailRow>
			</dl>
		</PageSection>

		<NSSEntryMarks
			kind="account"
			state={account.state}
			missingSince={account.missing_since}
			deletedAt={account.deleted_at}
			withheldAt={account.withheld_at}
			withheldReason={account.withheld_reason}
			uid={account.uid}
			pendingUid={account.pending_uid}
			gid={account.gid}
			pendingGid={account.pending_gid}
			pendingSince={account.pending_since}
			dirDisabled={account.dir_disabled}
			personDisabledAt={account.person?.disabled_at}
		/>

		<PageSection
			title="Stored memberships"
			description="Every membership row stored for this uid, whether or not the group is served. The primary group is listed only when a membership row names it."
			testid="nss-account-memberships"
		>
			{#if account.groups.length === 0}
				<p class="text-dense text-ink-muted" data-testid="nss-account-memberships-empty">
					No membership rows are stored for this uid.
				</p>
			{:else}
				<div class="relative overflow-x-auto">
					<table class="data-table" data-testid="nss-account-memberships-table">
						<thead>
							<tr>
								<th scope="col" class="text-right">GID</th>
								<th scope="col">Group</th>
								<th scope="col">State</th>
							</tr>
						</thead>
						<tbody>
							{#each account.groups as membership (membership.gid)}
								<tr data-testid="nss-account-membership">
									<td class="text-right font-mono">{membership.gid}</td>
									<td>
										{#if membership.name}
											<a
												href={resolve('/admin/nss/groups/[gid]', { gid: String(membership.gid) })}
												class="font-mono text-accent hover:underline"
												data-testid="nss-account-membership-link">{membership.name}</a
											>
										{:else}
											<span class="text-ink-muted" data-testid="nss-account-membership-unstored"
												>No stored group has this gid</span
											>
										{/if}
									</td>
									<td>
										{#if membership.state}<NSSStateBadge state={membership.state} />{:else}<span
												class="text-ink-muted">—</span
											>{/if}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</PageSection>
	{/if}
</PageShell>
