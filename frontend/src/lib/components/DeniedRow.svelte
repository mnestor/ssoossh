<script lang="ts">
	import type { DeniedRequest } from '$lib/api/types';
	import { relativeTime } from '$lib/format';
	import StatusBadge from './StatusBadge.svelte';
	import TypeBadge from './TypeBadge.svelte';

	// One refusal in a decision history, laid out to line up with the
	// CertRow beside it.
	//
	// Not a link, and that is the point rather than an omission: a denial
	// issues nothing, so there is no certificate page to open. Everything
	// recorded about it is on the row — what was asked for, who or what
	// asked, and when it was refused.
	//
	// It is a separate component rather than a mode of CertRow because a
	// denial genuinely has none of what a certificate row shows: no serial,
	// no key id, no fingerprint, no principals granted and no validity
	// window. Feeding empty strings into CertRow would render a row that
	// reads like a certificate whose details nobody can find.
	interface Props {
		denial: DeniedRequest;
		/** Pinned clock, so a list of rows agrees with itself and tests can fix it. */
		now?: Date;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { denial, now = new Date(), testid }: Props = $props();

	// "user@host" as the requester reported it, the same pair a certificate
	// row leads with. Joined only when both halves are there — "alice@" reads
	// as a truncated address rather than as a missing hostname.
	const askedBy = $derived(
		denial.reported_username && denial.reported_hostname
			? `${denial.reported_username}@${denial.reported_hostname}`
			: denial.reported_username || denial.reported_hostname || ''
	);

	// The request id is the fallback subject, not a preference: it is the
	// only thing every denial has, and it is what an audit event for this
	// refusal carries.
	const subject = $derived(askedBy || denial.certificate_request_id);

	// A denial whose request row has gone reports no type. Saying so beats
	// picking one: the row is still a real refusal, and an invented "user"
	// would be a wrong answer rather than a missing one.
	const what = $derived(denial.type ? `${denial.type} request` : 'request');

	// What the request said it was doing, in the third column where a
	// certificate row puts its principals. A denial granted none, and this
	// is the fact that fills the space usefully: "sudo · pts/3 · from
	// 10.1.2.9" is a refusal somebody can still recognise a month later,
	// where a request id is not.
	//
	// A user request carries no PAM service or terminal — there is no
	// session behind it — so the reporting binary stands in. Claims
	// throughout, self-reported by an unauthenticated caller and never
	// verified, which is what the "claimed" label says.
	const claimed = $derived(
		[
			denial.pam_service,
			denial.tty,
			denial.remote_host ? `from ${denial.remote_host}` : '',
			denial.pam_service || denial.tty || denial.remote_host ? '' : denial.client
		].filter((part) => !!part)
	);
</script>

<div
	data-testid={testid}
	class="flex w-full items-center justify-between gap-4 rounded-[10px] border border-border-subtle bg-surface px-4 py-3 text-left text-ink"
>
	<span class="flex min-w-0 flex-1 items-center gap-3">
		<TypeBadge type={denial.type} />
		<!-- The same three-column grid CertRow uses above `xl`, so the two
		     kinds of row read as one list rather than as two lists that
		     happen to be adjacent. Where a certificate row puts the
		     principals it granted, a denial puts what the request claimed
		     about itself: it granted nothing, and this is what makes the
		     refusal recognisable later. -->
		<span
			class="grid min-w-0 flex-1 gap-x-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)_minmax(0,1fr)] xl:items-baseline"
		>
			<span class="block truncate font-mono text-[13px]">{subject}</span>
			<span class="mt-0.5 block truncate text-xs text-ink-muted xl:mt-0">
				{what} denied {relativeTime(denial.decided_at, now)}
			</span>
			{#if claimed.length > 0}
				<span class="mt-px block truncate text-xs text-ink-muted xl:mt-0">
					claimed: <span class="font-mono">{claimed.join(' · ')}</span>
				</span>
			{/if}
		</span>
	</span>

	<StatusBadge status="denied" />
</div>
