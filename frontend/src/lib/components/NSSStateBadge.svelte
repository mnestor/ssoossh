<script lang="ts">
	import {
		NSSEntryStateActive,
		NSSEntryStateDisabled,
		NSSEntryStateMissing,
		NSSEntryStatePending,
		NSSEntryStateTombstone,
		NSSEntryStateWithheld,
		type NSSEntryState
	} from '#lib/api/generated/enums.js';
	import { entryStateLabel } from '#lib/nss.js';

	// A stored NSS account's or group's state as a chip, shaped like
	// KeyStatusBadge but with the word alone: six states would each need a
	// glyph of their own, and the word is what a reader compares. Coloured by
	// what the state does to hosts: active is served as stored; missing and
	// pending are served but want a look; disabled and withheld lock or hide
	// the entry; a tombstone is a settled fact.
	interface Props {
		state: NSSEntryState | string;
		testid?: string;
	}

	let { state, testid }: Props = $props();

	const tones: Record<NSSEntryState, string> = {
		[NSSEntryStateActive]: 'bg-granted-surface text-granted',
		[NSSEntryStateMissing]: 'bg-trimmed-surface text-trimmed',
		[NSSEntryStatePending]: 'bg-trimmed-surface text-trimmed',
		[NSSEntryStateDisabled]: 'bg-danger-surface text-danger',
		[NSSEntryStateWithheld]: 'bg-danger-surface text-danger',
		[NSSEntryStateTombstone]: 'bg-surface-muted text-ink-muted'
	};
</script>

<span
	class="inline-flex flex-shrink-0 items-center rounded px-2.5 py-1 text-xs font-semibold {tones[
		state as NSSEntryState
	] ?? 'bg-surface-muted text-ink-muted'}"
	data-testid={testid}
>
	{entryStateLabel(state)}
</span>
