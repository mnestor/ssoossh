<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getCertificateDetail } from '$lib/api/endpoints';
	import type { CertificateResponse } from '$lib/api/types';
	import { ApiError } from '$lib/api/client';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import CopyableId from '$lib/components/CopyableId.svelte';
	import DetailRow from '$lib/components/DetailRow.svelte';
	import Icon from '$lib/components/Icon.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageSection from '$lib/components/PageSection.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import SectionLabel from '$lib/components/SectionLabel.svelte';
	import TypeChip from '$lib/components/TypeChip.svelte';
	import {
		formatDateTime,
		formatDateTimeRange,
		formatGoDuration,
		isExpired,
		remainingLabel
	} from '$lib/format';
	import { parsePolicyExplanation } from '$lib/policyExplanation';

	const id = $derived(page.params.id ?? '');

	// Where the reader came from, so the chip at the top of the page returns
	// them to the list they opened this from rather than to one they were
	// never on. Read from `from`, and only ever resolved against these three:
	// the parameter is a hint written by our own links, not a destination to
	// follow because a URL said so.
	//
	// A certificate reached from a notification or an audit line carries no
	// `from` and falls back to the reader's own history, which is the list
	// that holds their certificates.
	const backTargets = {
		history: { route: '/logs/me', label: 'Certificate history' },
		dashboard: { route: '/dashboard', label: 'Recent decisions' },
		admin: { route: '/admin/certificates', label: 'All certificates' }
	} as const;
	const back = $derived.by(() => {
		const from = page.url.searchParams.get('from') ?? '';
		return from in backTargets
			? backTargets[from as keyof typeof backTargets]
			: backTargets.history;
	});

	let cert = $state<CertificateResponse | null>(null);
	let loadError = $state<string | null>(null);
	let isAccessDenied = $state(false);
	let hasLoaded = $state(false);

	$effect(() => {
		const controller = new AbortController();

		getCertificateDetail(id, controller.signal)
			.then((result: CertificateResponse) => {
				cert = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) {
					return;
				}
				if (cause instanceof ApiError && cause.isNotFound) {
					isAccessDenied = true;
				} else {
					loadError = errorMessage(cause);
				}
				hasLoaded = true;
			});

		return () => controller.abort();
	});

	const principals = $derived(
		cert && cert.principals
			? cert.principals
					.split(',')
					.map((p) => p.trim())
					.filter((p) => p.length > 0)
			: []
	);

	// The window's two ends are printed as one row, so the page needs a clock
	// that moves: "2h left" on a page left open in a tab should not still say
	// that tomorrow.
	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 30_000);
		return () => clearInterval(timer);
	});

	// Whether the certificate still works. The identity strip used to answer
	// this with an "approved" pill, which answered a different question and
	// always the same way — a certificate page exists because a request was
	// approved. This is the state a reader actually opens the page for, and
	// it belongs on the row that says when the window closes rather than in a
	// strip three sections above it.
	const expired = $derived(cert ? isExpired(cert.expires_at, now) : false);

	// What is left of the window, as a note on the row that prints both its
	// ends — the same parenthetical the service code page carries. Only what
	// is left: the granted lifetime is the distance between two dates the row
	// already shows, and saying "8h · 2h left" spends a second number on
	// arithmetic the reader did not ask for.
	const remaining = $derived(cert ? remainingLabel(cert.expires_at, now) : '');

	// A certificate exists because a request was approved; an absent decision
	// record means the audit trail predates it, not that it was denied. Kept
	// for the label on the decision row: the identity strip no longer carries
	// an outcome pill, because a certificate detail page that says "approved"
	// is answering a question nobody arrived with.
	const decision = $derived(cert && cert.decided_by_outcome === 'denied' ? 'denied' : 'approved');
	const decidedBy = $derived(
		cert ? cert.decided_by_email || cert.decided_by_username || cert.decided_by_subject : null
	);

	// " at 24 Aug 2024, 09:59", to follow the name on the same row. The
	// leading space is inside the value rather than in the markup because
	// Svelte trims whitespace at an element's edge, and a space written
	// there is dropped — leaving the name run into "at".
	const decidedAt = $derived(cert?.decided_at ? ` at ${formatDateTime(cert.decided_at)}` : '');
	const decidedByGroups = $derived(cert?.decided_by_groups ?? []);

	// What the certificate actually grants on the far side. Extensions are a
	// set of names; critical options are name/value pairs, and are listed
	// separately rather than folded together because sshd rejects a
	// certificate outright over a critical option it does not understand and
	// merely ignores an unknown extension.
	const extensions = $derived(cert?.extensions ?? []);
	const criticalOptions = $derived(Object.entries(cert?.critical_options ?? {}));

	// A PAM or console certificate authenticates one local operation and is
	// then thrown away, so permit-pty and friends mean nothing to it -- the
	// server says as much where the default is set
	// (config.CertOptionsPAM.Extensions), console has no extensions setting
	// at all, and neither type reaches an sshd that would act on a critical
	// option. Both sections would read "None / None" forever, which is a
	// section that teaches a reader to skip the page.
	//
	// Suppressed only when there is genuinely nothing in it. PAM extensions
	// are configurable even though they default to empty, and an audit page
	// must not hide something that really was signed into the certificate
	// just because its type usually carries none.
	const isLocalAuthCert = $derived(cert?.type === 'pam' || cert?.type === 'console');
	const showGrants = $derived(
		!isLocalAuthCert || extensions.length > 0 || criticalOptions.length > 0
	);

	// What the approval itself decided, distinct from what the issued
	// certificate above carries: recorded on the decision at approval time,
	// so it stays the record of what was decided even if the certificate's
	// own audit columns were ever unreadable.
	const decidedPrincipals = $derived(cert?.decided_principals ?? []);
	const decidedGrantedOptions = $derived(cert?.decided_granted_options ?? null);
	const decidedGrantedOptionsEmpty = $derived(
		!!decidedGrantedOptions &&
			decidedGrantedOptions.extensions.length === 0 &&
			!decidedGrantedOptions.force_command &&
			(decidedGrantedOptions.source_addresses ?? []).length === 0 &&
			!decidedGrantedOptions.no_touch_required
	);
	const policyExplanation = $derived(parsePolicyExplanation(cert?.decided_policy_explanation));

	// What asked for the certificate, as opposed to who approved it. Read
	// from the decision's own snapshot rather than from the request, so it
	// survives the request row being pruned -- see
	// model.CertificateRequestDecision.
	//
	// "user@host" is the pair the server resolved by type
	// (CertificateRequest.ReportedIdentity): the PAM account and machine for
	// a pam or console certificate, the local client's for a user one. Only
	// joined when both halves are there, since "alice@" reads as a truncated
	// address rather than as a missing hostname.
	const askedBy = $derived(
		cert?.reported_username && cert?.reported_hostname
			? `${cert.reported_username}@${cert.reported_hostname}`
			: (cert?.reported_username ?? cert?.reported_hostname ?? '')
	);
	const reportedRows = $derived(
		(
			[
				['Service', cert?.reported_pam_service],
				['Terminal', cert?.reported_tty],
				['Remote host', cert?.reported_remote_host],
				['Invoked by', cert?.reported_requesting_user],
				['Command', cert?.reported_process],
				['Machine ID', cert?.reported_machine_id],
				['Client', cert?.reported_client]
			] as [string, string | undefined][]
		).filter((row): row is [string, string] => Boolean(row[1]))
	);
	const hasReportedContext = $derived(Boolean(askedBy) || reportedRows.length > 0);
