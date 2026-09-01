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
