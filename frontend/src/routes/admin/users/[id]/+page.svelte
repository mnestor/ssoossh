<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { getAdminUser, disableUser, enableUser, getAdminConfig } from '$lib/api/endpoints';
	import Button from '$lib/components/Button.svelte';
	import type {
		AdminUserDetail,
		DisableUserConsequences,
		EffectiveConfigResponse
	} from '$lib/api/types';

	let user: AdminUserDetail | null = $state(null);
	let config: EffectiveConfigResponse | null = $state(null);
	let error: string | null = $state(null);
	let busy = $state(false);
	let actionBusy = $state(false);
	let showDisableConfirm = $state(false);
	let disableConsequences: DisableUserConsequences | null = $state(null);

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

	async function loadConfig() {
		try {
			config = await getAdminConfig();
		} catch (cause) {
			console.error('Failed to load admin config:', cause);
		}
	}

	async function openDisableConfirm() {
		// Calculate the real consequences before showing the modal
		if (!config || !user) return;

		// Parse grace period and calculate expiry
		const gracePeriodMs = parseGracePeriod(config.admin_disable_grace_period);
		const expireAt = new Date(Date.now() + gracePeriodMs);

		disableConsequences = {
			service_enrollment_count: user.service_enrollment_count,
			grace_period_seconds: gracePeriodMs / 1000,
			expire_at_timestamp: expireAt.toISOString()
		};
		showDisableConfirm = true;
	}

	function parseGracePeriod(durationStr: string): number {
		// Parse Go duration format like "30m", "2h", etc.
		const match = durationStr.match(/^(\d+)(ms|s|m|h)$/);
		if (!match) return 1800000; // default to 30 minutes

		const value = parseInt(match[1], 10);
		const unit = match[2];

		const multipliers: Record<string, number> = {
			ms: 1,
			s: 1000,
			m: 60000,
			h: 3600000
		};

		return value * (multipliers[unit] || 1);
	}

	async function handleDisable() {
		actionBusy = true;
		try {
			await disableUser(userId);
			await loadUser();
			showDisableConfirm = false;
			disableConsequences = null;
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to disable user';
		} finally {
			actionBusy = false;
		}
	}

	async function handleEnable() {
		actionBusy = true;
		try {
			await enableUser(userId);
			await loadUser();
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to enable user';
		} finally {
			actionBusy = false;
		}
	}

	onMount(() => {
		loadConfig();
		loadUser();
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
					<Button variant="primary" disabled={actionBusy} onclick={handleEnable}>
						{actionBusy ? 'Enabling...' : 'Re-enable'}
					</Button>
				{:else}
					<Button
						variant="danger"
						testid="disable-user"
						disabled={actionBusy || !config}
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
						Their <strong>{disableConsequences.service_enrollment_count}</strong>
						active service enrollment(s) will expire at
						<strong>{new Date(disableConsequences.expire_at_timestamp).toLocaleString()}</strong>,
						allowing running services time to rotate credentials.
					</p>
					<div class="flex justify-end gap-2">
						<Button
							variant="ghost"
							disabled={actionBusy}
							onclick={() => {
								showDisableConfirm = false;
								disableConsequences = null;
							}}
						>
							Cancel
						</Button>
						<Button
							variant="danger"
							testid="confirm-disable"
							disabled={actionBusy}
							onclick={handleDisable}
						>
							{actionBusy ? 'Disabling...' : 'Disable'}
						</Button>
					</div>
				</div>
			</div>
		{/if}
	{/if}
</div>
