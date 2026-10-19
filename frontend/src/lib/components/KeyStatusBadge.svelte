<script lang="ts">
	import {
		NSSKeyStatusActive,
		NSSKeyStatusExpired,
		NSSKeyStatusRevoked
	} from '#lib/api/generated/enums.js';
	import { KeyRotating, type KeyDisplayStatus } from '#lib/nss.js';
	import Icon from './Icon.svelte';

	// A fleet key's state as a chip, shaped like StatusBadge: glyph plus word,
	// coloured by meaning. Its own component because StatusBadge is typed to
	// the certificate request statuses, and a key is a different object.
	//
	// Rotating is a warning rather than a success: two secrets are accepted
	// and the old one is about to stop, which is the window an operator has
	// to watch. Expired and revoked are settled facts, so neither takes the
	// warning triangle; revoked is danger because someone ended it.
	interface Props {
		status: KeyDisplayStatus;
		testid?: string;
	}

	let { status, testid }: Props = $props();

	const variants: Record<KeyDisplayStatus, string> = {
		[NSSKeyStatusActive]: 'bg-granted-surface text-granted',
		[KeyRotating]: 'bg-trimmed-surface text-trimmed',
		[NSSKeyStatusExpired]: 'bg-surface-muted text-ink-muted',
		[NSSKeyStatusRevoked]: 'bg-danger-surface text-danger'
	};

	const icons: Record<KeyDisplayStatus, string> = {
		[NSSKeyStatusActive]: 'server-2',
		[KeyRotating]: 'rotate',
		[NSSKeyStatusExpired]: 'clock-cancel',
		[NSSKeyStatusRevoked]: 'server-off'
	};
</script>

<span
	class="inline-flex flex-shrink-0 items-center gap-1.5 rounded px-2.5 py-1 text-xs font-semibold capitalize {variants[
		status
	] ?? 'bg-surface-muted text-ink-muted'}"
	data-testid={testid}
>
	<Icon name={icons[status] ?? 'help-circle'} size="xs" />
	{status}
</span>
