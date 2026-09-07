<script lang="ts">
	import type { CertificateRecord } from '$lib/api/types';
	import { formatDuration, isExpired, relativeTime } from '$lib/format';
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';
	import TypeBadge from './TypeBadge.svelte';

	// One certificate as a standalone card. Rows are separate cards rather
	// than divided rows inside one panel so a list reads as a stack of
	// discrete decisions — each one is a thing that happened, not a line in a
	// table.
	interface Props {
		cert: CertificateRecord;
		/** What the row is about — "certificate requested", "service enrollment requested". */
		event?: string;
		/** Pinned clock, so a list of rows agrees with itself and tests can fix it. */
		now?: Date;
		/** Replaces the indicators on the right, for a different summary. */
		trailing?: Snippet;
		/**
		 * Whether to mark how the request was decided. Off by default: a
		 * certificate list is approvals by definition, so the mark is only
		 * worth its width in a list that also carries refusals — and only
		 * where there is width for it. See `outcome` below.
		 */
		showOutcome?: boolean;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		/**
		 * Where the row leads. A row is a link rather than a button because
		 * opening one is a navigation to the certificate's own page, and only a
		 * link gives a reader middle-click, ctrl-click and "copy link address".
		 */
		href: string;
	}

	let {
		cert,
		event = 'certificate requested',
		now = new Date(),
		trailing,
		showOutcome = false,
		testid,
		href
	}: Props = $props();

	// "user@host" as the requester reported it, the same pair the certificate
	// page reports as "Reported as". Joined only when both halves are there —
	// "alice@" reads as a truncated address rather than as a missing
	// hostname.
	const askedBy = $derived(
		cert.reported_username && cert.reported_hostname
			? `${cert.reported_username}@${cert.reported_hostname}`
			: cert.reported_username || cert.reported_hostname || ''
	);

	// The subject line is where the certificate came from, which is a
	// different field per type: a service certificate's own origin is the
	// address that redeemed the code, and every other type's is the client
	// that asked. Deliberately not the deciding account — on a person's own
	// history that is their own address on every row, which names nothing.
	// Who approved it is on the certificate's own page.
	const subject = $derived(
		cert.type === 'service'
			? cert.retrieved_source_ip || askedBy || cert.key_id
			: askedBy || cert.retrieved_source_ip || cert.key_id
	);

	const principals = $derived(
		cert.principals
			.split(',')
			.map((p) => p.trim())
			.filter((p) => p.length > 0)
			.join(', ')
	);

	// Validity is the requested lifetime, not the time left — the row records
	// what was granted, and "valid for 8h" stays true after it expires.
	const validFor = $derived(
		Math.floor((new Date(cert.expires_at).getTime() - new Date(cert.issued_at).getTime()) / 1000)
	);

	const detail = $derived(
		Number.isFinite(validFor) && validFor > 0
			? `${event} ${relativeTime(cert.issued_at, now)} · valid for ${formatDuration(validFor)}`
			: `${event} ${relativeTime(cert.issued_at, now)}`
	);

	// How the request was decided. A certificate exists only because one was
	// approved, so an absent decision record still means approved — it just
	// predates the audit trail.
	//
	// Shown only where a list mixes the two: on `/logs/me` above `sm`, where
	// refusals are interleaved (see DeniedRow) and the mark is what tells
	// the two kinds of row apart at a glance. Not on the admin list or the
	// dashboard, which read the certificates table alone and would carry the
	// same green tick down every row, and not at phone width, where the
	// history is pinned to approvals precisely so the row can spend that
	// space on the subject and the detail line instead.
	//
	// An icon rather than the pill this used to be: a pill costs a word's
	// width plus its padding on every row to answer a yes or a no.
	const outcome = $derived(cert.decided_by_outcome === 'denied' ? 'denied' : 'approved');

	// Whether the certificate still works, which is the question the outcome
	// mark beside it cannot answer — and the one a reader actually brings to
	// a row they are looking at.
	//
	// It is a state rather than a record, so it is the one thing on the row
	// that changes while the page is open: `now` ticks, and a certificate
	// that expires under the reader's eyes says so.
	const expired = $derived(isExpired(cert.expires_at, now));
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve --
     This file's one link is the row itself, and its href comes in already
     resolved from whoever renders the list. Resolving it a second time here
     would double the base path. -->
<a
	{href}
	data-testid={testid}
	class="flex w-full items-center justify-between gap-4 rounded-[10px] border border-border-subtle bg-surface px-4 py-3 text-left text-ink no-underline transition hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
>
	<span class="flex min-w-0 flex-1 items-center gap-3">
		<TypeBadge type={cert.type} />
		<!-- Stacked on a narrow screen, three columns once the page is wide
		     enough to hold them. A list of rows is the same five fields over
		     and over, and stretching a stacked row only pushes the last field
		     further from the first — aligning them into columns is what makes
		     the extra width worth having. -->
		<span
			class="grid min-w-0 flex-1 gap-x-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)_minmax(0,1fr)] xl:items-baseline"
		>
			<span class="block truncate font-mono text-[13px]">{subject}</span>
			<span class="mt-0.5 block truncate text-xs text-ink-muted xl:mt-0">{detail}</span>
			{#if principals}
				<span class="mt-px block truncate text-xs text-ink-muted xl:mt-0">
					principals: <span class="font-mono">{principals}</span>
				</span>
			{/if}
		</span>
	</span>

	{#if trailing}
		{@render trailing()}
	{:else}
		<!-- Titles as well as accessible names: on a pointer these icons are
		     the only thing there, and "expired" or "denied" has to be
		     readable without opening the row. -->
		<span class="flex flex-shrink-0 items-center gap-2">
			<span
				title={expired ? 'Expired' : 'Still valid'}
				aria-label={expired ? 'Expired' : 'Still valid'}
				data-testid="cert-validity"
				data-valid={expired ? 'false' : 'true'}
				class={expired ? 'text-ink-muted' : 'text-granted'}
			>
				<!-- One drawing in two conditions. The expired state used to
				     borrow a warning triangle, which asks the reader to act; a
				     certificate past its validity is a settled fact and needs
				     nothing from them. -->
				<Icon name={expired ? 'certificate-off' : 'certificate'} size="sm" />
			</span>
			{#if showOutcome}
				<!-- StatusBadge's own glyphs for the two outcomes, which are
				     also the ones the outcome filter chips carry, so the mark
				     on a row is the chip that selected it. -->
				<span
					title={outcome === 'denied' ? 'Denied' : 'Approved'}
					aria-label={outcome === 'denied' ? 'Denied' : 'Approved'}
					data-testid="cert-outcome"
					data-outcome={outcome}
					class={outcome === 'denied' ? 'text-danger' : 'text-granted'}
				>
					<Icon name={outcome === 'denied' ? 'circle-x' : 'circle-check'} size="sm" />
				</span>
			{/if}
		</span>
	{/if}
</a>
