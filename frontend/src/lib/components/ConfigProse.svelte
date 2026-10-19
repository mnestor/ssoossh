<script lang="ts">
	import { splitConfigKeys } from '#lib/nss.js';

	// One sentence with the config keys inside it set in mono, as every
	// config key is (DESIGN.md, "Where the mono goes"). For the NSS viewer's
	// explanations, which are written once in #lib/nss as plain sentences.
	interface Props {
		text: string;
	}

	let { text }: Props = $props();

	const segments = $derived(splitConfigKeys(text));
</script>

{#each segments as segment, index (index)}{#if segment.key}<code>{segment.text}</code
		>{:else}{segment.text}{/if}{/each}
