<script lang="ts">
	import type { ServiceEnrollment } from '$lib/api/types';
	import { formatDuration, isExpired, relativeTime } from '$lib/format';
	import Icon from './Icon.svelte';
	import TypeBadge from './TypeBadge.svelte';

	// One service enrollment as a standalone, clickable card, matching
	// CertRow's shape so the two lists read the same way. The subject is the
	// account the code mints for, because that is what an operator is
	// scanning the list to find; everything else is behind the row.
	interface Props {
		enrollment: ServiceEnrollment;
		/** Pinned clock, so a list of rows agrees with itself and tests can fix it. */
		now?: Date;
		onclick: () => void;
		/** Optional test ID for identifying this row in tests. */
		testid?: string;
	}

	let { enrollment, now = new Date(), onclick, testid }: Props = $props();

	// Service enrollments carry a single principal by construction. The join
	// covers a row that somehow says otherwise, and the fallback one whose
	// stored principals could not be decoded at all.
	const subject = $derived(
		enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account'
	);

	const expired = $derived(isExpired(enrollment.expires_at, now));

	// What a redemption hands out. An enrollment approved before the code and
	// certificate lifetimes were split reports no duration of its own, and
	// there the code's own expiry bounded both.
	const certificateLifetime = $derived(
		enrollment.certificate_valid_seconds === undefined
			? 'certificates last until the code expires'
			: `certificates valid for ${formatDuration(enrollment.certificate_valid_seconds)}`
	);

	const detail = $derived(
		`approved ${relativeTime(enrollment.created_at, now)} · ${certificateLifetime}`
	);

	// Usage is the second thing an operator looks for: a code nothing has
	// redeemed in months is a candidate to retire, whatever its expiry says.
	const usage = $derived(
		enrollment.retrieval_count === 0
			? 'never redeemed'
			: enrollment.last_retrieved_at
				? `redeemed ${enrollment.retrieval_count === 1 ? 'once' : `${enrollment.retrieval_count} times`}, last ${relativeTime(enrollment.last_retrieved_at, now)}`
				: `redeemed ${enrollment.retrieval_count === 1 ? 'once' : `${enrollment.retrieval_count} times`}`
	);
</script>

<button
	type="button"
	{onclick}
	data-testid={testid}
	class="flex w-full items-center justify-between gap-4 rounded-[10px] border border-border-subtle bg-surface px-5 py-3.5 text-left transition hover:bg-surface-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
>
	<span class="flex min-w-0 items-center gap-3">
		<TypeBadge type="service" />
		<span class="min-w-0">
			<span class="block truncate font-mono text-[13px]">{subject}</span>
			<span class="mt-0.5 block text-xs text-ink-muted">{detail}</span>
			<span class="mt-px block truncate text-xs text-ink-muted">{usage}</span>
		</span>
	</span>

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
</button>
