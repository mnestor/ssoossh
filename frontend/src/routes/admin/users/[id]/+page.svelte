<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { getAdminUser, disableUser, enableUser, getUserAudit } from '$lib/api/endpoints';
	import Button from '$lib/components/Button.svelte';
	import AuditTimeline from '$lib/components/AuditTimeline.svelte';
	import type { AdminUserDetail, AuditEvent, DisableUserConsequences } from '$lib/api/types';

	let user: AdminUserDetail | null = $state(null);
	let error: string | null = $state(null);
	let busy = $state(false);
	let actionBusy = $state(false);
	let showDisableConfirm = $state(false);
	let disableConsequences: DisableUserConsequences | null = $state(null);
	// Both reasons are required by the server, so the buttons stay disabled
	// until one is typed rather than letting the request fail.
	let disableReason = $state('');
	let showEnableConfirm = $state(false);
	let enableReason = $state('');
	let auditEvents: AuditEvent[] = $state([]);
	let auditError: string | null = $state(null);

	// $derived, not a plain const: on a client-side navigation the component
	// initialises before the router has populated params, so capturing the
	// value once yields an empty id. The approval page at
	// routes/approve/[id] reads it the same way for the same reason.
	const userId = $derived(page.params.id ?? '');

	/** disabledSourceLabel names what disabled an account in the terms the
	 * re-enable decision is made in. ldap_sync is the one that matters: the
	 * sync clears only its own disables, so a human disable is never undone
	 * automatically. */
	function disabledSourceLabel(source: string): string {
		switch (source) {
			case 'ldap_sync':
				return 'the directory sync (cleared automatically if the entry reappears)';
			case 'soc':
				return 'a SOC operator';
			case 'admin':
				return 'an admin';
			default:
				return source;
		}
	}

	async function loadUser() {
		busy = true;
		error = null;
		if (!userId) {
			error = 'No user ID provided';
			busy = false;
			return;
		}
		try {
			user = await getAdminUser(userId);
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to load user';
		} finally {
			busy = false;
		}
	}

	async function openDisableConfirm() {
		if (!user) return;

		// What disabling does is now bounded: it revokes this person's
		// access. The enrollment count is here to say what it leaves alone.
		disableConsequences = { service_enrollment_count: user.service_enrollment_count };
		showDisableConfirm = true;
	}

	async function handleDisable() {
		actionBusy = true;
		try {
			await disableUser(userId, { reason: disableReason });
			await loadUser();
			await loadAudit();
			showDisableConfirm = false;
			disableConsequences = null;
			disableReason = '';
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to disable user';
		} finally {
			actionBusy = false;
		}
	}

	async function handleEnable() {
		actionBusy = true;
		try {
			await enableUser(userId, { reason: enableReason });
			await loadUser();
			await loadAudit();
			showEnableConfirm = false;
			enableReason = '';
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to enable user';
		} finally {
			actionBusy = false;
		}
	}

	// The timeline is a separate, auditor-scoped read, so its failure is
	// reported beside it rather than replacing the whole page with an error.
	async function loadAudit() {
		try {
			const page = await getUserAudit(userId, { limit: 25 });
			auditEvents = page.events;
			auditError = null;
		} catch (cause) {
			auditError = cause instanceof Error ? cause.message : 'Failed to load the audit timeline';
		}
	}

	onMount(() => {
		loadUser();
		loadAudit();
	});
</script>

