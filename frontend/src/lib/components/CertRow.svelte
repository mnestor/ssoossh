<script lang="ts">
	import type { CertificateRecord } from '$lib/api/types';
	import { formatDuration, isExpired, relativeTime } from '$lib/format';
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';
	import StatusBadge from './StatusBadge.svelte';
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
		/** Replaces the decision badge on the right, for a different summary. */
		trailing?: Snippet;
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

	// A certificate exists only because a request was approved, so an absent
	// decision record still means approved — it just predates the audit trail.
	const decision = $derived(cert.decided_by_outcome === 'denied' ? 'denied' : 'approved');

	// Whether the certificate still works, which the decision badge beside
	// it cannot say: every row in a certificate list was approved, and the
	// question a reader actually brings to one is "can I still use this".
	// An icon rather than a second pill — two badges on one row compete, and
	// this one is a yes or a no.
	//
	// A shield, deliberately not the tick StatusBadge gives an approval:
	// they sit next to each other, and two ticks in a row would read as one
	// fact said twice rather than as two answers to different questions.
	// The warning triangle is the glyph StatusBadge already uses for an
	// expired request, which is the same meaning.
	//
	// It is a state, not a record, so it is the one thing on the row that
	// changes while the page is open: `now` ticks, and a certificate that
	// expires under the reader's eyes says so.
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
		<span class="flex flex-shrink-0 items-center gap-2">
			<!-- Title as well as an accessible name: on a pointer the icon is
			     the only thing there, and "expired" has to be readable
			     without opening the row. -->
			<span
				title={expired ? 'Expired' : 'Still valid'}
				aria-label={expired ? 'Expired' : 'Still valid'}
				data-testid="cert-validity"
				data-valid={expired ? 'false' : 'true'}
				class={expired ? 'text-ink-muted' : 'text-granted'}
			>
				<Icon name={expired ? 'alert-triangle' : 'shield-check'} size="sm" />
			</span>
			<StatusBadge status={decision} />
		</span>
	{/if}
</a>
