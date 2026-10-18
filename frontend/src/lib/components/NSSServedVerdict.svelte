<script lang="ts">
	import { PosixStatusDeleted, PosixStatusDisabled } from '#lib/api/generated/enums.js';
	import { notServedReason, type EntryKind } from '#lib/nss.js';
	import ConfigProse from './ConfigProse.svelte';

	// Whether hosts are served a stored entry, and if not why, as the first
	// thing its page says after the name. The worst case on these pages is an
	// admin concluding the wrong way and fixing the wrong layer, so the
	// verdict is the server's (`served` present, or `not_served`) and is never
	// worked out here from the state or the marks.
	interface Props {
		kind: EntryKind;
		/** True when the server sent a served record. */
		served: boolean;
		/** The served account's status; absent for a group. */
		status?: string;
		/** The server's not_served. */
		reason?: string;
		/** The row's withheld_reason, said when the reason is withheld. */
		withheldReason?: string;
		testid?: string;
	}

	let { kind, served, status, reason = '', withheldReason = '', testid }: Props = $props();

	// What a served account's status adds: it is still served, but locked.
	const servedNote = $derived.by(() => {
		if (status === PosixStatusDisabled) {
			return 'It is served disabled, with nss.disabled_shell as its shell.';
		}
		if (status === PosixStatusDeleted) {
			return 'It is served as deleted until nss.tombstone_retention ends.';
		}
		return '';
	});
</script>

<div class="flex flex-wrap items-baseline gap-x-2 gap-y-1 text-sm" data-testid={testid}>
	{#if served}
		<span
			class="rounded bg-granted-surface px-2.5 py-1 text-xs font-semibold text-granted"
			data-testid={testid ? `${testid}-word` : undefined}>Served</span
		>
		<span class="text-ink">
			Hosts that look up this {kind} get the record below.
			{#if servedNote}<ConfigProse text={servedNote} />{/if}
		</span>
	{:else}
		<span
			class="rounded bg-danger-surface px-2.5 py-1 text-xs font-semibold text-danger"
			data-testid={testid ? `${testid}-word` : undefined}>Not served</span
		>
		<span class="text-ink" data-testid={testid ? `${testid}-reason` : undefined}>
			<ConfigProse text={notServedReason(reason, kind, withheldReason)} />
			Hosts that look it up get nothing.
		</span>
	{/if}
</div>