</script>

<svelte:head><title>Certificate · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading
		title="Certificate details"
		back={{ href: resolve(back.route), label: back.label, testid: 'cert-back' }}
	/>

	{#if loadError}
		<Alert variant="error" title="Could not load certificate">{loadError}</Alert>
	{:else if isAccessDenied}
		<div data-testid="access-denied">
			<Alert variant="error" title="Access denied">
				You don't have permission to view this certificate. It may not exist or you may not have the
				required permissions.
			</Alert>
		</div>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if cert}
		<div data-testid="cert-details" class="flex flex-col gap-5">
			<!-- The identity strip: what kind of certificate this is, what
			     happened to the request behind it, and the id to quote in a
			     ticket. Wraps rather than squeezes, because the id is a full
			     uuid and the badges must not shrink to make room for it.
			     The id is a CopyableId like every other strip's, so it is one
			     click onto the clipboard rather than a value to select by
			     hand — it is what the audit events and the log lines carry. -->
			<div
				class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-border-subtle bg-surface-muted px-4 py-3"
			>
				<TypeChip type={cert.type} />
				<span class="ml-auto"><CopyableId value={cert.id} testid="cert-id" /></span>
			</div>

			<PageSection
				title="Certificate"
				description="What this certificate carries, what it grants, and how long for."
			>
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

					<DetailRow label="Key ID" mono>
						<span data-testid="cert-key-id">{cert.key_id}</span>
					</DetailRow>
					<DetailRow label="Serial number" mono>
						<span data-testid="cert-serial-number">{cert.serial_number}</span>
					</DetailRow>
					<DetailRow label="Key fingerprint" mono>{cert.public_key_fingerprint}</DetailRow>

					<!-- One row, not three. Issued, expires and the lifetime
					     between them are the two ends of a single fact and the
					     distance between them, and as separate rows they made a
					     reader subtract one date from another to answer "does
					     this still work?". The service code page states its own
					     window the same way. -->
					<DetailRow label="Valid period">
						<span class="flex flex-wrap items-center gap-2">
							<!-- Title as well as an accessible name: the glyph is
							     the only thing carrying the state, and it is the
							     same pair a history row uses. -->
							<span
								title={expired ? 'Expired' : 'Still valid'}
								aria-label={expired ? 'Expired' : 'Still valid'}
								data-testid="cert-validity"
								data-valid={expired ? 'false' : 'true'}
								class="flex flex-shrink-0 items-center {expired
									? 'text-ink-muted'
									: 'text-granted'}"
							>
								<Icon name={expired ? 'certificate-off' : 'certificate'} size="sm" />
							</span>
							<span data-testid="cert-valid-period">
								{formatDateTimeRange(cert.issued_at, cert.expires_at)}
								<span class="text-ink-muted">({remaining})</span>
							</span>
						</span>
					</DetailRow>

					<!-- What it grants, in the same list rather than a section of
					     its own. Extensions and critical options are two more
					     things signed into this certificate, exactly like the key
					     id and the validity window above them; splitting them off
					     made a reader cross a heading to finish reading one
					     certificate. Still one addressable block, because the
					     whole pair is dropped for a PAM or console certificate
					     that carries neither. -->
					{#if showGrants}
						<div data-testid="cert-grants" class="divide-y divide-border-subtle">
							<DetailRow label="Extensions">
								{#if extensions.length > 0}
									<span class="flex flex-wrap gap-1.5">
										{#each extensions as extension (extension)}
											<MonoChip>{extension}</MonoChip>
										{/each}
									</span>
								{:else}
									<span class="text-ink-muted">None</span>
								{/if}
							</DetailRow>

							<!-- Stated even when empty: "no critical options" is a
							     fact about the certificate worth reading, not an
							     absence. A force-command that is not there is why an
							     interactive shell works. -->
							<DetailRow label="Critical options">
								{#if criticalOptions.length > 0}
									<span class="flex flex-col items-start gap-1.5">
										{#each criticalOptions as [name, value] (name)}
											<MonoChip>{name} <span class="text-ink-muted">=</span> {value}</MonoChip>
										{/each}
									</span>
								{:else}
									<span class="text-ink-muted">None</span>
								{/if}
							</DetailRow>
						</div>
					{/if}
				</dl>
			</PageSection>

			{#if decidedBy}
				<!-- Who decided, from where, and when. The modal states this as a
				     sentence because it has one line to spare; the page has room
				     for the fields themselves, including the approver's groups —
				     the policy input that decided they were allowed to approve at
				     all, and which appears nowhere in the certificate. -->
				<PageSection
					title="Decision"
					description="The approval this certificate was issued against."
					testid="cert-decision"
				>
					<dl class="divide-y divide-border-subtle">
						<!-- Who and when as one row: "alice@example.com at 24 Aug
						     2024, 09:59". They are one event, and a row apiece made
						     the reader carry a name down the list to the timestamp
						     that belongs to it. The time is muted because the name
						     is what the row is for — the same weight the validity
						     window gives what is left of it. -->
						<DetailRow label={decision === 'denied' ? 'Denied by' : 'Approved by'} icon="user">
							<span data-testid="cert-decided-by">
								{decidedBy}{#if decidedAt}<span class="text-ink-muted">{decidedAt}</span>{/if}
							</span>
						</DetailRow>
						{#if cert.decided_source_ip}
							<DetailRow label="Source address" mono>{cert.decided_source_ip}</DetailRow>
						{/if}
						{#if decidedByGroups.length > 0}
							<DetailRow label="Approver groups">
								<span class="flex flex-wrap gap-1.5">
									{#each decidedByGroups as group (group)}
										<MonoChip>{group}</MonoChip>
									{/each}
								</span>
							</DetailRow>
						{/if}
						{#if decidedPrincipals.length > 0}
							<!-- What the approval selected, which is not always every
							     principal on the certificate above: an approver may
							     hold more accounts than they chose to include. -->
							<DetailRow label="Principals granted">
								<span class="flex flex-wrap gap-1.5">
									{#each decidedPrincipals as principal (principal)}
										<MonoChip>{principal}</MonoChip>
									{/each}
								</span>
							</DetailRow>
						{/if}
						{#if decidedGrantedOptions && !(isLocalAuthCert && decidedGrantedOptionsEmpty)}
							<!-- Recorded on the decision at approval time, which is a
							     different source from the certificate's own Extensions
							     and Critical options above: this is what the approval
							     granted, not what the signed certificate carries. -->
							<DetailRow label="Options granted">
								{#if decidedGrantedOptionsEmpty}
									<span class="text-ink-muted">None</span>
								{:else}
									<span class="flex flex-col items-start gap-1.5">
										{#if decidedGrantedOptions.extensions.length > 0}
											<span class="flex flex-wrap gap-1.5">
												{#each decidedGrantedOptions.extensions as extension (extension)}
													<MonoChip>{extension}</MonoChip>
												{/each}
											</span>
										{/if}
										{#if decidedGrantedOptions.force_command}
											<MonoChip
												>force-command <span class="text-ink-muted">=</span>
												{decidedGrantedOptions.force_command}</MonoChip
											>
										{/if}
										{#each decidedGrantedOptions.source_addresses ?? [] as address, index (index)}
											<MonoChip>{address}</MonoChip>
										{/each}
										{#if decidedGrantedOptions.no_touch_required}
											<MonoChip>no-touch-required</MonoChip>
										{/if}
									</span>
								{/if}
							</DetailRow>
						{/if}
					</dl>

					{#if policyExplanation}
						<!-- The lifetime policy engine's own record of how it arrived
						     at this certificate's duration: the tier that matched, the
						     ceiling and what it computed to, and the source rule that
						     narrowed it, if any (see service.PolicyExplanation). -->
						<div class="mt-5">
							<SectionLabel>Lifetime policy</SectionLabel>
							<dl class="divide-y divide-border-subtle">
								{#if policyExplanation.tier}
									<DetailRow label="Tier">
										{policyExplanation.tier.name}
										<span class="text-ink-muted">— {policyExplanation.tier.condition}</span>
									</DetailRow>
								{/if}
								<!-- Both come off the wire as Go duration strings —
								     "8h0m0s" — because the policy engine records them
								     by calling String() on a Duration. Rendered the way
								     every other lifetime on the site reads, and no
								     longer mono: they are a length of time now, not a
								     token to copy. -->
								<DetailRow label="Ceiling">
									<span data-testid="policy-ceiling"
										>{formatGoDuration(policyExplanation.ceiling)}</span
									>
								</DetailRow>
								<DetailRow label="Effective duration">
									<span data-testid="policy-effective-duration"
										>{formatGoDuration(policyExplanation.effective_duration)}</span
									>
								</DetailRow>
								{#if policyExplanation.source_rule}
									<DetailRow label="Source rule" mono
										>{policyExplanation.source_rule.cidr}</DetailRow
									>
								{/if}
							</dl>
						</div>
					{/if}
				</PageSection>
			{/if}

			{#if hasReportedContext}
				<!-- What asked for the certificate. The approval page showed
				     all of this to the approver; without it here the history
				     can say who signed off and what was granted but not which
				     machine or command it was for, which is where an incident
				     review starts.

				     Rendered as claims throughout. Every field is
				     self-reported by an unauthenticated caller and none of it
				     fed any decision: principals came from the approver's
				     held accounts and the lifetime from policy. The one
				     address the server established itself is the decision's
				     source address above, and that is the approver's. -->
				<PageSection
					title="What asked for it"
					description="Reported by the requesting host and never verified."
					testid="cert-reported-context"
				>
					<dl class="divide-y divide-border-subtle">
						{#if askedBy}
							<DetailRow label="Reported as" mono>{askedBy}</DetailRow>
						{/if}
						{#each reportedRows as [label, value] (label)}
							<DetailRow {label} mono>{value}</DetailRow>
						{/each}
					</dl>
				</PageSection>
			{/if}

			{#if cert.retrieved_source_ip}
				<!-- Only a service certificate has one. The address here is the
				     machine that ran `service retrieve`, which is a different
				     question from the decision above: that names the human who
				     approved the code, months earlier and from a browser, and is
				     identical on every certificate the code has ever minted. -->
				<PageSection
					title="Where it was fetched"
					description="The redemption of the service code that produced this certificate."
				>
					<dl class="divide-y divide-border-subtle">
						<DetailRow label="Source address" mono>{cert.retrieved_source_ip}</DetailRow>
						{#if cert.retrieved_at}
							<DetailRow label="Retrieved at">{formatDateTime(cert.retrieved_at)}</DetailRow>
						{/if}
						{#if cert.enrollment_id}
							<DetailRow label="Service code">
								<a
									href={resolve(`/service-codes/${cert.enrollment_id}`)}
									class="inline-flex items-center gap-1.5 text-accent underline-offset-2 hover:underline"
								>
									View the code this came from
									<Icon name="arrow-right" size="xs" />
								</a>
							</DetailRow>
						{/if}
					</dl>
				</PageSection>
			{/if}
		</div>
	{/if}
</PageShell>