<div class="flex max-w-full flex-col gap-6">
	{#if busy}
		<div class="text-center text-ink-muted">Loading...</div>
	{:else if error}
		<div class="rounded-lg border border-danger-surface bg-danger-surface p-4 text-sm text-danger">
			{error}
		</div>
	{:else if user}
		<div class="flex items-center justify-between">
			<div>
				<h1 class="text-2xl font-bold text-ink">{user.username}</h1>
				<p class="text-sm text-ink-muted">{user.email || 'No email'}</p>
			</div>
			<div class="flex gap-2">
				{#if user.disabled_at}
					<Button
						variant="primary"
						testid="enable-user"
						disabled={actionBusy}
						onclick={() => (showEnableConfirm = true)}
					>
						Re-enable
					</Button>
				{:else}
					<Button
						variant="danger"
						testid="disable-user"
						disabled={actionBusy}
						onclick={openDisableConfirm}
					>
						{actionBusy ? 'Disabling...' : 'Disable'}
					</Button>
				{/if}
			</div>
		</div>

		<!-- User details section -->
		<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
			<h2 class="mb-4 font-semibold text-ink">Identity</h2>
			<div class="grid gap-4 sm:grid-cols-2">
				<div>
					<p class="text-xs font-semibold text-ink-muted">Subject (OIDC sub)</p>
					<p class="font-mono text-sm">{user.subject}</p>
				</div>
				<div>
					<p class="text-xs font-semibold text-ink-muted">Created</p>
					<p>{new Date(user.created_at).toLocaleString()}</p>
				</div>
				<div>
					<p class="text-xs font-semibold text-ink-muted">Last Updated</p>
					<p>{new Date(user.updated_at).toLocaleString()}</p>
				</div>
				{#if user.disabled_at}
					<div data-testid="user-disabled-badge">
						<p class="text-xs font-semibold text-danger">Disabled At</p>
						<p>{new Date(user.disabled_at).toLocaleString()}</p>
					</div>
					<div data-testid="user-disabled-reason">
						<p class="text-xs font-semibold text-danger">Disable Reason</p>
						<p>{user.disabled_reason || 'No reason recorded'}</p>
					</div>
					{#if user.disabled_source}
						<div data-testid="user-disabled-source">
							<p class="text-xs font-semibold text-danger">Disabled By</p>
							<!-- The source, not the person: it is what decides whether the
							     directory sync may clear this disable automatically. -->
							<p>{disabledSourceLabel(user.disabled_source)}</p>
						</div>
					{/if}
				{/if}
			</div>

			{#if user.other_accounts.length > 0}
				<div class="mt-4">
					<p class="text-xs font-semibold text-ink-muted">Other Accounts</p>
					<div class="flex flex-wrap gap-2">
						{#each user.other_accounts as acct (acct)}
							<span class="rounded bg-surface px-2 py-1 text-sm">{acct}</span>
						{/each}
					</div>
				</div>
			{/if}

			{#if user.service_accounts.length > 0}
				<div class="mt-4">
					<p class="text-xs font-semibold text-ink-muted">Service Accounts</p>
					<div class="flex flex-wrap gap-2">
						{#each user.service_accounts as acct (acct)}
							<span class="rounded bg-surface px-2 py-1 text-sm">{acct}</span>
						{/each}
					</div>
				</div>
			{/if}

			{#if Object.keys(user.extra_fields).length > 0}
				<div class="mt-4">
					<p class="text-xs font-semibold text-ink-muted">Extra Fields</p>
					<div class="space-y-2">
						{#each Object.entries(user.extra_fields) as [key, value] (key)}
							<div class="flex items-start gap-2">
								<span
									class="flex-shrink-0 rounded bg-surface px-2 py-1 font-mono text-sm text-ink-muted"
									>{key}</span
								>
								<span class="flex-grow rounded bg-surface px-2 py-1 text-sm">
									{#if Array.isArray(value)}
										{value.join(', ')}
									{:else}
										{value}
									{/if}
								</span>
							</div>
						{/each}
					</div>
				</div>
			{/if}

			{#if user.disabled_by_username}
				<div class="mt-4">
					<p class="text-xs font-semibold text-danger">Disabled By</p>
					<p>{user.disabled_by_username}</p>
				</div>
			{/if}
		</div>

		<!-- Group membership. Never an authorization input: this is what the
		     server recorded for notification fan-out and display, and it is
		     what answers "why did this reach them" or "why is the group I
		     expected missing". -->
		<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
			<h2 class="mb-1 font-semibold text-ink">Group membership</h2>
			<p class="mb-4 text-[13px] text-ink-muted">
				Captured at login (OIDC) and by the directory sync (LDAP). Only group names the
				configuration references are stored, so a group missing here may simply be unconfigured.
				Never used to authorize anything.
			</p>
			{#if user.groups.length === 0}
				<p class="text-sm text-ink-muted" data-testid="user-groups-empty">
					No group memberships have been captured for this user.
				</p>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full text-sm" data-testid="user-groups-table">
						<thead>
							<tr class="border-b border-border-subtle text-left text-xs text-ink-muted">
								<th class="py-2 pr-4 font-semibold">Group</th>
								<th class="py-2 pr-4 font-semibold">Source</th>
								<th class="py-2 pr-4 font-semibold">First seen</th>
								<th class="py-2 font-semibold">Last seen</th>
							</tr>
						</thead>
						<tbody>
							{#each user.groups as group (group.source + '/' + group.name)}
								<tr class="border-b border-border-subtle last:border-0">
									<td class="py-2 pr-4 font-mono">{group.name}</td>
									<td class="py-2 pr-4">
										<span class="rounded bg-surface px-2 py-0.5 text-xs uppercase"
											>{group.source}</span
										>
									</td>
									<td class="py-2 pr-4 text-ink-muted"
										>{new Date(group.first_seen_at).toLocaleString()}</td
									>
									<td class="py-2 text-ink-muted"
										>{new Date(group.last_seen_at).toLocaleString()}</td
									>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>

		<!-- Directory record. Present only for a user who has been enriched at
		     least once; its absence is an answer rather than an error. -->
		{#if user.directory}
			<div
				class="rounded-lg border border-border-subtle bg-surface-muted p-4"
				data-testid="user-directory"
			>
				<h2 class="mb-1 font-semibold text-ink">Directory record</h2>
				<p class="mb-4 text-[13px] text-ink-muted">
					What the LDAP sync last read for this user, and whether their entry still resolves.
				</p>

				<div class="grid gap-4 sm:grid-cols-2">
					<div class="sm:col-span-2">
						<p class="text-xs font-semibold text-ink-muted">Distinguished name</p>
						<p class="font-mono text-sm break-all">{user.directory.dn || '—'}</p>
					</div>
					<div>
						<p class="text-xs font-semibold text-ink-muted">Entry last seen</p>
						<p>
							{user.directory.last_seen_at
								? new Date(user.directory.last_seen_at).toLocaleString()
								: 'never'}
						</p>
					</div>
					<div>
						<p class="text-xs font-semibold text-ink-muted">Last sync attempt</p>
						<p>
							{user.directory.last_synced_at
								? new Date(user.directory.last_synced_at).toLocaleString()
								: 'never'}
						</p>
					</div>
				</div>

				{#if user.directory.first_missing_at}
					<div
						class="mt-4 rounded border-l-2 border-danger bg-surface p-3 text-sm"
						data-testid="user-directory-missing"
					>
						<p class="font-semibold text-danger">Entry is currently missing</p>
						<p class="text-ink-muted">
							First missing {new Date(user.directory.first_missing_at).toLocaleString()}, observed
							by {user.directory.consecutive_misses} sync
							{user.directory.consecutive_misses === 1 ? 'pass' : 'passes'}. The auto-disable is
							decided on how long it has been missing, not on how many passes have seen it.
						</p>
					</div>
				{/if}

				{#if Object.keys(user.directory.attributes).length > 0}
					<div class="mt-4">
						<p class="mb-2 text-xs font-semibold text-ink-muted">Stored field values</p>
						<div class="space-y-2">
							{#each Object.entries(user.directory.attributes) as [field, values] (field)}
								<div class="flex items-start gap-2">
									<span
										class="flex-shrink-0 rounded bg-surface px-2 py-1 font-mono text-sm text-ink-muted"
										>{field}</span
									>
									<span class="flex-grow rounded bg-surface px-2 py-1 font-mono text-sm break-all">
										{values.length > 0 ? values.join(', ') : '—'}
									</span>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		{/if}

		<!-- Notification choices. A kind with no row is on its registered
		     default, so an empty list means "all default", not "all off". -->
		{#if user.notification_preferences.length > 0}
			<div
				class="rounded-lg border border-border-subtle bg-surface-muted p-4"
				data-testid="user-notification-preferences"
			>
				<h2 class="mb-1 font-semibold text-ink">Notification choices</h2>
				<p class="mb-4 text-[13px] text-ink-muted">
					Only the choices this user has changed. Anything not listed is on its default.
				</p>
				<div class="space-y-2">
					{#each user.notification_preferences as pref (pref.kind)}
						<div class="flex items-center gap-2 text-sm">
							<span class="rounded bg-surface px-2 py-1 font-mono text-ink-muted">{pref.kind}</span>
							<span class:text-danger={!pref.enabled} class:text-granted={pref.enabled}>
								{pref.enabled ? 'on' : 'off'}
							</span>
							<span class="text-xs text-ink-muted">
								changed {new Date(pref.updated_at).toLocaleString()}
							</span>
						</div>
					{/each}
				</div>
			</div>
		{/if}

		<!-- Activity section -->
		<div class="grid gap-4 sm:grid-cols-2">
			<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
				<p class="text-xs font-semibold text-ink-muted">Certificates</p>
				<p class="text-2xl font-bold text-accent">{user.certificate_count}</p>
			</div>
			<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
				<p class="text-xs font-semibold text-ink-muted">Active Service Enrollments</p>
				<p class="text-2xl font-bold text-accent">{user.service_enrollment_count}</p>
			</div>
		</div>

		<!-- Disable confirmation modal -->
		{#if showDisableConfirm && user && disableConsequences}
			<div class="fixed inset-0 flex items-center justify-center bg-black/50 p-4">
				<div class="w-full max-w-md rounded-lg bg-surface p-6 shadow-lg">
					<h3 class="mb-4 text-lg font-semibold text-ink">Disable User?</h3>
					<p data-testid="disable-consequences" class="mb-4 text-sm text-ink-muted">
						This will prevent <strong>{user.username}</strong> from authenticating immediately.
						{#if disableConsequences.service_enrollment_count > 0}
							The <strong>{disableConsequences.service_enrollment_count}</strong> live service enrollment(s)
							they approved keep working: those belong to their service accounts, not to this person,
							and everyone else holding the account keeps them.
						{:else}
							They have approved no live service enrollments.
						{/if}
					</p>
					<label class="mb-4 block">
						<span class="mb-1 block text-xs font-semibold text-ink-muted"> Reason (required) </span>
						<textarea
							data-testid="disable-reason"
							bind:value={disableReason}
							rows="3"
							placeholder="Why is this account being disabled? e.g. offboarded, SEC-1234"
							class="w-full rounded border border-border-subtle bg-surface-muted p-2 text-sm"
						></textarea>
						<span class="mt-1 block text-xs text-ink-muted">
							Shown to whoever decides whether to re-enable this account.
						</span>
					</label>
					<div class="flex justify-end gap-2">
						<Button
							variant="ghost"
							disabled={actionBusy}
							onclick={() => {
								showDisableConfirm = false;
								disableConsequences = null;
								disableReason = '';
							}}
						>
							Cancel
						</Button>
						<Button
							variant="danger"
							testid="confirm-disable"
							disabled={actionBusy || disableReason.trim() === ''}
							onclick={handleDisable}
						>
							{actionBusy ? 'Disabling...' : 'Disable'}
						</Button>
					</div>
				</div>
			</div>
		{/if}

		<!-- Re-enable confirmation, which exists for its reason field: the
		     next reader of this account benefits from "cleared with security,
		     SEC-1234" as much as from why it was disabled. -->
		{#if showEnableConfirm && user}
			<div class="fixed inset-0 flex items-center justify-center bg-black/50 p-4">
				<div class="w-full max-w-md rounded-lg bg-surface p-6 shadow-lg">
					<h3 class="mb-4 text-lg font-semibold text-ink">Re-enable User?</h3>
					<p class="mb-4 text-sm text-ink-muted">
						This restores <strong>{user.username}</strong>'s ability to authenticate. Service
						enrollments that already expired are not restored.
					</p>
					{#if user.disabled_reason}
						<p class="mb-4 rounded bg-surface-muted p-2 text-sm">
							<span class="font-semibold text-ink-muted">Disabled because:</span>
							{user.disabled_reason}
						</p>
					{/if}
					<label class="mb-4 block">
						<span class="mb-1 block text-xs font-semibold text-ink-muted"> Reason (required) </span>
						<textarea
							data-testid="enable-reason"
							bind:value={enableReason}
							rows="3"
							placeholder="Why is this account being restored? e.g. cleared with security, SEC-1234"
							class="w-full rounded border border-border-subtle bg-surface-muted p-2 text-sm"
						></textarea>
					</label>
					<div class="flex justify-end gap-2">
						<Button
							variant="ghost"
							disabled={actionBusy}
							onclick={() => {
								showEnableConfirm = false;
								enableReason = '';
							}}
						>
							Cancel
						</Button>
						<Button
							variant="primary"
							testid="confirm-enable"
							disabled={actionBusy || enableReason.trim() === ''}
							onclick={handleEnable}
						>
							{actionBusy ? 'Enabling...' : 'Re-enable'}
						</Button>
					</div>
				</div>
			</div>
		{/if}

		<!-- Audit timeline: everything this account did and everything done
		     to it, from the same rows. -->
		<div class="rounded-lg border border-border-subtle bg-surface p-4">
			<h2 class="mb-4 font-semibold text-ink">Audit Timeline</h2>
			{#if auditError}
				<p class="text-sm text-danger">{auditError}</p>
			{:else}
				<AuditTimeline events={auditEvents} subjectUserId={userId} />
			{/if}
		</div>
	{/if}
</div>
