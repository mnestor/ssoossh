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
	import { clockSkewLabel, expiryLabel, formatDateTime, formatDuration } from '$lib/format';

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

	// The approval deadline moves, so it needs a clock that does too. Ten
	// seconds rather than the thirty the list pages use: a console request's
	// budget is deliberately the shortest of the four, and a countdown that
	// lags by half a minute against a two-minute window is worse than none.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 10_000);
		return () => clearInterval(timer);
	});

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
	const isConsoleRequest = $derived(detail.type === 'console');
	const isServiceRequest = $derived(detail.type === 'service');
	const isUserRequest = $derived(detail.type === 'user');
	// PAM and console requests authorize a local operation on a host, not an
	// SSH session, and their certificate is shaped entirely by server config:
	// the requesting machine minted a throwaway key and asked for nothing, so
	// the public key, the extension and critical-option sets, and the
	// "less than was requested" comparison carry no decision for the
	// approver. What they need is above: who is acting, as which account, on
	// which machine.
	const isLocalAuth = $derived(detail.type === 'pam' || isConsoleRequest);
	const hasServiceAccounts = $derived(serviceAccounts.length > 0);
	const hasPrincipals = $derived(userPrincipals.length > 0);
	// The approver picks which of their accounts the certificate carries for
	// every type but service. For PAM and console the host then matches
	// those principals against the local account, directly or through its
	// principals-map, so which ones go in is the approver's call.
	const picksPrincipals = $derived((isUserRequest || isLocalAuth) && hasPrincipals);
	// The picker replaces the read-only row only while there is a decision
	// to make: a decided, expired or foreign request shows what the server
	// says instead. Once a decision has been sent the chips stay, disabled,
	// so the page keeps showing what was actually chosen.
	const showsPicker = $derived(picksPrincipals && !hasDecisionRecord && !blocked);

	/** togglePrincipal adds principal to the selection or removes it. */
	function togglePrincipal(principal: string) {
		selectedPrincipals = selectedPrincipals.includes(principal)
			? selectedPrincipals.filter((p) => p !== principal)
			: [...selectedPrincipals, principal];
	}

	// The short form of the request id, for the corner of the card — enough
	// to tell two requests apart when comparing against a log line. Labelled
	// rather than prefixed with a bare "#", which reads as a colour code.
	// The full id is on the title attribute for anyone who needs all of it.
	const shortId = $derived(detail.id.slice(0, 5));

	// PAM authenticates a single local operation (e.g. `sudo`) to
	// pam_ssoossh, not an interactive SSH session — "requesting an SSH
	// certificate" would misdescribe what's actually being authorized, so
	// the heading says what this type of certificate is for instead (see
	// https://mnestor.github.io/ssoossh/concepts/, PAM).
	// A console login is the bigger grant of the two and reads differently:
	// it authorizes a whole interactive session on the machine, and the
	// person approving it is being asked to vouch for someone standing at a
	// keyboard they cannot see.
	const cardCopy = $derived.by(() => {
		if (detail.type === 'console') {
			return {
				title: 'Approve a console login',
				description:
					'Someone is logging in at this machine\u2019s console. Approving grants an interactive session there, not a single command \u2014 so check that the machine, terminal and account below are the ones in front of you.'
			};
		}
		if (detail.type === 'pam') {
			return {
				title: 'Approve a PAM authentication',
				description:
					"Review before authorizing this local operation. This certificate is for a single sudo (or other PAM) call on the client's machine, not an interactive SSH session."
			};
		}
		return {
			title: 'Approve a certificate request',
			description: 'Review exactly what this certificate will grant before authorizing it.'
		};
	});

	// A console has no remote host. PAM_RHOST arriving non-empty on a
	// request that claims to be one means it is something else — an SSH
	// session, or a caller sending whatever it likes — and that is worth
	// saying outright rather than rendering as one more grey row.
	const remoteHostSuspicious = $derived(isConsoleRequest && !!detail.remote_host);

	// A PAM authentication with no terminal and no remote host is not
	// someone at a keyboard: an interactive sudo has a tty, and a remote one
	// has PAM_RHOST. Neither present usually means a cron job or a service
	// account driving sudo unattended — worth a quiet note, not a warning,
	// since it is ordinary for plenty of deployments.
	const pamLooksHeadless = $derived(
		detail.type === 'pam' && !!detail.pam_service && !detail.tty && !detail.remote_host
	);

	/** One row of the claimed-context block: the caller's own, unauthenticated
	 * account of where and how this operation is happening. `sub` is a
	 * smaller line under the value (machine_id under Host). `mono` renders
	 * the value as plain monospace text instead of a bordered chip, for
	 * values that are not a discrete token — a command line, a version
	 * string, a list of process ids. `plain` renders it as ordinary sans-serif
	 * prose instead — for a value that is neither a token nor code, such as a
	 * platform description (see DESIGN.md: Fira Code is for keys,
	 * fingerprints, principals and other cryptographic data, not prose). */
	interface ClaimedContextRow {
		key: string;
		label: string;
		value: string;
		sub?: string;
		mono?: boolean;
		plain?: boolean;
	}

	// Everything the client and the pam_ssoossh module sent about where and
	// how this request is happening, in the order a reviewer would want to
	// check it: who, running what, from where, as observed by which module.
	// None of it is authenticated — see server/service/hostcontext.go — so
	// every row is rendered as a claim, not a fact, and PAM and console
	// requests share exactly the same block: both are local-auth requests a
	// human is approving on trust in the reporting host.
	const claimedContext = $derived.by((): ClaimedContextRow[] => {
		if (!isLocalAuth) {
			return [];
		}
		const rows: ClaimedContextRow[] = [];
		if (detail.target_account) {
			rows.push({ key: 'account', label: 'Account', value: detail.target_account });
		}
		// Omitted when it just repeats the account: PAM_RUSER equal to the
		// target account is the ordinary case (someone sudo-ing to
		// themselves) and adds nothing a second row would explain.
		if (detail.requesting_user && detail.requesting_user !== detail.target_account) {
			rows.push({ key: 'invoked-by', label: 'Invoked by', value: detail.requesting_user });
		}
		if (detail.process) {
			rows.push({ key: 'command', label: 'Command', value: detail.process, mono: true });
		}
		if (detail.hostname) {
			rows.push({ key: 'host', label: 'Host', value: detail.hostname, sub: detail.machine_id });
		}
		if (detail.pam_service) {
			rows.push({ key: 'service', label: 'Service', value: detail.pam_service });
		}
		if (detail.tty) {
			rows.push({ key: 'terminal', label: 'Terminal', value: detail.tty });
		}
		// A console request with a suspicious remote host gets the warning
		// below instead of a plain row — the row would just repeat what the
		// warning already says more usefully.
		if (detail.remote_host && !remoteHostSuspicious) {
			rows.push({ key: 'remote-host', label: 'Remote host', value: detail.remote_host });
		}
		if (detail.os) {
			rows.push({ key: 'platform', label: 'Platform', value: detail.os, plain: true });
		}
		if (detail.client) {
			const value = detail.client_mode
				? `${detail.client} (mode=${detail.client_mode})`
				: detail.client;
			rows.push({ key: 'client', label: 'Client', value, mono: true });
		}
		const processIds: string[] = [];
		if (detail.caller_uid !== undefined) {
			processIds.push(`uid ${detail.caller_uid}`);
		}
		if (detail.caller_pid !== undefined) {
			processIds.push(`pid ${detail.caller_pid}`);
		}
		if (detail.caller_ppid !== undefined) {
			processIds.push(`ppid ${detail.caller_ppid}`);
		}
		if (processIds.length > 0) {
			rows.push({
				key: 'process-ids',
				label: 'Process ids',
				value: processIds.join(' \u00b7 '),
				mono: true
			});
		}
		return rows;
	});

	// How far the host's own clock (client_time) has drifted from when the
	// server received the request — worth a note past a small tolerance,
	// since ordinary drift is not, but a clock that is minutes off might mean
	// the reporting host is misconfigured or the request is not what it
	// claims to be.
	const hostClockSkew = $derived(
		detail.client_time ? clockSkewLabel(detail.client_time, detail.created_at) : null
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
				{#if showsPicker}
					<!-- The approver's held accounts as toggle chips: pressed ones go
					     into the certificate. In the row itself rather than a section
					     further down, so the value being decided sits where the value
					     is read. -->
					<span
						class="flex flex-wrap items-center gap-1.5"
						role="group"
						aria-label="Principals to include"
					>
						{#each userPrincipals as principal (principal)}
							{@const pressed = selectedPrincipals.includes(principal)}
							<button
								type="button"
								onclick={() => togglePrincipal(principal)}
								aria-pressed={pressed}
								disabled={busy || outcome !== null}
								class="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 font-mono text-xs break-all transition focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-default"
								class:border-accent={pressed}
								class:bg-accent={pressed}
								class:text-accent-ink={pressed}
								class:border-border-subtle={!pressed}
								class:text-ink-muted={!pressed}
								class:hover:bg-surface-muted={!pressed && !busy && outcome === null}
							>
								{#if pressed}<Icon name="check" size="sm" />{/if}
								{principal}
							</button>
						{/each}
						<span class="font-sans text-ink-muted">select the accounts to include</span>
					</span>
				{:else if detail.principals.length > 0}
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
			<!-- The claimed context: everything the client and the pam_ssoossh
			     module reported about who is doing this and where, for PAM and
			     console requests alike. None of it is authenticated — the row
			     labels and the "reported by the client" tag say so rather than
			     presenting any of it as fact. Their value is that they let a
			     human notice "I am at my desk, why is there a console login on
			     rack07" — not that they authorize anything. -->
			{#each claimedContext as row (row.key)}
				<DetailRow label={row.label} mono={row.mono}>
					<span class="flex flex-col gap-0.5">
						<span class="flex flex-wrap items-center gap-1.5">
							{#if row.mono || row.plain}
								<span>{row.value}</span>
							{:else}
								<MonoChip>{row.value}</MonoChip>
							{/if}
							<span class="font-sans text-ink-muted">reported by the client</span>
						</span>
						{#if row.sub}
							<span class="font-mono text-[11px] text-ink-muted">{row.sub}</span>
						{/if}
					</span>
				</DetailRow>
			{/each}
			{#if isLocalAuth && detail.client_time}
				<DetailRow label="Host clock">
					<span class="flex flex-wrap items-center gap-1.5">
						<span>{formatDateTime(detail.client_time)}</span>
						<span class="font-sans text-ink-muted">reported by the client</span>
						{#if hostClockSkew}
							<span class="text-trimmed">{hostClockSkew}</span>
						{/if}
					</span>
				</DetailRow>
			{/if}
			{#if isLocalAuth && (detail.trusted_ca_fingerprints ?? []).length > 0}
				<DetailRow label="Host trusts">
					<span class="flex flex-col items-start gap-1.5">
						{#each detail.trusted_ca_fingerprints ?? [] as fingerprint, index (index)}
							<MonoChip>{fingerprint}</MonoChip>
						{/each}
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
			{#if detail.expires_at && !detail.already_closed}
				<!-- The server's deadline, not a guess: a request is approvable
				     for its own type's budget and is refused after it. Shown so a
				     slow sign-in is distinguishable from a request that has
				     already died, which is the difference between "click
				     approve" and "start again at the machine". -->
				<DetailRow label="Approvable until">
					{formatDateTime(detail.expires_at)} ({expiryLabel(detail.expires_at, now)})
				</DetailRow>
			{/if}
			{#if !isLocalAuth}
				<DetailRow label="Public key" mono>{detail.public_key}</DetailRow>
			{/if}
		</dl>

		<!-- The granted set, not the requested set. Options this deployment does
		     not permit are trimmed rather than rejected, so the two can differ and
		     the difference has to be visible before anyone approves (root
		     CLAUDE.md, Hard Constraints). Omitted for PAM and console requests,
		     where nothing was requested by a person: see isLocalAuth. -->
		<div class="mt-6 space-y-6">
			{#if !isLocalAuth}
				<div>
					<SectionLabel>Extensions this certificate will carry</SectionLabel>
					<OptionDiffList entries={extensions} emptyLabel="No extensions requested." />
				</div>

				<div>
					<SectionLabel>Critical options</SectionLabel>
					<OptionDiffList entries={criticalOptions} emptyLabel="No critical options requested." />
				</div>
			{/if}

			{#if remoteHostSuspicious}
				<Alert
					variant="warning"
					title="This does not look like a console"
					testid="console-remote-host-warning"
				>
					The request says it is a console login, but it also reports a remote host (<span
						class="font-mono">{detail.remote_host}</span
					>). A console has nobody connecting to it over the network. Treat this as a login you did
					not start unless you know otherwise.
				</Alert>
			{/if}

			{#if pamLooksHeadless}
				<!-- Not a warning: plenty of deployments run sudo unattended on
				     purpose. Just worth naming, since the approver otherwise has
				     to notice the absence of two rows themselves. -->
				<p class="text-[13px] text-ink-muted" data-testid="pam-headless-note">
					No terminal and no remote host: this looks like a script or a service, not a person at a
					keyboard.
				</p>
			{/if}

			{#if narrowed && !isLocalAuth}
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
								(picksPrincipals && selectedPrincipals.length === 0)}
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
