<script lang="ts">
	import { listRetrievals } from '$lib/api/endpoints';
	import { ApiError } from '$lib/api/client';
	import type { CertificateRecord, EnrollmentRetrievalsResponse } from '$lib/api/types';
	import { formatDateTime, formatDuration } from '$lib/format';
	import DetailRow from './DetailRow.svelte';
	import Icon from './Icon.svelte';
	import MonoChip from './MonoChip.svelte';
	import SectionLabel from './SectionLabel.svelte';
	import StatusBadge from './StatusBadge.svelte';
	import TypeChip from './TypeChip.svelte';

	interface Props {
		cert: CertificateRecord;
		onclosed: () => void;
	}

	let { cert, onclosed }: Props = $props();
	let dialogEl = $state<HTMLDialogElement | undefined>(undefined);
	let copied = $state(false);
	let retrievals = $state<EnrollmentRetrievalsResponse | null>(null);

	// Keyed on the certificate so opening a different row re-opens the
	// dialog: the component is kept mounted across rows, and an effect that
	// only depended on the element would have fired once and never again.
	$effect(() => {
		void cert.id;
		if (dialogEl && !dialogEl.open) {
			dialogEl.showModal();
		}
	});

	// Fetch retrieval log for service-type certificates. The cert.id serves as
	// a proxy for the request ID until the server includes request_id in the
	// certificate response.
	$effect(() => {
		retrievals = null;

		if (cert.type !== 'service') {
			return;
		}

		const controller = new AbortController();

		listRetrievals(cert.id, controller.signal)
			.then((result) => {
				// Guard against stale responses if cert changes while loading.
				if (cert.id !== cert.id) {
					return;
				}
				retrievals = result;
			})
			.catch((cause) => {
				if (controller.signal.aborted) {
					return;
				}
				// 404 means no enrollment exists for this cert; 403 means we're not
				// authorized. Both are expected in some cases and shouldn't show an error.
				if (cause instanceof ApiError && (cause.isNotFound || cause.isForbidden)) {
					retrievals = null;
				} else {
					// For unexpected errors, also don't show anything — the modal still works
					retrievals = null;
				}
			});

		return () => controller.abort();
	});

	// Every close path — the button, Escape, the backdrop — arrives here,
	// which is what keeps the ?modal= parameter in step with the dialog.
	// Closing only from the button would leave the URL claiming a modal was
	// open after Escape, and the row would then refuse to reopen it.
	function handleClosed() {
		copied = false;
		onclosed();
	}

	/** copyLink puts this certificate's own URL on the clipboard — the
	 * ?modal= parameter makes the open dialog addressable. */
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

	const principals = $derived(
		cert.principals
			.split(',')
			.map((p) => p.trim())
			.filter((p) => p.length > 0)
	);

	// The short form of the id, for the corner of the panel — enough to tell
	// two certificates apart when comparing against a log line. Labelled
	// rather than prefixed with a bare "#", which reads as a colour code.
	// The full id is on the title attribute for anyone who needs all of it.
	const shortId = $derived(cert.id.slice(0, 5));

	const validFor = $derived(
		Math.floor((new Date(cert.expires_at).getTime() - new Date(cert.issued_at).getTime()) / 1000)
	);

	// A certificate exists because a request was approved; an absent decision
	// record means the audit trail predates it, not that it was denied.
	const decision = $derived(cert.decided_by_outcome === 'denied' ? 'denied' : 'approved');
	const decidedBy = $derived(
		cert.decided_by_email || cert.decided_by_username || cert.decided_by_subject
	);
</script>

<dialog
	bind:this={dialogEl}
	onclose={handleClosed}
	aria-label="Certificate details"
	class="modal-dialog z-50"
>
	<div
		class="flex max-h-[88vh] w-full max-w-[600px] flex-col gap-[18px] overflow-y-auto rounded-xl border border-border-subtle bg-surface p-6 shadow-lg"
	>
		<div class="flex items-center justify-between gap-3">
			<TypeChip type={cert.type} />
			<div class="flex items-center gap-2">
				<button
					type="button"
					onclick={copyLink}
					aria-label={copied ? 'Link copied' : 'Copy link to this certificate'}
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
			<StatusBadge status={decision} />
			<span class="text-xs text-ink-muted" title={cert.id}>
				ID <span class="font-mono">{shortId}</span>
			</span>
		</div>

		{#if decidedBy}
			<!-- Who decided, from where, and when — in the banner rather than
			     buried among the fields, because on an audit record it is the
			     first thing anyone reviewing it came to find. -->
			<div class="flex gap-2.5 rounded-lg bg-surface-muted px-3.5 py-3 text-[13px] leading-normal">
				<Icon name="user" size="sm" class="mt-px flex-shrink-0 text-ink-muted" />
				<span>
					{decision === 'denied' ? 'Denied' : 'Approved'} by <strong>{decidedBy}</strong>
					{#if cert.decided_source_ip}
						from <span class="font-mono">{cert.decided_source_ip}</span>
					{/if}
					{#if cert.decided_at}
						· {formatDateTime(cert.decided_at)}
					{/if}
				</span>
			</div>
		{/if}

		<div>
			<SectionLabel>Details</SectionLabel>
			<dl class="divide-y divide-border-subtle">
				{#if principals.length > 0}
					<DetailRow label="Principals">
						<span class="flex flex-wrap gap-1.5">
							{#each principals as principal, index (index)}
								<MonoChip>{principal}</MonoChip>
							{/each}
						</span>
					</DetailRow>
				{/if}

				{#if validFor > 0}
					<DetailRow label="Valid for">{formatDuration(validFor)}</DetailRow>
				{/if}

				<DetailRow label="Issued at">{formatDateTime(cert.issued_at)}</DetailRow>
				<DetailRow label="Expires at">{formatDateTime(cert.expires_at)}</DetailRow>
				<DetailRow label="Serial number" mono>{cert.serial_number}</DetailRow>
				<DetailRow label="Key fingerprint" mono>{cert.public_key_fingerprint}</DetailRow>
				<DetailRow label="Key ID" mono>{cert.key_id}</DetailRow>
			</dl>
		</div>

		{#if cert.type === 'service' && retrievals}
			<div>
				<SectionLabel>Retrievals</SectionLabel>
				{#if retrievals.retrievals.length === 0}
					<p class="text-[13px] text-ink-muted">Never retrieved.</p>
				{:else}
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
