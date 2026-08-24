<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { getAdminUser, disableUser, enableUser } from '$lib/api/endpoints';
	import Button from '$lib/components/Button.svelte';
	import type { AdminUserDetail } from '$lib/api/types';

	let user: AdminUserDetail | null = $state(null);
	let error: string | null = $state(null);
	let busy = $state(false);
	let actionBusy = $state(false);
	let showDisableConfirm = $state(false);

	const userId = page.params.id;

	async function loadUser() {
		busy = true;
		error = null;
		try {
			user = await getAdminUser(userId);
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to load user';
		} finally {
			busy = false;
		}
	}

	async function handleDisable() {
		actionBusy = true;
		try {
			await disableUser(userId);
			await loadUser();
			showDisableConfirm = false;
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

	onMount(loadUser);
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
						disabled={actionBusy}
						onclick={() => (showDisableConfirm = true)}
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
					<div>
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
		{#if showDisableConfirm}
			<div class="fixed inset-0 flex items-center justify-center bg-black/50 p-4">
				<div class="w-full max-w-md rounded-lg bg-surface p-6 shadow-lg">
					<h3 class="mb-4 text-lg font-semibold text-ink">Disable User?</h3>
					<p class="mb-4 text-sm text-ink-muted">
						This will prevent <strong>{user.username}</strong> from authenticating immediately.
						Their <strong>{user.service_enrollment_count}</strong> active service enrollment(s) will expire
						after the configured grace period, allowing running services time to rotate credentials.
					</p>
					<div class="flex justify-end gap-2">
						<Button
							variant="ghost"
							disabled={actionBusy}
							onclick={() => (showDisableConfirm = false)}
						>
							Cancel
						</Button>
						<Button variant="danger" disabled={actionBusy} onclick={handleDisable}>
							{actionBusy ? 'Disabling...' : 'Disable'}
						</Button>
					</div>
				</div>
			</div>
		{/if}
	{/if}
</div>
