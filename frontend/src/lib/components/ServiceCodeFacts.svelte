<script lang="ts">
	import type { AdminEnrollment, ServiceEnrollment } from '$lib/api/types';
	import { formatDateTimeRange, formatDuration, isExpired, remainingLabel } from '$lib/format';
	import CopyableId from './CopyableId.svelte';
	import DetailRow from './DetailRow.svelte';
	import Icon from './Icon.svelte';
	import MonoChip from './MonoChip.svelte';
	import PageSection from './PageSection.svelte';
	import TypeChip from './TypeChip.svelte';

	// Everything both service-code pages say about a code, in one place.
	//
	// The holder's page and the admin's are the same reading with different
	// controls attached: the same identity strip, the same account, lifetime
	// and key, the same window and options. They were two copies of that
	// markup, and the copies had already drifted — one resolved the account
	// from service_account and the other from principals, for a value that is
	// meant to be the same thing.
	//
	// What stays with the pages is what genuinely differs: who may edit the
	// notification address, who may retire the code, and how much of the
	// approver's identity each audience is shown.
	interface Props {
		/** Either enrollment shape: the admin response is the wider one, and
		 *  every field read here is in both. */
		enrollment: ServiceEnrollment | AdminEnrollment;
		/** The approver as this audience should see them — a username on the
		 *  holder's page, username and email in the admin console. */
		approvedBy: string;
		/** Pinned clock, so the remaining lifetime matches the list it came
		 *  from. */
		now?: Date;
	}

	let { enrollment, approvedBy, now = new Date() }: Props = $props();

	// The service account, which is both the certificate principal and who
	// owns this code. principals is the fallback for a row from before the
	// account had its own field.
	const subject = $derived(
		enrollment.service_account ||
			(enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account')
	);

	const expired = $derived(isExpired(enrollment.expires_at, now));

	const certificateLifetime = $derived(
		enrollment.certificate_valid_seconds === undefined
			? 'until the code expires'
			: formatDuration(enrollment.certificate_valid_seconds)
	);

	// The options as they would be written in authorized_keys, rather than as
	// a row apiece. They are one fact — what is fixed into every certificate
	// this code mints — and a section of four labels made the reader assemble
	// it themselves. Source addresses and the forced command keep their
	// keywords so a chip cannot be mistaken for an extension name.
	const optionChips = $derived([
		...enrollment.options.extensions,
		...(enrollment.options.no_touch_required ? ['no-touch-required'] : []),
		...(enrollment.options.source_addresses ?? []).map((address) => `from=${address}`),
		...(enrollment.options.force_command ? [`command=${enrollment.options.force_command}`] : [])
	]);
</script>

<!-- The identity strip, the same one the certificate page opens with: what
     kind of thing this is, whether it still works, and the id to quote in a
     ticket. -->
<div
	class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-border-subtle bg-surface-muted px-4 py-3"
>
	<TypeChip type="service" />
	{#if expired}
		<span
			class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full bg-surface px-2.5 py-1 text-xs font-semibold text-ink-muted"
		>
			<Icon name="alert-triangle" size="xs" />
			Expired
		</span>
	{:else}
		<span
			class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full bg-granted-surface px-2.5 py-1 text-xs font-semibold text-granted"
		>
			<Icon name="circle-check" size="xs" />
			Active
		</span>
	{/if}
	<span class="ml-auto"><CopyableId value={enrollment.id} testid="enrollment-id" /></span>
</div>

<PageSection title="What it hands out">
	<dl class="divide-y divide-border-subtle">
		<!-- The principal leads the way the decider leads on a certificate: it
		     is the account every certificate this code mints is for, fixed at
		     approval, and everything below is a property of that grant. -->
		<DetailRow label="Principal" mono>
			<span data-testid="service-code-account">{subject}</span>
		</DetailRow>
		<DetailRow label="Certificate life" icon="clock">{certificateLifetime}</DetailRow>
		<DetailRow label="Key ID" mono>{enrollment.key_id || '—'}</DetailRow>
		<DetailRow label="Bound key" mono>{enrollment.public_key_fingerprint || '—'}</DetailRow>
	</dl>
</PageSection>

<PageSection title="The code itself">
	<dl class="divide-y divide-border-subtle">
		<!-- Approval and expiry as one window rather than two rows: they are
		     the two ends of a single fact, and apart they made the reader
		     subtract one from the other to answer "how long does this
		     live?". -->
		<DetailRow label="Valid period" icon="clock">
			{formatDateTimeRange(enrollment.created_at, enrollment.expires_at)}
			<span class="text-ink-muted">({remainingLabel(enrollment.expires_at, now)})</span>
		</DetailRow>
		<DetailRow label="Approved by">{approvedBy || '—'}</DetailRow>
		<DetailRow label="Certificate options">
			{#if optionChips.length === 0}
				<span class="text-ink-muted">None — certificates carry the server's defaults.</span>
			{:else}
				<span class="flex flex-wrap gap-1.5" data-testid="certificate-options">
					{#each optionChips as chip (chip)}
						<MonoChip>{chip}</MonoChip>
					{/each}
				</span>
			{/if}
		</DetailRow>
	</dl>
</PageSection>
