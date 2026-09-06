<script lang="ts">
	import { ApiError } from '$lib/api/client';
	import {
		expireOwnEnrollment,
		listRetrievals,
		setEnrollmentNotificationEmail
	} from '$lib/api/endpoints';
	import type { EnrollmentRetrievalsResponse, ServiceEnrollment } from '$lib/api/types';
	import { errorMessage } from '$lib/auth';
	import { expiryLabel, formatDateTime, formatDuration, isExpired } from '$lib/format';
	import { session } from '$lib/session.svelte';
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

	// One service enrollment in full, the body of /service-codes/[id].
	// Deliberately unable to show the code: `service enroll` prints it once
	// and the server has no endpoint that returns one, so the answer here is
	// what the code grants and how long it lasts.
	//
	// The one thing here that can be changed is where notifications about the
	// code go. Everything else was fixed at approval, and the code belongs to
	// its service account, so there is no owner to transfer it to.
	//
	// A page rather than the dialog this used to be: this is a screenful of
	// fields, a redemption log and two controls, which is a page's worth of
	// reading — and inside a dialog none of it could be linked to, reloaded,
	// or reached with the browser's own Back.
	interface Props {
		enrollment: ServiceEnrollment;
		/** Pinned clock, so the remaining lifetime matches the list it came from. */
		now?: Date;
		/** Called once the code is retired, so the page can leave. */
		onexpired?: () => void;
	}

	let { enrollment, now = new Date(), onexpired }: Props = $props();

	let retrievals = $state<EnrollmentRetrievalsResponse | null>(null);

	// The address form's own state. emailDraft is reset from the enrollment
	// whenever a different code is shown, so a draft abandoned on one code
	// does not follow the reader to the next.
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
			// Rendered from the answer rather than the draft: the server trims,
			// and the page should show what is actually stored.
			storedEmail = stored.notification_email;
			emailDraft = storedEmail;
			emailSaved = true;
		} catch (cause) {
			emailError = errorMessage(cause);
		} finally {
			savingEmail = false;
		}
	}

	// The redemption log, keyed on the request the enrollment was approved
	// from. The row already carries the count and the last redemption; this
	// is the detail behind them — when, from where, and whether a certificate
	// actually came out.
	$effect(() => {
		retrievals = null;

		const requestID = enrollment.certificate_request_id;
		if (!requestID) {
			return;
		}

		const controller = new AbortController();

		listRetrievals(requestID, controller.signal)
			.then((result) => {
				retrievals = result;
			})
			.catch((cause) => {
				if (controller.signal.aborted) {
					return;
				}
				// A 404 (no enrollment for that request) or 403 (someone else's)
				// is not an error worth a banner: the summary counts on the row
				// stand on their own, and the rest of the page is unaffected.
				if (!(cause instanceof ApiError)) {
					return;
				}
				retrievals = null;
			});

		return () => controller.abort();
	});

	// The service account, which is both the certificate principal and who
	// owns this code. principals is the fallback for a row from before the
	// account had its own field.
	const subject = $derived(
		enrollment.service_account ||
			(enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account')
	);

	const expired = $derived(isExpired(enrollment.expires_at, now));

	const certificateLifetime = $derived(
		enrollment.certificate_valid_seconds === undefined
			? 'until the code expires'
			: formatDuration(enrollment.certificate_valid_seconds)
	);

	// The server caps the log it returns, so the page has to say what it is
	// showing a slice of rather than let the last row read as the first
	// redemption.
	const truncated = $derived(!!retrievals && retrievals.total > retrievals.retrievals.length);

	const hasOptions = $derived(
		enrollment.options.extensions.length > 0 ||
			!!enrollment.options.force_command ||
			!!enrollment.options.source_addresses?.length ||
			enrollment.options.no_touch_required
	);
</script>

<div class="flex flex-col gap-5" data-testid="service-code-detail">
	<!-- The identity strip, the same one the certificate page opens with:
	     what kind of thing this is, whether it still works, and the id to
	     quote in a ticket. -->
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

	<div>
		<SectionLabel>What it hands out</SectionLabel>
		<dl class="divide-y divide-border-subtle">
			<!-- The principal leads the way the decider leads on a certificate:
			     it is the account every certificate this code mints is for, fixed
			     at approval, and everything below is a property of that grant. -->
			<DetailRow label="Principal" mono>
				<span data-testid="service-code-account">{subject}</span>
			</DetailRow>
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
			<DetailRow label="Approved">{formatDateTime(enrollment.created_at)}</DetailRow>
			<DetailRow label="Approved by">{enrollment.approved_by_username || '—'}</DetailRow>
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

	<!-- Immediately above the notification address, because the two answer
	     the same question from opposite ends: this is who the code reaches by
	     default, and the field below is how to send it somewhere else
	     instead. -->
	<AccountHoldersPanel
		enrollmentId={enrollment.id}
		serviceAccount={enrollment.service_account}
		viewerUsername={session.user?.username ?? ''}
	/>

	<!-- The only editable thing here. It exists for the cases fan-out cannot
	     serve: an account whose holders have never logged in reaches nobody,
	     and a large holder set turns every redemption into a mailshot where a
	     team alias would do. -->
	<div>
		<SectionLabel>Notifications</SectionLabel>
		<p class="mb-2 text-[13px] text-ink-muted">
			{#if storedEmail}
				Notifications about this code go to
				<span class="font-mono">{storedEmail}</span>. Clear the field to send them to everyone with
				access to the account instead.
			{:else}
				Notifications about this code go to everyone with access to
				<span class="font-mono">{subject}</span>. Set an address to send them to one place instead.
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
				{savingEmail ? 'Saving…' : 'Save'}
			</Button>
		</div>
		{#if emailError}
			<div class="mt-2">
				<Alert variant="error" title="That did not save" testid="notification-email-error">
					{emailError}
				</Alert>
			</div>
		{:else if emailSaved}
			<p class="mt-2 text-[13px] text-granted" data-testid="notification-email-saved">
				{emailDraft ? 'Saved.' : 'Cleared — notifications go to everyone with access again.'}
			</p>
		{/if}
	</div>

	{#if retrievals}
		<div>
			<SectionLabel>Retrievals</SectionLabel>
			{#if retrievals.retrievals.length === 0}
				<p class="text-[13px] text-ink-muted">Never retrieved.</p>
			{:else}
				{#if truncated}
					<!-- Said before the list, not after it: a reader who stops
					     scrolling partway through still needs to know this is the
					     recent end of a longer history, not all of it. -->
					<p class="mb-2 text-[13px] text-ink-muted">
						The {retrievals.retrievals.length} most recent of {retrievals.total} redemptions.
					</p>
				{/if}
				<dl class="divide-y divide-border-subtle">
					<!-- Keyed by position: reusable codes mean two redemptions can
					     land in the same second, and a keyed each throws on a
					     duplicate key rather than rendering it. -->
					{#each retrievals.retrievals as retrieval, index (index)}
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
			{/if}
		</div>
	{/if}

	<!-- Retiring the code, for the people who live with it. An enrollment
	     belongs to its service account rather than to whoever approved it, so
	     a holder is the one who knows the job behind it has been
	     decommissioned — and until now they had to ask an admin to retire it
	     for them. An already-expired code needs no control: the outcome is
	     already true. -->
	{#if !expired}
		<div>
			<SectionLabel>Retire this code</SectionLabel>
			<div class="max-w-[560px]">
				<ExpireCodeAction
					testid="expire-code"
					expire={(reason) => expireOwnEnrollment(enrollment.id, reason)}
					onexpired={() => onexpired?.()}
				/>
			</div>
		</div>
	{/if}
</div>
