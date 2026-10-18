<script lang="ts">
	import { NSSEntryStateActive } from '#lib/api/generated/enums.js';
	import { formatDateTime } from '#lib/format.js';
	import { entryStateLabel, entryStateMeaning, type EntryKind } from '#lib/nss.js';
	import ConfigProse from './ConfigProse.svelte';
	import DetailRow from './DetailRow.svelte';
	import NSSDirectoryRef from './NSSDirectoryRef.svelte';
	import PageSection from './PageSection.svelte';

	// The "why is this not plain active" section of a stored account's or
	// group's page: what the state means for hosts, then every mark the row
	// carries, since several can hold at once (a withheld row can also have a
	// pending change). Rendered only when the state is not active; the marks
	// are the server's, shown as sent.
	interface Props {
		kind: EntryKind;
		state: string;
		missingSince?: string;
		deletedAt?: string;
		withheldAt?: string;
		withheldReason?: string;
		/** The stored and the directory's numbers, for a pending change. */
		uid?: number;
		pendingUid?: number;
		/** The stored gid; absent when the entry has none, so none is pending. */
		gid?: number;
		pendingGid?: number;
		pendingSince?: string;
		/** An account's own disabled marks, for a disabled state. */
		dirDisabled?: boolean;
		personDisabledAt?: string;
	}

	let {
		kind,
		state,
		missingSince,
		deletedAt,
		withheldAt,
		withheldReason,
		uid,
		pendingUid,
		gid,
		pendingGid,
		pendingSince,
		dirDisabled = false,
		personDisabledAt
	}: Props = $props();

	const pending = $derived(pendingUid !== undefined || pendingGid !== undefined);
	const withheld = $derived(!!withheldAt || !!withheldReason);
</script>

{#if state !== NSSEntryStateActive}
	<PageSection title="Entry state" testid="nss-marks">
		<p class="max-w-[70ch] text-sm text-ink" data-testid="nss-marks-meaning">
			<span class="font-semibold">{entryStateLabel(state)}.</span>
			<ConfigProse text={entryStateMeaning(state, kind)} />
		</p>
		<dl class="mt-3 flex flex-col divide-y divide-border-subtle">
			{#if dirDisabled}
				<DetailRow label="Directory flag">
					<span data-testid="nss-marks-dir-disabled"
						>Disabled in the directory (<code>nss.disabled_filter</code>)</span
					>
				</DetailRow>
			{/if}
			{#if personDisabledAt}
				<DetailRow label="Person disabled">
					<span data-testid="nss-marks-person-disabled"
						>The linked person was disabled at {formatDateTime(personDisabledAt)}</span
					>
				</DetailRow>
			{/if}
			{#if missingSince}
				<DetailRow label="Missing since">
					<span data-testid="nss-marks-missing">{formatDateTime(missingSince)}</span>
				</DetailRow>
			{/if}
			{#if deletedAt}
				<DetailRow label="Tombstone since">
					<span data-testid="nss-marks-tombstone">{formatDateTime(deletedAt)}</span>
				</DetailRow>
			{/if}
			{#if withheld}
				<DetailRow label="Withheld">
					<span class="flex flex-col gap-1" data-testid="nss-marks-withheld">
						{#if withheldAt}<span>Since {formatDateTime(withheldAt)}</span>{/if}
						{#if withheldReason}<span>{withheldReason}</span>{/if}
						<span class="text-ink-muted"
							><NSSDirectoryRef section="collisions" testid="nss-marks-collisions" /></span
						>
					</span>
				</DetailRow>
			{/if}
			{#if pending}
				<DetailRow label="Pending change">
					<span class="flex flex-col gap-1" data-testid="nss-marks-pending">
						{#if pendingUid !== undefined}
							<span
								>uid <span class="font-mono">{uid}</span> to
								<span class="font-mono font-semibold">{pendingUid}</span></span
							>
						{/if}
						{#if pendingGid !== undefined}
							<span
								>gid <span class="font-mono">{gid}</span> to
								<span class="font-mono font-semibold">{pendingGid}</span></span
							>
						{/if}
						{#if pendingSince}<span>Seen since {formatDateTime(pendingSince)}</span>{/if}
						<span class="text-ink-muted"
							><NSSDirectoryRef section="pending" testid="nss-marks-pending-ref" /></span
						>
					</span>
				</DetailRow>
			{/if}
		</dl>
	</PageSection>
{/if}
