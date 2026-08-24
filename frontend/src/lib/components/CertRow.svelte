<script lang="ts">
	import type { CertificateRecord } from '$lib/api/types';
	import { formatDuration, relativeTime } from '$lib/format';
	import type { Snippet } from 'svelte';
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
		onclick: () => void;
	}

	let {
		cert,
		event = 'certificate requested',
		now = new Date(),
		trailing,
		onclick
	}: Props = $props();

	// The subject line is whatever names this certificate to a human: the
	// account it was decided for, else its key id.
	const subject = $derived(cert.decided_by_email || cert.decided_by_username || cert.key_id);

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
</script>

<button
	type="button"
	{onclick}
	class="flex w-full items-center justify-between gap-4 rounded-[10px] border border-border-subtle bg-surface px-5 py-3.5 text-left transition hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
>
	<span class="flex min-w-0 items-center gap-3">
		<TypeBadge type={cert.type} />
		<span class="min-w-0">
			<span class="block truncate font-mono text-[13px]">{subject}</span>
			<span class="mt-0.5 block text-xs text-ink-muted">{detail}</span>
			{#if principals}
				<span class="mt-px block truncate text-xs text-ink-muted">
					principals: <span class="font-mono">{principals}</span>
				</span>
			{/if}
		</span>
	</span>

	{#if trailing}
		{@render trailing()}
	{:else}
		<StatusBadge status={decision} />
	{/if}
</button>
