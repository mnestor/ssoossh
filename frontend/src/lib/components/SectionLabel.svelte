<script lang="ts">
	import type { Snippet } from 'svelte';

	// The small uppercase label that opens a group of fields. It is the only
	// small uppercase label left in the app now that PageHeading's accent
	// eyebrow is gone, and it stays muted: it names a section within a card,
	// not the page.
	//
	// Two elements, one look. Without `for` it is a `div` naming a group of
	// rows — which is most of its uses, and a `label` pointing at nothing
	// would be a lie. With `for` it is the real `<label>` for one control,
	// which is what the LDAP probe needed: its "Filter" and "Attributes"
	// captions looked like labels, sat where labels sit, and named nothing
	// as far as an assistive technology was concerned, leaving both fields
	// with no accessible name but a placeholder.
	interface Props {
		/** The id of the control this names. Omit when it heads a group. */
		for?: string;
		children: Snippet;
	}

	let { for: forId, children }: Props = $props();

	const cls = 'mb-1.5 block text-[11px] font-semibold tracking-[0.06em] text-ink-muted uppercase';
</script>

{#if forId}
	<label for={forId} class={cls}>{@render children()}</label>
{:else}
	<div class={cls}>{@render children()}</div>
{/if}
