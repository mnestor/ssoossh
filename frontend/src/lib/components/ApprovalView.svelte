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
	import Icon from '$lib/components/Icon.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import OptionDiffList from '$lib/components/OptionDiffList.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import SectionLabel from '$lib/components/SectionLabel.svelte';
	import StatusBadge from '$lib/components/StatusBadge.svelte';
	import TypeChip from '$lib/components/TypeChip.svelte';
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
		/** Service accounts available to this approver (for service-type requests). */
		serviceAccounts?: string[];
		/** Selected service account for approval (for service-type requests). */
		selectedServiceAccount?: string | null;
		/**
		 * Optional address every notification about the resulting enrollment
		 * goes to, instead of fanning out to every holder of the service
		 * account (service-type requests only).
		 */
		notificationEmail?: string;
		/** Principals available to this approver (for user-type requests). */
		userPrincipals?: string[];
		/** Selected principals for approval (for user-type requests). */
		selectedPrincipals?: string[];
		onapprove: () => void;
		ondeny: () => void;
	}

	let {
		detail,
		busy = false,
		actionError = null,
		outcome = null,
		serviceAccounts = [],
		selectedServiceAccount = $bindable(),
		notificationEmail = $bindable(''),
		userPrincipals = [],
		// A fallback, like userPrincipals and serviceAccounts above. The prop
		// is declared optional and the approve button reads
		// selectedPrincipals.length, so without one any consumer that omits
		// it crashes the component on render. The approve route always binds
		// it, which is why nothing caught this.
		selectedPrincipals = $bindable([]),
		onapprove,
		ondeny
	}: Props = $props();

	// Live region for announcing action outcomes to screen readers.
	let liveMessage = $state('');

	const extensions = $derived(extensionDiff(detail.requested, detail.granted));
	const criticalOptions = $derived(criticalOptionDiff(detail.requested, detail.granted));
	const narrowed = $derived(anyTrimmed(extensions) || anyTrimmed(criticalOptions));
	const blocked = $derived(approvalBlockedReason(detail));

	// Announce outcomes to screen readers.
	$effect(() => {
		if (outcome === 'approved') {
			liveMessage = 'Certificate approved and is being signed';
		} else if (outcome === 'denied') {
			liveMessage = 'Certificate request denied';
		}
	});

	const hasDecisionRecord = $derived(!!detail.decided_at);
	const isServiceRequest = $derived(detail.type === 'service');
	const isUserRequest = $derived(detail.type === 'user');
	const hasServiceAccounts = $derived(serviceAccounts.length > 0);
	const hasPrincipals = $derived(userPrincipals.length > 0);

	// The short form of the request id, for the corner of the card — enough
	// to tell two requests apart when comparing against a log line. Labelled
	// rather than prefixed with a bare "#", which reads as a colour code.
	// The full id is on the title attribute for anyone who needs all of it.
	const shortId = $derived(detail.id.slice(0, 5));

	// PAM authenticates a single local operation (e.g. `sudo`) to
	// pam_ssoossh, not an interactive SSH session — "requesting an SSH
	// certificate" would misdescribe what's actually being authorized, so
	// the heading says what this type of certificate is for instead (see
	// docs/guide/features.md, PAM).
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
			'This request is closed. Certificate requests are short-lived by design — run the client again to start a new one.',
		'no-service-accounts': 'You have no service accounts to approve this for.'
	};
</script>

