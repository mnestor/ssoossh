<script lang="ts">
	import { formatDateTime } from '#lib/format.js';

	// The Added cell of a stored membership, on an NSS account's memberships
	// and a group's members: when the server first stored the membership,
	// or "Before first import" for one the first full import stored, whose
	// date in the directory is unknowable (it does not date member values).

	interface Props {
		/** The membership's added_at; absent when the first import stored it. */
		at?: string;
	}

	const { at }: Props = $props();
</script>

{#if at}
	<time datetime={at} class="tabular-nums">{formatDateTime(at)}</time>
{:else}
	<span class="text-ink-muted">Before first import</span>
{/if}
