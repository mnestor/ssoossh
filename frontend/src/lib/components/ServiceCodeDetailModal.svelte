<script lang="ts">
	import { ApiError } from '$lib/api/client';
	import { listRetrievals } from '$lib/api/endpoints';
	import type { EnrollmentRetrievalsResponse, ServiceEnrollment } from '$lib/api/types';
	import { expiryLabel, formatDateTime, formatDuration, isExpired } from '$lib/format';
	import DetailRow from './DetailRow.svelte';
	import Icon from './Icon.svelte';
	import MonoChip from './MonoChip.svelte';
	import SectionLabel from './SectionLabel.svelte';
	import TypeChip from './TypeChip.svelte';

	// One service enrollment in full, opened from the list. Read-only, and
	// deliberately unable to show the code: `service enroll` prints it once
	// and the server has no endpoint that returns one, so the answer here is
	// what the code grants and how long it lasts.
	//
	// Read-only in the stronger sense too: the code belongs to its service
	// account, so there is no owner to transfer it to and nothing here to
	// act on.
	interface Props {
		enrollment: ServiceEnrollment;
		/** Pinned clock, so the remaining lifetime matches the row behind it. */
		now?: Date;
		onclosed: () => void;
	}

	let { enrollment, now = new Date(), onclosed }: Props = $props();

	let dialogEl = $state<HTMLDialogElement | undefined>(undefined);
	let copied = $state(false);
	let retrievals = $state<EnrollmentRetrievalsResponse | null>(null);

	// Keyed on the enrollment so opening a different row re-opens the dialog:
	// the component stays mounted across rows, and an effect depending only on
	// the element would fire once and never again.
	$effect(() => {
		void enrollment.id;
		if (dialogEl && !dialogEl.open) {
			dialogEl.showModal();
		}
	});

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
				// stand on their own, and the rest of the panel is unaffected.
				if (!(cause instanceof ApiError)) {
					return;
				}
				retrievals = null;
			});

		return () => controller.abort();
	});

	// Every close path — the button, Escape, the backdrop — arrives here,
	// which is what keeps the ?modal= parameter in step with the dialog.
	function handleClosed() {
		copied = false;
		onclosed();
	}

	/** copyLink puts this enrollment's own URL on the clipboard — the
	 * ?modal= parameter makes the open panel addressable. */
	async function copyLink() {
		try {
			await navigator.clipboard.writeText(window.location.href);
			copied = true;
		} catch {
			// A denied clipboard permission is not worth an error state: the
			// URL is in the address bar either way.
			copied = false;
		}
	}

	// The service account, which is both the certificate principal and who
	// owns this code. principals is the fallback for a row from before the
	// account had its own field.
	const subject = $derived(
		enrollment.service_account ||
			(enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account')
	);

	const expired = $derived(isExpired(enrollment.expires_at, now));

	// The short form of the id, for the corner of the panel — enough to tell
	// two enrollments apart against a log line. The full id is on the title
	// attribute for anyone who needs all of it.
	const shortId = $derived(enrollment.id.slice(0, 5));

	const certificateLifetime = $derived(
		enrollment.certificate_valid_seconds === undefined
			? 'until the code expires'
			: formatDuration(enrollment.certificate_valid_seconds)
	);

	// The server caps the log it returns, so the panel has to say what it is
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
					onclick={copyLink}
					aria-label={copied ? 'Link copied' : 'Copy link to this service code'}
					class="inline-flex h-[30px] w-[30px] items-center justify-center rounded-[7px] border border-border-subtle text-ink-muted transition hover:bg-surface-muted"
					class:text-granted={copied}
				>
					<Icon name={copied ? 'check' : 'link'} size="sm" />
				</button>
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

		<!-- The account leads, the way the decider leads on a certificate:
		     everything else on the panel is a property of the grant made to
		     this one account -- including who can see this panel at all. -->
		<div class="flex gap-2.5 rounded-lg bg-surface-muted px-3.5 py-3 text-[13px] leading-normal">
			<Icon name="user" size="sm" class="mt-px flex-shrink-0 text-ink-muted" />
			<span>
				Mints certificates for <strong class="font-mono" data-testid="service-code-account"
					>{subject}</strong
				>, its only principal, fixed at approval. Everyone with access to that account sees and
				manages this code.
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
				<DetailRow label="Approved">{formatDateTime(enrollment.created_at)}</DetailRow>
				<DetailRow label="Approved by">{enrollment.approved_by_username || '—'}</DetailRow>
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
	</div>
</dialog>
