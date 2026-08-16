<script lang="ts">
	import type { RequestDetail } from '$lib/api/types';
	import {
		anyTrimmed,
		approvalBlockedReason,
		criticalOptionDiff,
		extensionDiff,
		type BlockedReason
	} from '$lib/approval';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';
	import DetailRow from '$lib/components/DetailRow.svelte';
	import OptionDiffList from '$lib/components/OptionDiffList.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import { formatDateTime, formatDuration } from '$lib/format';

	// Everything this component shows arrives as props, and both decisions
	// leave as callbacks. Fetching and posting stay in +page.svelte so this
	// can be rendered against a literal RequestDetail in tests — the cases
	// that matter (trimmed options, someone else's request, an
	// already-resolved one, an expired one) are all just different values
	// of `detail`.
	interface Props {
		detail: RequestDetail;
		/** True while an approve or deny call is in flight. */
		busy?: boolean;
		/** A failed approve/deny, as opposed to a failed load. */
		actionError?: string | null;
		/** Set once a decision has been recorded, to replace the buttons. */
		outcome?: 'approved' | 'denied' | null;
		onapprove: () => void;
		ondeny: () => void;
	}

	let {
		detail,
		busy = false,
		actionError = null,
		outcome = null,
		onapprove,
		ondeny
	}: Props = $props();

	const extensions = $derived(extensionDiff(detail.requested, detail.granted));
	const criticalOptions = $derived(criticalOptionDiff(detail.requested, detail.granted));
	const narrowed = $derived(anyTrimmed(extensions) || anyTrimmed(criticalOptions));
	const blocked = $derived(approvalBlockedReason(detail));

	// PAM authenticates a single local operation (e.g. `sudo`) to
	// pam_ssoossh, not an interactive SSH session — "requesting an SSH
	// certificate" would misdescribe what's actually being authorized, so
	// the heading says what this type of certificate is for instead (see
	// docs/release-phase4-pam-server.md, item 7).
	const cardCopy = $derived(
		detail.type === 'pam'
			? {
					title: 'Approve a PAM authentication',
					description:
						"Review before authorizing this local operation. This certificate is for a single sudo (or other PAM) call on the client's machine, not an interactive SSH session."
				}
			: {
					title: 'Approve a certificate request',
					description: 'Review exactly what this certificate will grant before authorizing it.'
				}
	);

	// Wording per blocked reason, so the page explains why there is no
	// button rather than just not having one.
	const blockedText: Record<BlockedReason, string> = {
		'not-yours':
			'This request belongs to another account. Only the account that opened it can approve or deny it.',
		'in-progress':
			'This request has already been approved and its certificate is being signed. Nothing further is needed here.',
		'already-resolved':
			'This request is closed. Certificate requests are short-lived by design — run the client again to start a new one.'
	};
</script>

<Card title={cardCopy.title} description={cardCopy.description} testid="approval-view">
	<dl class="divide-y divide-border-subtle">
		<DetailRow label="Status"><StatusBadge status={detail.status} /></DetailRow>
		<DetailRow label="Certificate type">{detail.type}</DetailRow>
		<DetailRow label="Principals" mono>
			{#if detail.principals.length > 0}
				{detail.principals.join(', ')}
			{:else}
				<span class="font-sans text-ink-muted">none</span>
			{/if}
		</DetailRow>
		<DetailRow label="Valid for">{formatDuration(detail.valid_seconds)}</DetailRow>
		<DetailRow label="Requested from" mono>{detail.source_ip}</DetailRow>
		{#if detail.hostname}
			<DetailRow label="Hostname" mono>{detail.hostname}</DetailRow>
		{/if}
		<DetailRow label="Requested at">{formatDateTime(detail.created_at)}</DetailRow>
		<DetailRow label="Public key" mono>{detail.public_key}</DetailRow>
	</dl>

	<!-- The granted set, not the requested set. Options this deployment does
	     not permit are trimmed rather than rejected, so the two can differ and
	     the difference has to be visible before anyone approves (root
	     CLAUDE.md, Hard Constraints). -->
	<div class="mt-6 space-y-6">
		<div>
			<h3 class="text-sm font-semibold">Extensions this certificate will carry</h3>
			<div class="mt-2">
				<OptionDiffList entries={extensions} emptyLabel="No extensions requested." />
			</div>
		</div>

		<div>
			<h3 class="text-sm font-semibold">Critical options</h3>
			<div class="mt-2">
				<OptionDiffList entries={criticalOptions} emptyLabel="No critical options requested." />
			</div>
		</div>

		{#if narrowed}
			<Alert variant="warning" title="Less than was requested" testid="narrowed-warning">
				This server does not permit everything the client asked for. The struck-through entries
				above will not be in the certificate.
			</Alert>
		{/if}
	</div>

	{#snippet footer()}
		{#if outcome === 'approved'}
			<Alert variant="info" title="Approved" testid="outcome-approved">
				The certificate is being signed and will reach the waiting client on its own connection —
				you can close this page.
			</Alert>
		{:else if outcome === 'denied'}
			<Alert variant="warning" title="Denied" testid="outcome-denied">
				The waiting client has been told, and no certificate was issued.
			</Alert>
		{:else if blocked}
			<Alert variant={blocked === 'in-progress' ? 'info' : 'warning'} testid="blocked-{blocked}">
				{blockedText[blocked]}
			</Alert>
		{:else}
			<div class="space-y-3">
				{#if actionError}
					<Alert variant="error" title="That did not go through">{actionError}</Alert>
				{/if}
				<div class="flex flex-wrap gap-3">
					<Button testid="approve-button" disabled={busy} onclick={onapprove}
						>{busy ? 'Working…' : 'Approve'}</Button
					>
					<Button testid="deny-button" variant="danger" disabled={busy} onclick={ondeny}
						>Deny</Button
					>
				</div>
			</div>
		{/if}
	{/snippet}
</Card>
