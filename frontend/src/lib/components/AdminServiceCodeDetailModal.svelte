<script lang="ts">
	import { ApiError } from '$lib/api/client';
	import {
		expireEnrollment,
		getAdminEnrollmentDetail,
		setEnrollmentNotificationEmail
	} from '$lib/api/endpoints';
	import type { AdminEnrollment } from '$lib/api/types';
	import { errorMessage } from '$lib/auth';
	import { expiryLabel, formatDateTime, formatDuration, isExpired } from '$lib/format';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import DetailRow from './DetailRow.svelte';
	import Icon from './Icon.svelte';
	import MonoChip from './MonoChip.svelte';
	import SectionLabel from './SectionLabel.svelte';
	import TypeChip from './TypeChip.svelte';

	interface Props {
		enrollment: AdminEnrollment;
		now?: Date;
		onclosed: () => void;
	}

	let { enrollment, now = new Date(), onclosed }: Props = $props();

	let dialogEl = $state<HTMLDialogElement | undefined>(undefined);
	let detailData = $state<any>(null);
	let detailLoading = $state(true);
	let detailError = $state<string | null>(null);

	let expireConfirm = $state(false);
	let expireError = $state<string | null>(null);
	let expiring = $state(false);

	// The notification address, editable here for the deployment where the
	// account's holders are outside ssoossh entirely and cannot set it
	// themselves. Reset per enrollment so a draft does not follow the reader
	// from one code to the next.
	let emailDraft = $state('');
	let savingEmail = $state(false);
	let emailError = $state<string | null>(null);
	let emailSaved = $state(false);
	let storedEmail = $state('');

	// Both assignments read the prop, never storedEmail: an effect that read
	// the state it also writes would re-run on its own save and wipe the
	// "Saved." line it had just earned.
	$effect(() => {
		const address = enrollment.notification_email ?? '';
		storedEmail = address;
		emailDraft = address;
		emailError = null;
		emailSaved = false;
	});

	const emailDirty = $derived(emailDraft.trim() !== storedEmail);

	/** saveNotificationEmail stores the address, or clears it when empty. */
	async function saveNotificationEmail() {
		savingEmail = true;
		emailError = null;
		emailSaved = false;
		try {
			const stored = await setEnrollmentNotificationEmail(enrollment.id, emailDraft.trim());
			// From the answer, not the draft: the server trims, and the panel
			// should show what is actually stored.
			storedEmail = stored.notification_email;
			emailDraft = storedEmail;
			emailSaved = true;
		} catch (cause) {
			emailError = errorMessage(cause);
		} finally {
			savingEmail = false;
		}
	}

	// Load detail when enrollment changes
	$effect(() => {
		void enrollment.id;
		if (dialogEl && !dialogEl.open) {
			dialogEl.showModal();
		}
	});

	// Load detail data
	$effect(() => {
		detailLoading = true;
		detailError = null;
		const controller = new AbortController();

		getAdminEnrollmentDetail(enrollment.id, controller.signal)
			.then((result) => {
				detailData = result;
				detailLoading = false;
			})
			.catch((cause) => {
				if (controller.signal.aborted) {
					return;
				}
				if (cause instanceof ApiError && cause.status === 404) {
					detailError = 'Enrollment not found';
				} else if (cause instanceof ApiError && cause.status === 403) {
					detailError = 'You do not have permission to view this enrollment';
				} else {
					detailError = cause instanceof Error ? cause.message : 'Failed to load details';
				}
				detailLoading = false;
			});

		return () => controller.abort();
	});

	function handleClosed() {
		expireConfirm = false;
		expireError = null;
		onclosed();
	}

	async function handleExpire() {
		expiring = true;
		expireError = null;

		try {
			await expireEnrollment(enrollment.id);
			// Close after successful expiry
			dialogEl?.close();
		} catch (cause) {
			if (cause instanceof ApiError) {
				if (cause.status === 404) {
					expireError = 'Enrollment not found';
				} else if (cause.status === 403) {
					expireError = 'You do not have permission to expire this enrollment';
				} else {
					expireError = cause.message;
				}
			} else {
				expireError = cause instanceof Error ? cause.message : 'Failed to expire enrollment';
			}
		} finally {
			expiring = false;
		}
	}

	const subject = $derived(
		enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account'
	);
	const expired = $derived(isExpired(enrollment.expires_at, now));
	const shortId = $derived(enrollment.id.slice(0, 5));
	const certificateLifetime = $derived(
		enrollment.certificate_valid_seconds === undefined
			? 'until the code expires'
			: formatDuration(enrollment.certificate_valid_seconds)
	);
	const truncated = $derived(
		detailData && detailData.retrievals.length < detailData.retrieval_total
	);
	const hasOptions = $derived(
		enrollment.options.extensions.length > 0 ||
			!!enrollment.options.force_command ||
			!!enrollment.options.source_addresses?.length ||
			enrollment.options.no_touch_required
	);