<div class="flex w-full max-w-[560px] flex-col gap-4">
	<PageHeading eyebrow="Certificate request" title={cardCopy.title} />

	<Card testid="approval-view">
		<!-- Live region for screen reader announcements of action outcomes. -->
		<div aria-live="polite" aria-atomic="true" class="sr-only">{liveMessage}</div>

		<!-- Status, type and id read together at the top: what state this
		     request is in, what kind it is, and which one it is. -->
		<div class="flex items-center justify-between gap-3 pb-4">
			<div class="flex items-center gap-2">
				<StatusBadge status={detail.status} />
				<TypeChip type={detail.type} />
			</div>
			<span class="text-xs text-ink-muted" title={detail.id}>
				Request <span class="font-mono">{shortId}</span>
			</span>
		</div>

		<p class="pb-4 text-[13px] text-ink-muted">{cardCopy.description}</p>

		<dl class="divide-y divide-border-subtle">
			<DetailRow label="Principals">
				{#if detail.principals.length > 0}
					<span class="flex flex-wrap gap-1.5">
						<!-- Keyed by position: a request carries whatever the client sent,
					     and nothing guarantees those values are distinct. -->
						{#each detail.principals as principal, index (index)}
							<MonoChip>{principal}</MonoChip>
						{/each}
					</span>
				{:else}
					<span class="font-sans text-ink-muted">none</span>
				{/if}
			</DetailRow>
			{#if detail.target_account}
				<!-- PAM only. The account the sudo is being attempted as, reported
				     by the client rather than proven, and deliberately not one of
				     the principals above: the certificate names the approver, and
				     the host's principals-map decides whether that authorizes this
				     account. Shown because without it the approver cannot see what
				     they are actually authorizing. -->
				<DetailRow label="Attempting to act as">
					<span class="flex flex-wrap items-center gap-1.5">
						<MonoChip>{detail.target_account}</MonoChip>
						<span class="font-sans text-ink-muted">reported by the client</span>
					</span>
				</DetailRow>
			{/if}
			<DetailRow label="Valid for">{formatDuration(detail.valid_seconds)}</DetailRow>
			<DetailRow label="Requested from">
				<MonoChip>{detail.source_ip}</MonoChip>
			</DetailRow>
			{#if detail.local_username || detail.local_hostname}
				<DetailRow label="Client" mono>
					{detail.local_username}{detail.local_username && detail.local_hostname
						? '@'
						: ''}{detail.local_hostname}
				</DetailRow>
			{/if}
			{#if (detail.requested.source_addresses ?? []).length > 0}
				<DetailRow label="Registered IPs">
					<span class="flex flex-wrap gap-1.5">
						{#each detail.requested.source_addresses ?? [] as address, index (index)}
							<MonoChip>{address}</MonoChip>
						{/each}
					</span>
				</DetailRow>
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
				<SectionLabel>Extensions this certificate will carry</SectionLabel>
				<OptionDiffList entries={extensions} emptyLabel="No extensions requested." />
			</div>

			<div>
				<SectionLabel>Critical options</SectionLabel>
				<OptionDiffList entries={criticalOptions} emptyLabel="No critical options requested." />
			</div>

			{#if narrowed}
				<Alert variant="warning" title="Less than was requested" testid="narrowed-warning">
					This server does not permit everything the client asked for. The struck-through entries
					above will not be in the certificate.
				</Alert>
			{/if}

			{#if hasDecisionRecord}
				<div>
					<SectionLabel>Decision record</SectionLabel>
					<dl class="divide-y divide-border-subtle">
						<DetailRow label="Decision">{detail.decided_by_outcome}</DetailRow>
						<DetailRow label="Decided by">
							{detail.decided_by_username || detail.decided_by_subject || 'Unknown'}
						</DetailRow>
						{#if detail.decided_by_email}
							<DetailRow label="Email">{detail.decided_by_email}</DetailRow>
						{/if}
						{#if detail.decided_source_ip}
							<DetailRow label="From IP" mono>{detail.decided_source_ip}</DetailRow>
						{/if}
						{#if detail.decided_at}
							<DetailRow label="Decided at">{formatDateTime(detail.decided_at)}</DetailRow>
						{/if}
					</dl>
				</div>
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
			{:else if isServiceRequest && !hasServiceAccounts}
				<Alert variant="warning" testid="blocked-no-service-accounts">
					{blockedText['no-service-accounts']}
				</Alert>
			{:else if blocked}
				<Alert variant={blocked === 'in-progress' ? 'info' : 'warning'} testid="blocked-{blocked}">
					{blockedText[blocked]}
				</Alert>
			{:else}
				<div class="space-y-3">
					{#if isServiceRequest}
						<div>
							<SectionLabel>Select service account</SectionLabel>
							<div class="flex flex-col gap-2.5">
								<label class="flex flex-col gap-1">
									<span class="text-[13px] text-ink-muted">Account</span>
									<select
										bind:value={selectedServiceAccount}
										aria-label="Service account to approve for"
										class="rounded border border-border-subtle bg-surface px-3 py-2 text-[13px] text-ink hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
									>
										<option value="">Select an account...</option>
										{#each serviceAccounts as account (account)}
											<option value={account}>{account}</option>
										{/each}
									</select>
								</label>
								<!-- Offered here because this is the moment the approver
								     is already deciding what the enrollment is for. Left
								     empty, notifications reach everyone holding the
								     account; a team alias reaches the people who
								     actually run the job. Editable later either way. -->
								<label class="flex flex-col gap-1">
									<span class="text-[13px] text-ink-muted">Notification address (optional)</span>
									<input
										type="email"
										bind:value={notificationEmail}
										data-testid="notification-email-input"
										placeholder="deploys@example.com"
										aria-describedby="notification-email-help"
										class="rounded border border-border-subtle bg-surface px-3 py-2 text-[13px] text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
									/>
									<span id="notification-email-help" class="text-[11px] text-ink-muted">
										Where notifications about this enrollment go — redemptions, the expiry reminder,
										and any use of the code after it expires. Leave empty to notify everyone holding
										the account.
									</span>
								</label>
							</div>
						</div>
					{/if}
					{#if isUserRequest && hasPrincipals}
						<div>
							<SectionLabel>Select principals</SectionLabel>
							<div class="flex flex-col gap-2.5">
								{#each userPrincipals as principal (principal)}
									<label class="flex items-center gap-2">
										<input
											type="checkbox"
											value={principal}
											bind:group={selectedPrincipals}
											class="rounded border border-border-subtle bg-surface accent-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
										/>
										<span class="font-mono text-[13px] text-ink">{principal}</span>
									</label>
								{/each}
							</div>
						</div>
					{/if}
					{#if actionError}
						<Alert variant="error" title="That did not go through">{actionError}</Alert>
					{/if}
					<!-- Deny first, Approve last: the affirming action sits where the
					     eye finishes, and the destructive one is not the button a
					     hurried hand lands on by default. -->
					<div class="flex flex-wrap justify-end gap-2.5">
						<Button testid="deny-button" variant="ghost" {busy} onclick={ondeny}>
							<Icon name="x" size="sm" />
							Deny
						</Button>
						<Button
							testid="approve-button"
							{busy}
							disabled={(isServiceRequest && !selectedServiceAccount) ||
								(isUserRequest && selectedPrincipals.length === 0)}
							onclick={onapprove}
						>
							<Icon name="check" size="sm" />
							{busy ? 'Working…' : 'Approve'}
						</Button>
					</div>
				</div>
			{/if}
		{/snippet}
	</Card>

	<p class="text-center text-[11px] text-ink-muted">
		Requests are logged. See the audit trail for details.
	</p>
</div>
