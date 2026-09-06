<script lang="ts">
	import { expireEnrollment, setEnrollmentNotificationEmail } from '$lib/api/endpoints';
	import type { AdminEnrollmentDetail } from '$lib/api/endpoints';
	import { errorMessage } from '$lib/auth';
	import { expiryLabel, formatDateTime, formatDuration, isExpired } from '$lib/format';
	import AccountHoldersPanel from './AccountHoldersPanel.svelte';
	import ExpireCodeAction from './ExpireCodeAction.svelte';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import CopyableId from './CopyableId.svelte';
	import DetailRow from './DetailRow.svelte';
	import Icon from './Icon.svelte';
	import MonoChip from './MonoChip.svelte';
	import SectionLabel from './SectionLabel.svelte';
	import TypeChip from './TypeChip.svelte';

	// One enrollment as an operator sees it, the body of
	// /admin/service-codes/[id].
	//
	// The detail is handed in rather than fetched here: GET
	// /api/admin/enrollments/:id is audited, so a component that fetched on
	// mount would write a second admin.enrollment_viewed event for the one
	// look the route has already recorded.
	//
	// A page rather than the dialog this used to be. The list opens a code by
	// navigating to it, which is what makes an id from a log line, a
	// notification or an audit event openable at all — the dialog could only
	// ever be reached by finding the row again.
	interface Props {
		detail: AdminEnrollmentDetail;
		now?: Date;
		/** Called once the code is retired, so the page can leave. */
		onexpired?: () => void;
	}

	let { detail, now = new Date(), onexpired }: Props = $props();

	const enrollment = $derived(detail.enrollment);

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
			// From the answer, not the draft: the server trims, and the page
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

	const subject = $derived(
		enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account'
	);
	const expired = $derived(isExpired(enrollment.expires_at, now));
	const certificateLifetime = $derived(
		enrollment.certificate_valid_seconds === undefined
			? 'until the code expires'
			: formatDuration(enrollment.certificate_valid_seconds)
	);
	const truncated = $derived(detail.retrievals.length < detail.retrieval_total);
	const hasOptions = $derived(
		enrollment.options.extensions.length > 0 ||
			!!enrollment.options.force_command ||
			!!enrollment.options.source_addresses?.length ||
			enrollment.options.no_touch_required
	);
</script>

<div class="flex flex-col gap-5" data-testid="service-code-detail">
	<!-- The identity strip: what kind of thing this is, whether it still
	     works, and the id to quote in a ticket. -->
	<div
		class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-[10px] border border-border-subtle bg-surface-muted px-4 py-3"
	>
		<TypeChip type="service" />
		{#if expired}
			<span
				class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full bg-surface px-2.5 py-1 text-xs font-semibold text-ink-muted"
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
		<span class="ml-auto"><CopyableId value={enrollment.id} testid="enrollment-id" /></span>
	</div>

	<div class="flex gap-2.5 rounded-lg bg-surface-muted px-3.5 py-3 text-[13px] leading-normal">
		<Icon name="user" size="sm" class="mt-px flex-shrink-0 text-ink-muted" />
		<span>
			Mints certificates for <strong class="font-mono" data-testid="service-code-account"
				>{subject}</strong
			>, its only principal, fixed when approved.
		</span>
	</div>

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
			<DetailRow label="Last redeemed">
				{enrollment.last_retrieved_at ? formatDateTime(enrollment.last_retrieved_at) : '—'}
			</DetailRow>
			<DetailRow label="Redemptions">{enrollment.retrieval_count}</DetailRow>
		</dl>
	</div>

	{#if detail.retrievals.length > 0}
		<div>
			<SectionLabel>Retrievals</SectionLabel>
			{#if truncated}
				<p class="mb-2 text-[13px] text-ink-muted">
					The {detail.retrievals.length} most recent of {detail.retrieval_total} redemptions.
				</p>
			{/if}
			<dl class="divide-y divide-border-subtle">
				{#each detail.retrievals as retrieval, index (index)}
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

	<!-- Immediately above the admin controls, because the first of them
	     redirects notifications away from exactly these people. Provenance is
	     the "Approved by" row above; this is ownership. -->
	<AccountHoldersPanel enrollmentId={enrollment.id} serviceAccount={enrollment.service_account} />

	<!-- Admin controls -->
	<div class="space-y-4 border-t border-border-subtle pt-4">
		<SectionLabel>Admin actions</SectionLabel>

		<!-- The address is editable here as well as on the holder's own page,
		     for the deployment where the account's holders are outside ssoossh
		     entirely and so have no page to set it on. Changing it is
		     audited. -->
		<div class="space-y-2">
			<p class="text-[13px] text-ink-muted">
				{#if storedEmail}
					Notifications about this code go to
					<span class="font-mono">{storedEmail}</span>. Clear the field to send them to everyone
					with access to the account instead.
				{:else}
					Notifications about this code go to everyone with access to the account. Set an address to
					send them to one place instead.
				{/if}
			</p>
			<div class="flex max-w-[560px] flex-wrap items-start gap-2">
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
			<div class="max-w-[560px]">
				<ExpireCodeAction
					testid="admin-expire-code"
					expire={(reason) => expireEnrollment(enrollment.id, reason)}
					onexpired={() => onexpired?.()}
				/>
			</div>
		{/if}
	</div>
</div>