</script>

<dialog
	bind:this={dialogEl}
	onclose={handleClosed}
	aria-label="Service code details"
	class="modal-dialog z-50"
>
	<div
		class="flex max-h-[88vh] w-full max-w-[600px] flex-col gap-[18px] overflow-y-auto rounded-xl border border-border-subtle bg-surface p-6 shadow-lg"
	>
		<div class="flex items-center justify-between gap-3">
			<TypeChip type="service" />
			<div class="flex items-center gap-2">
				<button
					type="button"
					onclick={() => dialogEl?.close()}
					aria-label="Close"
					class="inline-flex h-[30px] w-[30px] items-center justify-center rounded-[7px] border border-border-subtle text-ink-muted transition hover:bg-surface-muted"
				>
					<Icon name="x" size="sm" />
				</button>
			</div>
		</div>

		<div class="flex items-center justify-between gap-3">
			{#if expired}
				<span
					class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full bg-surface-muted px-2.5 py-1 text-xs font-semibold text-ink-muted"
				>
					<Icon name="alert-triangle" size="xs" />
					Expired
				</span>
			{:else}
				<span
					class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full bg-granted-surface px-2.5 py-1 text-xs font-semibold text-granted"
				>
					<Icon name="check-circle" size="xs" />
					Active
				</span>
			{/if}
			<span class="text-xs text-ink-muted" title={enrollment.id}>
				ID <span class="font-mono">{shortId}</span>
			</span>
		</div>

		<div class="flex gap-2.5 rounded-lg bg-surface-muted px-3.5 py-3 text-[13px] leading-normal">
			<Icon name="user" size="sm" class="mt-px flex-shrink-0 text-ink-muted" />
			<span>
				Mints certificates for <strong class="font-mono">{subject}</strong>, its only principal,
				fixed when approved.
			</span>
		</div>

		{#if detailLoading}
			<p class="text-sm text-ink-muted">Loading details…</p>
		{:else if detailError}
			<Alert variant="error" title="Could not load details">{detailError}</Alert>
		{:else}
			<div>
				<SectionLabel>What it hands out</SectionLabel>
				<dl class="divide-y divide-border-subtle">
					<DetailRow label="Certificate life" icon="clock">{certificateLifetime}</DetailRow>
					<DetailRow label="Key ID" mono>{enrollment.key_id || '—'}</DetailRow>
					<DetailRow label="Bound key" mono>{enrollment.public_key_fingerprint || '—'}</DetailRow>
				</dl>
			</div>

			<div>
				<SectionLabel>Certificate options</SectionLabel>
				{#if !hasOptions}
					<p class="text-[13px] text-ink-muted">
						No extensions or restrictions: certificates carry the server's defaults.
					</p>
				{:else}
					<dl class="divide-y divide-border-subtle">
						{#if enrollment.options.extensions.length > 0}
							<DetailRow label="Extensions">
								<span class="flex flex-wrap gap-1.5">
									{#each enrollment.options.extensions as extension (extension)}
										<MonoChip>{extension}</MonoChip>
									{/each}
								</span>
							</DetailRow>
						{/if}
						{#if enrollment.options.force_command}
							<DetailRow label="Force command" mono>{enrollment.options.force_command}</DetailRow>
						{/if}
						{#if enrollment.options.source_addresses?.length}
							<DetailRow label="Source addresses">
								<span class="flex flex-wrap gap-1.5">
									{#each enrollment.options.source_addresses as address (address)}
										<MonoChip>{address}</MonoChip>
									{/each}
								</span>
							</DetailRow>
						{/if}
						{#if enrollment.options.no_touch_required}
							<DetailRow label="Touch">not required</DetailRow>
						{/if}
					</dl>
				{/if}
			</div>

			<div>
				<SectionLabel>The code itself</SectionLabel>
				<dl class="divide-y divide-border-subtle">
					<DetailRow label="Approved by"
						>{enrollment.approved_by_username} ({enrollment.approved_by_email})</DetailRow
					>
					<DetailRow label="Approved">{formatDateTime(enrollment.created_at)}</DetailRow>
					<DetailRow label={expired ? 'Stopped working' : 'Stops working'} icon="clock">
						{formatDateTime(enrollment.expires_at)}
						<span class="text-ink-muted">({expiryLabel(enrollment.expires_at, now)})</span>
					</DetailRow>
					<DetailRow label="First redeemed">
						{enrollment.first_redeemed_at ? formatDateTime(enrollment.first_redeemed_at) : '—'}
					</DetailRow>
					<DetailRow label="Redemptions">{enrollment.retrieval_count}</DetailRow>
				</dl>
			</div>

			{#if detailData?.retrievals && detailData.retrievals.length > 0}
				<div>
					<SectionLabel>Retrievals</SectionLabel>
					{#if truncated}
						<p class="mb-2 text-[13px] text-ink-muted">
							The {detailData.retrievals.length} most recent of {detailData.retrieval_total} redemptions.
						</p>
					{/if}
					<dl class="divide-y divide-border-subtle">
						{#each detailData.retrievals as retrieval, index (index)}
							<div class="flex items-center justify-between gap-3 py-3">
								<div>
									<div class="text-[13px]">{formatDateTime(retrieval.retrieved_at)}</div>
									<div class="mt-1 flex items-center gap-1.5">
										<MonoChip>{retrieval.source_ip}</MonoChip>
										{#if !retrieval.succeeded}
											<span class="text-[11px] font-semibold text-danger">Failed</span>
										{/if}
									</div>
								</div>
								<span class="text-[11px] text-ink-muted">
									Serial <span class="font-mono">{retrieval.certificate_serial}</span>
								</span>
							</div>
						{/each}
					</dl>
				</div>
			{/if}

			<!-- Admin controls -->
			<div class="space-y-4 border-t border-border-subtle pt-4">
				<SectionLabel>Admin actions</SectionLabel>

				<!-- The address is editable here as well as on the holder's own
				     page, for the deployment where the account's holders are
				     outside ssoossh entirely and so have no page to set it on.
				     Changing it is audited. -->
				<div class="space-y-2">
					<p class="text-[13px] text-ink-muted">
						{#if storedEmail}
							Notifications about this code go to
							<span class="font-mono">{storedEmail}</span>. Clear the field to send them to everyone
							with access to the account instead.
						{:else}
							Notifications about this code go to everyone with access to the account. Set an
							address to send them to one place instead.
						{/if}
					</p>
					<div class="flex flex-wrap items-start gap-2">
						<label class="flex min-w-[220px] flex-1 flex-col gap-1">
							<span class="sr-only">Notification address</span>
							<input
								type="email"
								bind:value={emailDraft}
								data-testid="notification-email-input"
								placeholder="deploys@example.com"
								disabled={savingEmail}
								class="rounded border border-border-subtle bg-surface px-3 py-2 text-[13px] text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
							/>
						</label>
						<Button
							testid="notification-email-save"
							busy={savingEmail}
							disabled={!emailDirty}
							onclick={saveNotificationEmail}
						>
							{savingEmail ? 'Saving…' : 'Save address'}
						</Button>
					</div>
					{#if emailError}
						<Alert variant="error" title="That did not save" testid="notification-email-error">
							{emailError}
						</Alert>
					{:else if emailSaved}
						<p class="text-[13px] text-granted" data-testid="notification-email-saved">
							{emailDraft ? 'Saved.' : 'Cleared — notifications go to everyone with access again.'}
						</p>
					{/if}
				</div>

				<!-- Expire control -->
				{#if !expired}
					<div class="space-y-2">
						{#if !expireConfirm}
							<Button variant="danger" onclick={() => (expireConfirm = true)}>
								Expire this code
							</Button>
						{:else}
							<div class="space-y-2 rounded-lg bg-danger-surface p-3">
								<p class="text-[13px] text-ink">
									Expiring this code will prevent further certificate retrievals. Certificates
									already issued will continue to work until they expire on their own.
								</p>
								<div class="flex gap-2">
									<Button variant="danger" disabled={expiring} onclick={handleExpire}>
										{expiring ? 'Expiring…' : 'Confirm expiry'}
									</Button>
									<Button
										variant="ghost"
										disabled={expiring}
										onclick={() => (expireConfirm = false)}
									>
										Cancel
									</Button>
								</div>
								{#if expireError}
									<Alert variant="error" title="Expiry failed">{expireError}</Alert>
								{/if}
							</div>
						{/if}
					</div>
				{/if}
			</div>
		{/if}
	</div>
</dialog>
